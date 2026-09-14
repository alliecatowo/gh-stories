package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// These are the behavioural acceptance gates from the product brief, executed
// against the real router, real database and real authorization rules.
//
// Controlled identities: A authors, B is eligible, C is the third party who
// must never get anything.

func trio(t *testing.T) (*harness, *actor, *actor, *actor) {
	h := newHarness(t)
	return h, h.actor(2001, "alice"), h.actor(2002, "bob"), h.actor(2003, "carol")
}

// GATE: No private leakage.
//
// C must not obtain an unauthorized ring/status, metadata, media, poster,
// range response, reply or viewer list by guessing IDs.
func TestNoPrivateLeakageByGuessingIDs(t *testing.T) {
	h, alice, bob, carol := trio(t)
	require.NoError(t, h.store.Follow(t.Context(), nil, alice.User.ID, bob.User.GitHubID, domain.ProvenanceNative))
	story := h.publish(alice, domain.VisibilityFollowersOfAuthor, nil, "private concert")
	id := story.String()

	// Bob is eligible and gets everything he should.
	require.Equal(t, http.StatusOK, bob.get("/v1/stories/"+id).StatusCode)
	require.Equal(t, http.StatusOK, bob.get("/v1/media/"+id+"/image").StatusCode)

	// Carol knows the exact id and still gets nothing, anywhere.
	for _, path := range []string{
		"/v1/stories/" + id,
		"/v1/media/" + id + "/image",
		"/v1/media/" + id + "/thumb",
		"/v1/media/" + id + "/poster",
		"/v1/media/" + id + "/terminal",
		"/v1/stories/" + id + "/viewers",
	} {
		resp := carol.get(path)
		require.Equal(t, http.StatusNotFound, resp.StatusCode,
			"C must not reach %s", path)
		drain(resp)
	}

	// A ranged request must not become a side door.
	resp := carol.getRange("/v1/media/"+id+"/image", "bytes=0-10")
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a range request must be authorized exactly like a full request")
	drain(resp)

	// Interactions are refused too.
	require.Equal(t, http.StatusNotFound,
		carol.do(http.MethodPost, "/v1/stories/"+id+"/replies",
			map[string]string{"body": "hi"}).StatusCode)
	require.Equal(t, http.StatusNotFound,
		carol.do(http.MethodPut, "/v1/stories/"+id+"/reaction",
			map[string]string{"emoji": "❤️"}).StatusCode)

	// And the ring status is not an oracle.
	statuses := decode[struct {
		Entries []ringStatus `json:"entries"`
	}](t, carol.do(http.MethodPost, "/v1/stories/status",
		map[string]any{"github_user_ids": []int64{int64(alice.User.GitHubID)}}))
	require.Len(t, statuses.Entries, 1)
	require.False(t, statuses.Entries[0].HasActive,
		"an inaccessible active Story must be indistinguishable from no Story")
}

// GATE: Viewer list is author-only, even for an eligible viewer.
func TestViewerListIsAuthorOnly(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	require.Equal(t, http.StatusOK, alice.get("/v1/stories/"+story.String()+"/viewers").StatusCode)
	resp := bob.get("/v1/stories/" + story.String() + "/viewers")
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"an eligible viewer must not read the named viewer list")
	drain(resp)
}

// GATE: Read state. Delivering media records exactly one view; feed and status
// requests never do.
func TestViewRecordedOnMediaDeliveryOnly(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	id := story.String()

	drain(bob.get("/v1/feed"))
	drain(bob.do(http.MethodPost, "/v1/stories/status",
		map[string]any{"github_user_ids": []int64{int64(alice.User.GitHubID)}}))
	drain(bob.get("/v1/stories/" + id))

	viewers := decode[viewerList](t, alice.get("/v1/stories/"+id+"/viewers"))
	require.Zero(t, viewers.Total,
		"fetching the feed, the ring status or the metadata must not mark a view")

	// Now actually deliver the bytes, twice.
	drain(bob.get("/v1/media/" + id + "/image"))
	drain(bob.get("/v1/media/" + id + "/image"))

	viewers = decode[viewerList](t, alice.get("/v1/stories/"+id+"/viewers"))
	require.Equal(t, 1, viewers.Total, "repeated delivery to one viewer is one view")
	require.Equal(t, "bob", viewers.Viewers[0].User.Login)

	// The author viewing their own Story is not a viewer.
	drain(alice.get("/v1/media/" + id + "/image"))
	viewers = decode[viewerList](t, alice.get("/v1/stories/"+id+"/viewers"))
	require.Equal(t, 1, viewers.Total)
}

// GATE: A thumbnail is not content-bearing, so it does not create a view.
func TestThumbnailDoesNotRecordAView(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	drain(bob.get("/v1/media/" + story.String() + "/thumb"))
	viewers := decode[viewerList](t, alice.get("/v1/stories/"+story.String()+"/viewers"))
	require.Zero(t, viewers.Total)
}

// GATE: Revocation. Delete, block, hide, a narrowed audience or a suspension
// must stop subsequent media requests on an already-known route.
func TestRevocationStopsKnownMediaRoutes(t *testing.T) {
	ctx := t.Context()

	t.Run("deletion", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		require.Equal(t, http.StatusNoContent,
			alice.do(http.MethodDelete, "/v1/stories/"+story.String(), nil).StatusCode)
		require.Equal(t, http.StatusNotFound, bob.get(path).StatusCode,
			"a known media URL must stop working the instant the Story is deleted")
	})

	t.Run("block", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		require.Equal(t, http.StatusNoContent,
			alice.do(http.MethodPut, "/v1/blocks/bob", nil).StatusCode)
		require.Equal(t, http.StatusNotFound, bob.get(path).StatusCode)
	})

	t.Run("hide", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		require.Equal(t, http.StatusNoContent,
			alice.do(http.MethodPut, "/v1/hides/bob", nil).StatusCode)
		require.Equal(t, http.StatusNotFound, bob.get(path).StatusCode)
	})

	t.Run("narrowed audience", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		// Narrow to mutuals, which Bob is not.
		resp := alice.do(http.MethodPatch, "/v1/stories/"+story.String(),
			map[string]any{"visibility": "mutuals"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		drain(resp)
		require.Equal(t, http.StatusNotFound, bob.get(path).StatusCode,
			"narrowing the audience must revoke newly unauthorized requests")
	})

	t.Run("suspension", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		require.NoError(t, h.store.SuspendUser(ctx, nil, alice.User.ID, "policy"))
		require.Equal(t, http.StatusNotFound, bob.get(path).StatusCode)
	})

	t.Run("session revocation", func(t *testing.T) {
		h, alice, bob, _ := trio(t)
		story := h.publish(alice, domain.VisibilityPublic, nil, "")
		path := "/v1/media/" + story.String() + "/image"
		require.Equal(t, http.StatusOK, bob.get(path).StatusCode)

		require.Equal(t, http.StatusNoContent,
			bob.do(http.MethodPost, "/v1/auth/logout", nil).StatusCode)
		// A revoked session is now an anonymous caller. A live public Story
		// stays world-readable (200, publicly cached); identity-gated
		// access stops immediately.
		resp := bob.get(path)
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"public media stays anonymous-readable after logout")
		require.Contains(t, resp.Header.Get("Cache-Control"), "public")
		drain(resp)
		resp = bob.get("/v1/me")
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"a revoked session must stop working immediately on identity-gated routes")
		drain(resp)
	})
}

// GATE: Expiry across every surface, at the exact boundary.
func TestExpiryAcrossEverySurface(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "cat")
	id := story.String()

	h.clock.Advance(24*time.Hour - time.Microsecond)
	require.Equal(t, http.StatusOK, bob.get("/v1/stories/"+id).StatusCode)
	require.Equal(t, http.StatusOK, bob.get("/v1/media/"+id+"/image").StatusCode)
	require.Equal(t, http.StatusOK, bob.get("/v1/media/"+id+"/thumb").StatusCode)

	feed := decode[feedResponse](t, bob.get("/v1/feed"))
	require.Len(t, feed.Groups, 1)

	// Cross the boundary. At exactly expires_at the item is expired.
	h.clock.Advance(time.Microsecond)

	require.Equal(t, http.StatusNotFound, bob.get("/v1/stories/"+id).StatusCode)
	require.Equal(t, http.StatusNotFound, bob.get("/v1/media/"+id+"/image").StatusCode)
	require.Equal(t, http.StatusNotFound, bob.get("/v1/media/"+id+"/thumb").StatusCode,
		"thumbnails expire with the Story")

	resp := bob.getRange("/v1/media/"+id+"/image", "bytes=0-5")
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a range request must not outlive expiry")
	drain(resp)

	feed = decode[feedResponse](t, bob.get("/v1/feed"))
	require.Empty(t, feed.Groups)

	statuses := decode[struct {
		Entries []ringStatus `json:"entries"`
	}](t, bob.do(http.MethodPost, "/v1/stories/status",
		map[string]any{"github_user_ids": []int64{int64(alice.User.GitHubID)}}))
	require.False(t, statuses.Entries[0].HasActive)

	require.Equal(t, http.StatusNotFound,
		bob.do(http.MethodPost, "/v1/stories/"+id+"/replies",
			map[string]string{"body": "late"}).StatusCode,
		"interactions must not survive expiry")
	require.Equal(t, http.StatusNotFound,
		bob.do(http.MethodPut, "/v1/stories/"+id+"/reaction",
			map[string]string{"emoji": "🔥"}).StatusCode)
	require.Equal(t, http.StatusNotFound,
		alice.get("/v1/media/"+id+"/image").StatusCode,
		"even the author stops getting the bytes after expiry")
}

// GATE: Independent clocks.
func TestPostingAgainDoesNotExtendAnEarlierItem(t *testing.T) {
	h, alice, bob, _ := trio(t)
	first := h.publish(alice, domain.VisibilityPublic, nil, "first")
	item := decode[storyItem](t, bob.get("/v1/stories/"+first.String()))
	firstExpiry := *item.ExpiresAt

	h.clock.Advance(6 * time.Hour)
	second := h.publish(alice, domain.VisibilityPublic, nil, "second")

	item = decode[storyItem](t, bob.get("/v1/stories/"+first.String()))
	require.Equal(t, firstExpiry, *item.ExpiresAt,
		"posting another item must not extend the first item's expiry")

	h.clock.Advance(18 * time.Hour)
	require.Equal(t, http.StatusNotFound, bob.get("/v1/stories/"+first.String()).StatusCode)
	require.Equal(t, http.StatusOK, bob.get("/v1/stories/"+second.String()).StatusCode)
}

// GATE: Inbox. Only permitted participants see private replies; disable-replies
// and block rules hold.
func TestInboxPermissions(t *testing.T) {
	h, alice, bob, carol := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	id := story.String()

	resp := bob.do(http.MethodPost, "/v1/stories/"+id+"/replies", map[string]string{"body": "lmao"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	drain(resp)

	// Alice, the recipient, sees it.
	inbox := decode[inboxResponse](t, alice.get("/v1/inbox"))
	require.Len(t, inbox.Entries, 1)
	require.Equal(t, "lmao", inbox.Entries[0].Body)
	require.Equal(t, 1, inbox.Unread)

	// Carol cannot enumerate anyone else's exchange.
	inbox = decode[inboxResponse](t, carol.get("/v1/inbox"))
	require.Empty(t, inbox.Entries)

	// Bob's own outgoing reply is not in his inbox as an incoming event.
	inbox = decode[inboxResponse](t, bob.get("/v1/inbox"))
	require.Empty(t, inbox.Entries)

	// Turning replies off is enforced.
	resp = alice.do(http.MethodPatch, "/v1/stories/"+id, map[string]any{"allow_replies": false})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	drain(resp)
	require.Equal(t, http.StatusForbidden,
		carol.do(http.MethodPost, "/v1/stories/"+id+"/replies",
			map[string]string{"body": "nope"}).StatusCode)
}

// GATE: Reactions are one-per-user, replaceable and removable, and respect the
// author's toggle.
func TestReactions(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	id := story.String()

	for _, emoji := range []string{"❤️", "💀"} {
		resp := bob.do(http.MethodPut, "/v1/stories/"+id+"/reaction",
			map[string]string{"emoji": emoji})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		drain(resp)
	}
	item := decode[storyItem](t, alice.get("/v1/stories/"+id))
	require.NotNil(t, item.ReactionCount)
	require.Equal(t, 1, *item.ReactionCount, "one reaction per user, replaced not added")

	// A reaction outside the allowed set is refused.
	require.Equal(t, http.StatusBadRequest,
		bob.do(http.MethodPut, "/v1/stories/"+id+"/reaction",
			map[string]string{"emoji": "🚀"}).StatusCode)

	require.Equal(t, http.StatusNoContent,
		bob.do(http.MethodDelete, "/v1/stories/"+id+"/reaction", nil).StatusCode)
	item = decode[storyItem](t, alice.get("/v1/stories/"+id))
	require.Equal(t, 0, *item.ReactionCount)
}

// GATE: Idempotency. An interrupted reply retried must not duplicate.
func TestReplyIdempotency(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	id := story.String()

	send := func(key string) *http.Response {
		return bob.doWithKey(http.MethodPost, "/v1/stories/"+id+"/replies",
			map[string]string{"body": "same message"}, key)
	}
	first := decode[replyResponse](t, send("retry-1"))
	second := decode[replyResponse](t, send("retry-1"))
	require.Equal(t, first.ID, second.ID, "a retried reply must return the original, not a duplicate")

	inbox := decode[inboxResponse](t, alice.get("/v1/inbox"))
	require.Len(t, inbox.Entries, 1, "the recipient must not receive it twice")

	// The same key with a different body is a conflict rather than a silent
	// wrong answer.
	resp := bob.doWithKey(http.MethodPost, "/v1/stories/"+id+"/replies",
		map[string]string{"body": "different"}, "retry-1")
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	drain(resp)
}

// GATE: Anonymous callers get exactly the public surface and nothing else.
//
// Public launch decision: a live public Story's metadata, media and render
// acknowledgement are world-readable without a session. Identity-gated
// surfaces (account, feed, inbox, settings, viewers, non-public Stories)
// still refuse anonymous callers.
func TestAnonymousIsRefused(t *testing.T) {
	h, alice, _, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	followersOnly := h.publish(alice, domain.VisibilityFollowersOfAuthor, nil, "")
	anon := &actor{h: h}
	for _, path := range []string{
		"/v1/me", "/v1/feed", "/v1/inbox", "/v1/settings",
		"/v1/stories/" + followersOnly.String(),
		"/v1/media/" + followersOnly.String() + "/image",
		"/v1/stories/" + story.String() + "/viewers",
	} {
		resp := anon.get(path)
		code := resp.StatusCode
		drain(resp)
		require.Contains(t, []int{http.StatusUnauthorized, http.StatusNotFound}, code,
			"%s must not be world-readable", path)
	}
	// The public surface itself is anonymous-readable.
	resp := anon.get("/v1/stories/" + story.String())
	require.Equal(t, http.StatusOK, resp.StatusCode, "public metadata must be anonymous-readable")
	drain(resp)
	resp = anon.get("/v1/media/" + story.String() + "/image")
	require.Equal(t, http.StatusOK, resp.StatusCode, "public media must be anonymous-readable")
	require.Contains(t, resp.Header.Get("Cache-Control"), "public")
	drain(resp)
}

// GATE: Moderator routes are deny-by-default and do not advertise themselves.
func TestModerationIsDenyByDefault(t *testing.T) {
	_, _, bob, _ := trio(t)
	resp := bob.get("/v1/moderation/reports")
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"an ordinary user must not learn that a moderation queue exists")
	drain(resp)
}

// GATE: Media caching is split by audience. Session-gated bytes must not be
// shareable-cacheable, or a later request could bypass the permission check.
// Anonymous public bytes are short-cacheable and vary on Authorization so the
// two never mix in a shared cache.
func TestMediaIsNotShareableCacheable(t *testing.T) {
	h, alice, bob, _ := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "")
	resp := bob.get("/v1/media/" + story.String() + "/image")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	cc := resp.Header.Get("Cache-Control")
	require.Contains(t, cc, "no-store")
	require.Contains(t, cc, "private")
	require.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

// GATE: Moderation. A report reaches a queue only a moderator can see, and
// actions are recorded and take effect.
func TestModerationFlow(t *testing.T) {
	h, alice, bob, carol := trio(t)
	ctx := t.Context()

	story := h.publish(alice, domain.VisibilityPublic, nil, "reported")
	id := story.String()

	// Bob reports it.
	resp := bob.do(http.MethodPost, "/v1/reports", map[string]any{
		"subject_kind": "story", "story_id": id,
		"reason": "spam", "details": "unsolicited",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	drain(resp)

	// Nobody can reach the queue yet — not even the reporter.
	for _, who := range []*actor{alice, bob, carol} {
		r := who.get("/v1/moderation/reports")
		require.Equal(t, http.StatusNotFound, r.StatusCode,
			"the moderation queue must not be reachable by ordinary accounts")
		drain(r)
	}

	// Carol is appointed a moderator by configuration.
	granted, _, err := h.store.SyncModerators(ctx, []domain.GitHubID{carol.User.GitHubID})
	require.NoError(t, err)
	require.Equal(t, 1, granted)

	queue := decode[struct {
		Reports []map[string]any `json:"reports"`
	}](t, carol.get("/v1/moderation/reports"))
	require.Len(t, queue.Reports, 1)
	require.Equal(t, "spam", queue.Reports[0]["reason"])

	// A moderator can remove the Story, and it really goes.
	resp = carol.do(http.MethodPost, "/v1/moderation/actions", map[string]any{
		"kind": "remove_story", "story_id": id, "reason": "policy",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	drain(resp)

	require.Equal(t, http.StatusNotFound, bob.get("/v1/stories/"+id).StatusCode,
		"a removed Story must stop being served")
	require.Equal(t, http.StatusNotFound, bob.get("/v1/media/"+id+"/image").StatusCode)

	// Suspension stops the author entirely.
	resp = carol.do(http.MethodPost, "/v1/moderation/actions", map[string]any{
		"kind": "suspend_user", "login": "alice", "reason": "repeated",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	drain(resp)

	r := alice.get("/v1/feed")
	require.Equal(t, http.StatusForbidden, r.StatusCode,
		"a suspended account can neither act nor be seen")
	drain(r)

	// And the report is resolved rather than left open forever.
	open := decode[struct {
		Reports []map[string]any `json:"reports"`
	}](t, carol.get("/v1/moderation/reports?state=open"))
	require.Empty(t, open.Reports)
}

// GATE: A report cannot be used to probe for private Story ids.
func TestReportingCannotProbeForPrivateStories(t *testing.T) {
	h, alice, _, carol := trio(t)
	story := h.publish(alice, domain.VisibilityMutuals, nil, "private")

	resp := carol.do(http.MethodPost, "/v1/reports", map[string]any{
		"subject_kind": "story", "story_id": story.String(), "reason": "spam",
	})
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"reporting a Story you cannot see must be indistinguishable from it not existing")
	drain(resp)
}

// GATE: Public launch — a live public Story is world-readable without a
// session; every other visibility stays session-gated. Anonymous media is
// cacheable and increments only the aggregate counter: no named viewer row
// and no IP history is retained.
func TestPublicStoryReadableAnonymously(t *testing.T) {
	h, alice, _, carol := trio(t)
	story := h.publish(alice, domain.VisibilityPublic, nil, "hello world")
	id := story.String()

	anon := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, h.srv.URL+path, nil)
		require.NoError(t, err)
		resp, err := h.srv.Client().Do(req)
		require.NoError(t, err)
		return resp
	}

	// Anonymous metadata works for public.
	resp := anon(http.MethodGet, "/v1/stories/"+id)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	item := decode[storyItem](t, resp)
	require.Equal(t, "public", item.Visibility)

	// Anonymous media works and is cacheable.
	resp = anon(http.MethodGet, "/v1/media/"+id+"/image")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Cache-Control"), "public")
	body := drain(resp)
	require.NotEmpty(t, body)

	// Anonymous render acknowledgement is a no-op 204 that stores nothing.
	resp = anon(http.MethodPost, "/v1/stories/"+id+"/view")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	drain(resp)

	// No named viewer row was created for the anonymous fetch.
	viewers := decode[viewerList](t, alice.get("/v1/stories/"+id+"/viewers"))
	require.Zero(t, viewers.Total, "anonymous delivery must not create a named view")

	// The aggregate counter did move.
	n, err := h.store.AnonymousViewCount(t.Context(), story)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// A followers-only Story stays invisible anonymously.
	private := h.publish(alice, domain.VisibilityFollowersOfAuthor, nil, "private")
	require.Equal(t, http.StatusNotFound,
		anon(http.MethodGet, "/v1/stories/"+private.String()).StatusCode)
	require.Equal(t, http.StatusNotFound,
		anon(http.MethodGet, "/v1/media/"+private.String()+"/image").StatusCode)

	// ... but an eligible signed-in caller still gets it, privately cached.
	require.Equal(t, http.StatusOK, carol.get("/v1/stories/"+id).StatusCode)
	resp = carol.get("/v1/media/" + id + "/image")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Cache-Control"), "private")
	drain(resp)
}
