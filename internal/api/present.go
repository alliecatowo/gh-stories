package api

import (
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// The JSON shapes below mirror packages/contracts/openapi.yaml exactly. A
// contract test asserts the router's routes match the document; these structs
// keep the payloads matching too.

type publicUser struct {
	GitHubUserID int64  `json:"github_user_id"`
	Login        string `json:"login"`
	AvatarURL    string `json:"avatar_url"`
	ProfileURL   string `json:"profile_url"`
	HasAccount   bool   `json:"has_account"`
}

func presentIdentity(id domain.Identity) publicUser {
	return publicUser{
		GitHubUserID: int64(id.GitHubID),
		Login:        id.Login,
		AvatarURL:    id.AvatarURL,
		ProfileURL:   id.ProfileURL,
		HasAccount:   id.Registered,
	}
}

func presentUser(u *domain.User) publicUser {
	return publicUser{
		GitHubUserID: int64(u.GitHubID),
		Login:        u.Login,
		AvatarURL:    u.AvatarURL,
		ProfileURL:   u.ProfileURL,
		HasAccount:   true,
	}
}

type mediaVariant struct {
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	MIME       string `json:"mime"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	ByteSize   int64  `json:"byte_size,omitempty"`
	HasAudio   bool   `json:"has_audio,omitempty"`
}

type storyItem struct {
	ID             string         `json:"id"`
	Author         publicUser     `json:"author"`
	State          string         `json:"state"`
	Caption        string         `json:"caption,omitempty"`
	AltText        string         `json:"alt_text,omitempty"`
	Visibility     string         `json:"visibility,omitempty"`
	AudienceListID string         `json:"audience_list_id,omitempty"`
	AudienceLabel  string         `json:"audience_label,omitempty"`
	AllowReplies   bool           `json:"allow_replies"`
	AllowReactions bool           `json:"allow_reactions"`
	MediaKind      string         `json:"media_kind,omitempty"`
	Variants       []mediaVariant `json:"variants,omitempty"`
	PublishedAt    *time.Time     `json:"published_at,omitempty"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
	Seen           bool           `json:"seen"`
	IsOwner        bool           `json:"is_owner"`
	ViewerCount    *int           `json:"viewer_count,omitempty"`
	ReactionCount  *int           `json:"reaction_count,omitempty"`
	MyReaction     string         `json:"my_reaction,omitempty"`
	FailureCode    string         `json:"failure_code,omitempty"`
	FailureMessage string         `json:"failure_message,omitempty"`
}

// mediaPath builds the authorization-gateway path for a variant.
//
// These are never public object URLs and never signed bearer URLs: every
// request is re-checked against session, audience, block, hide, suspension,
// deletion and expiry.
func mediaPath(storyID uuid.UUID, kind domain.VariantKind) string {
	return "/v1/media/" + storyID.String() + "/" + string(kind)
}

type presentOpts struct {
	viewer     *domain.User
	seen       bool
	myReaction string
	// ownerCounts are attached only when the caller is the author.
	views, reactions *int
}

func presentStory(it *domain.StoryItem, opts presentOpts) storyItem {
	isOwner := opts.viewer != nil && opts.viewer.ID == it.AuthorUserID
	out := storyItem{
		ID:             it.ID.String(),
		State:          string(it.State),
		Caption:        it.Caption,
		AltText:        it.AltText,
		Visibility:     string(it.Visibility),
		AllowReplies:   it.AllowReplies,
		AllowReactions: it.AllowReactions,
		PublishedAt:    it.PublishedAt,
		ExpiresAt:      it.ExpiresAt,
		Seen:           opts.seen,
		IsOwner:        isOwner,
		MyReaction:     opts.myReaction,
		FailureCode:    it.FailureCode,
		FailureMessage: it.FailureMessage,
	}
	if it.Author != nil {
		out.Author = presentIdentity(*it.Author)
	}
	if it.AudienceListID != nil {
		out.AudienceListID = it.AudienceListID.String()
	}
	// The plain-language audience is shown to the author. Other viewers are
	// not told how narrowly they were selected.
	if isOwner {
		out.AudienceLabel = it.Visibility.Label()
		out.ViewerCount = opts.views
		out.ReactionCount = opts.reactions
	}
	if len(it.Variants) > 0 {
		out.MediaKind = string(it.MediaKind())
		for _, v := range it.Variants {
			out.Variants = append(out.Variants, mediaVariant{
				Kind:       string(v.Kind),
				URL:        mediaPath(it.ID, v.Kind),
				MIME:       v.MIME,
				Width:      v.Width,
				Height:     v.Height,
				DurationMS: v.DurationMS,
				ByteSize:   v.ByteSize,
				HasAudio:   v.HasAudio,
			})
		}
	}
	return out
}

type authorGroup struct {
	Author           publicUser  `json:"author"`
	Items            []storyItem `json:"items"`
	HasUnseen        bool        `json:"has_unseen"`
	Muted            bool        `json:"muted"`
	FollowProvenance string      `json:"follow_provenance,omitempty"`
}

func presentGroup(g store.FeedGroup, viewer *domain.User, reactions map[uuid.UUID]string) authorGroup {
	out := authorGroup{
		Author:           presentIdentity(g.Author),
		HasUnseen:        g.HasUnseen,
		Muted:            g.Muted,
		FollowProvenance: g.Provenance,
	}
	for i, it := range g.Items {
		seen := false
		if i < len(g.Seen) {
			seen = g.Seen[i]
		}
		out.Items = append(out.Items, presentStory(it, presentOpts{
			viewer: viewer, seen: seen, myReaction: reactions[it.ID],
		}))
	}
	return out
}

type feedResponse struct {
	Me         *authorGroup  `json:"me,omitempty"`
	Groups     []authorGroup `json:"groups"`
	NextCursor string        `json:"next_cursor,omitempty"`
	ServerTime time.Time     `json:"server_time"`
	Version    string        `json:"version,omitempty"`
}

type ringStatus struct {
	GitHubUserID int64  `json:"github_user_id"`
	Login        string `json:"login,omitempty"`
	HasActive    bool   `json:"has_active"`
	HasUnseen    bool   `json:"has_unseen"`
	Muted        bool   `json:"muted"`
	IsSelf       bool   `json:"is_self"`
}

type sessionInfo struct {
	ID          string    `json:"id"`
	ClientKind  string    `json:"client_kind"`
	ClientLabel string    `json:"client_label,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Current     bool      `json:"current"`
}

func presentSession(s domain.Session, currentID uuid.UUID) sessionInfo {
	return sessionInfo{
		ID:          s.ID.String(),
		ClientKind:  string(s.ClientKind),
		ClientLabel: s.ClientLabel,
		CreatedAt:   s.CreatedAt,
		LastUsedAt:  s.LastUsedAt,
		ExpiresAt:   s.ExpiresAt,
		Current:     s.ID == currentID,
	}
}

type relationship struct {
	User       publicUser `json:"user"`
	Provenance string     `json:"provenance,omitempty"`
	FollowsMe  bool       `json:"follows_me"`
	Muted      bool       `json:"muted"`
	Blocked    bool       `json:"blocked"`
	Hidden     bool       `json:"hidden"`
}

func presentRelationship(r store.RelationshipRow) relationship {
	return relationship{
		User:       presentIdentity(r.Identity),
		Provenance: string(r.Provenance),
		FollowsMe:  r.FollowsMe,
		Muted:      r.Muted,
		Blocked:    r.Blocked,
		Hidden:     r.Hidden,
	}
}

type relationshipPage struct {
	Relationships []relationship `json:"relationships"`
	NextCursor    string         `json:"next_cursor,omitempty"`
}

type audienceList struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Members     []publicUser `json:"members"`
	MemberCount int          `json:"member_count"`
}

func presentAudienceList(l store.AudienceList) audienceList {
	out := audienceList{ID: l.ID.String(), Name: l.Name, MemberCount: len(l.Members)}
	out.Members = make([]publicUser, 0, len(l.Members))
	for _, m := range l.Members {
		out.Members = append(out.Members, presentIdentity(m))
	}
	return out
}

type viewerEntry struct {
	User     publicUser `json:"user"`
	ViewedAt time.Time  `json:"viewed_at"`
	Reaction string     `json:"reaction,omitempty"`
}

type viewerList struct {
	StoryID string        `json:"story_id"`
	Total   int           `json:"total"`
	Viewers []viewerEntry `json:"viewers"`
}

type replyResponse struct {
	ID           string     `json:"id"`
	StoryID      string     `json:"story_id"`
	Sender       publicUser `json:"sender"`
	Recipient    publicUser `json:"recipient"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	StoryExpired bool       `json:"story_expired"`
}

type inboxEntry struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Actor        publicUser `json:"actor"`
	StoryID      string     `json:"story_id,omitempty"`
	StoryExpired bool       `json:"story_expired"`
	StoryThumb   string     `json:"story_thumb_url,omitempty"`
	Body         string     `json:"body,omitempty"`
	Emoji        string     `json:"emoji,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
}

func presentInboxEntry(e store.InboxEntry) inboxEntry {
	out := inboxEntry{
		ID:           e.ID.String(),
		Kind:         string(e.Kind),
		Actor:        presentIdentity(e.Actor),
		StoryExpired: e.StoryExpired,
		Body:         e.Body,
		Emoji:        e.Emoji,
		CreatedAt:    e.CreatedAt,
		ReadAt:       e.ReadAt,
	}
	if e.StoryID != nil {
		out.StoryID = e.StoryID.String()
		// A thumbnail only exists while the Story is alive. Once it has
		// expired the context becomes "Story expired" and no media, not even a
		// permanent thumbnail, is retained.
		if e.HasThumb {
			out.StoryThumb = mediaPath(*e.StoryID, domain.VariantThumb)
		}
	}
	return out
}

type inboxResponse struct {
	Entries    []inboxEntry `json:"entries"`
	Unread     int          `json:"unread"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type meResponse struct {
	User            publicUser `json:"user"`
	NeedsOnboarding bool       `json:"needs_onboarding"`
	IsModerator     bool       `json:"is_moderator"`
	UnreadInbox     int        `json:"unread_inbox"`
	// ServerTime lets clients correct ordinary clock skew rather than
	// trusting the device clock for expiry.
	ServerTime time.Time `json:"server_time"`
}

type settingsResponse struct {
	DefaultVisibility     string         `json:"default_visibility"`
	DefaultAudienceListID string         `json:"default_audience_list_id,omitempty"`
	DefaultAllowReplies   bool           `json:"default_allow_replies"`
	DefaultAllowReactions bool           `json:"default_allow_reactions"`
	AudienceLists         []audienceList `json:"audience_lists"`
	HiddenFrom            []publicUser   `json:"hidden_from"`
	Muted                 []publicUser   `json:"muted"`
	Blocked               []publicUser   `json:"blocked"`
	Sessions              []sessionInfo  `json:"sessions"`
	ReplyRetentionDays    int            `json:"reply_retention_days"`
}

type healthResponse struct {
	Status                   string            `json:"status"`
	Version                  string            `json:"version"`
	Commit                   string            `json:"commit"`
	Checks                   map[string]string `json:"checks,omitempty"`
	MediaJobsBacklog         int               `json:"media_jobs_backlog"`
	OldestMediaJobAgeSeconds int               `json:"oldest_media_job_age_seconds"`
	CleanupBacklog           int               `json:"cleanup_backlog"`
}

func presentIdentities(ids []domain.Identity) []publicUser {
	out := make([]publicUser, 0, len(ids))
	for _, id := range ids {
		out = append(out, presentIdentity(id))
	}
	return out
}
