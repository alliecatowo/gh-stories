package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/ratelimit"
)

// routes wires the /v1 contract.
//
// Grouping is by required authorization, not by feature, so it is visually
// obvious which routes are reachable anonymously. Everything under the
// authenticated group runs requireAuth before its handler.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(withRequestID)
	r.Use(s.recovery)
	r.Use(securityHeaders)
	r.Use(s.cors)
	r.Use(s.authenticate)
	r.Use(s.logging)
	r.Use(timeout(30 * time.Second))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, httpx.NotFound())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, httpx.Err(http.StatusMethodNotAllowed,
			httpx.CodeBadRequest, "That method is not allowed here."))
	})

	r.Route("/v1", func(r chi.Router) {
		// ---------------------------------------------------- anonymous
		r.Get("/health/live", h(s.getLive))
		r.Get("/health/ready", h(s.getReady))

		r.Group(func(r chi.Router) {
			r.Use(s.limit(ratelimit.RuleLoginStart, "login-start"))
			r.Get("/auth/github/start", h(s.getAuthStart))
			r.Get("/auth/github/callback", h(s.getAuthCallback))
			r.Post("/auth/cli/pending", h(s.postPendingLogin))
			r.Post("/auth/device/start", h(s.postDeviceLoginStart))
		})
		r.With(s.limit(ratelimit.RuleLoginPoll, "login-poll")).
			Post("/auth/cli/poll", h(s.postPollLogin))
		r.With(s.limit(ratelimit.RuleLoginPoll, "login-poll")).
			Post("/auth/device/poll", h(s.postDeviceLoginPoll))

		// Compiled in only under the `ghs_testidp` build tag, and refused at
		// runtime outside local/test. A no-op in every release build.
		s.registerTestRoutes(r)

		// ------------------------------------------------- public stories
		// A live public Story is world-readable: its metadata, its media and
		// its render acknowledgement accept anonymous callers. The handlers
		// resolve everything through PublicStory; any non-public, expired,
		// deleted, removed or suspended item is the same 404 everywhere.
		// Everything else under /v1 stays session-gated.
		r.With(s.limit(ratelimit.RuleRead, "read-public")).Group(func(r chi.Router) {
			r.Get("/stories/{storyId}", h(s.getStory))
		})
		r.With(s.limit(ratelimit.RuleMedia, "media-public")).Group(func(r chi.Router) {
			r.Get("/media/{storyId}/{variant}", h(s.getMedia))
			r.Head("/media/{storyId}/{variant}", h(s.getMedia))
		})
		r.With(s.limit(ratelimit.RuleMutation, "view-public")).
			Post("/stories/{storyId}/view", h(s.postView))

		// ------------------------------------------------- authenticated
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)

			r.With(s.limit(ratelimit.RuleRead, "read")).Group(func(r chi.Router) {
				r.Get("/me", h(s.getMe))
				r.Get("/feed", h(s.getFeed))
				r.Get("/stories/mine", h(s.getOwnStories))
				r.Get("/users/lookup", h(s.getUserLookup))
				r.Get("/users/{login}/stories", h(s.getAuthorStories))
				r.Get("/stories/{storyId}/viewers", h(s.getViewers))
				r.Get("/inbox", h(s.getInbox))
				r.Get("/following", h(s.listFollowing))
				r.Get("/followers", h(s.listFollowers))
				r.Get("/mutes", h(s.listSimple("mutes", "muter_user_id", "muted_github_user_id")))
				r.Get("/blocks", h(s.listSimple("blocks", "blocker_user_id", "blocked_github_user_id")))
				r.Get("/hides", h(s.listSimple("hide_rules", "author_user_id", "hidden_github_user_id")))
				r.Get("/settings", h(s.getSettings))
				r.Get("/audience-lists", h(s.listAudienceLists))
				r.Get("/auth/sessions", h(s.listAuthSessions))
			})

			// Ring status is called on every page of GitHub, so it gets its
			// own, more generous budget.
			r.With(s.limit(ratelimit.RuleStatus, "status")).
				Post("/stories/status", h(s.postStatus))

			// Authenticated media goes through the same public gateway
			// handlers: they serve private bytes with no-store when a
			// session is present and public bytes with short caching when
			// it is not. No second route exists, so no weaker rule can
			// drift in.

			r.With(s.limit(ratelimit.RuleUpload, "upload")).Group(func(r chi.Router) {
				r.Post("/uploads", h(s.postUpload))
				r.Post("/uploads/{uploadId}/finalize", h(s.postFinalize))
			})

			r.With(s.limit(ratelimit.RuleReply, "reply")).
				Post("/stories/{storyId}/replies", h(s.postReply))
			r.With(s.limit(ratelimit.RuleReaction, "reaction")).Group(func(r chi.Router) {
				r.Put("/stories/{storyId}/reaction", h(s.putReaction))
				r.Delete("/stories/{storyId}/reaction", h(s.deleteReaction))
			})
			r.With(s.limit(ratelimit.RuleReport, "report")).Post("/reports", h(s.postReport))

			r.With(s.limit(ratelimit.RuleMutation, "mutation")).Group(func(r chi.Router) {
				r.Patch("/stories/{storyId}", h(s.patchStory))
				r.Delete("/stories/{storyId}", h(s.deleteStory))
				r.Post("/inbox/read", h(s.postInboxRead))
				r.Post("/onboarding/import-follows", h(s.postImportFollows))

				r.Put("/following/{login}", h(s.putFollow))
				r.Delete("/following/{login}", h(s.deleteFollow))
				r.Put("/mutes/{login}", h(s.toggleRelation("mute", true)))
				r.Delete("/mutes/{login}", h(s.toggleRelation("mute", false)))
				r.Put("/blocks/{login}", h(s.toggleRelation("block", true)))
				r.Delete("/blocks/{login}", h(s.toggleRelation("block", false)))
				r.Put("/hides/{login}", h(s.toggleRelation("hide", true)))
				r.Delete("/hides/{login}", h(s.toggleRelation("hide", false)))

				r.Patch("/settings", h(s.patchSettings))
				r.Delete("/settings/account", h(s.deleteAccount))
				r.Post("/audience-lists", h(s.createAudienceList))
				r.Patch("/audience-lists/{listId}", h(s.patchAudienceList))
				r.Delete("/audience-lists/{listId}", h(s.deleteAudienceList))

				r.Post("/auth/logout", h(s.postLogout))
				r.Delete("/auth/sessions/{sessionId}", h(s.revokeSession))
			})

			// ------------------------------------------- moderators only
			// Deny-by-default, and answers 404 rather than 403 so the queue's
			// existence is not advertised to ordinary users.
			r.Group(func(r chi.Router) {
				r.Use(requireModerator)
				r.Get("/moderation/reports", h(s.listReports))
				r.Post("/moderation/actions", h(s.postModerationAction))
			})
		})
	})
	return r
}

// h adapts an error-returning handler to http.HandlerFunc.
func h(fn httpx.Handler) http.HandlerFunc { return fn.ServeHTTP }
