// Package cliapi is a typed client for the GitHub Stories HTTP API described
// by packages/contracts/openapi.yaml. Every type here mirrors a schema from
// that contract; field names and JSON tags are kept in lockstep with it
// deliberately, so a diff against the contract is enough to spot drift.
package cliapi

import "time"

// PublicUser is a Stories account (or a GitHub identity that hasn't signed
// up yet) as shown to other people.
type PublicUser struct {
	GitHubID   int64  `json:"github_user_id"`
	Login      string `json:"login"`
	AvatarURL  string `json:"avatar_url"`
	ProfileURL string `json:"profile_url"`
	HasAccount bool   `json:"has_account"`
}

// Me is the caller's own account, as returned by GET /me.
type Me struct {
	User            PublicUser `json:"user"`
	NeedsOnboarding bool       `json:"needs_onboarding"`
	IsModerator     bool       `json:"is_moderator"`
	UnreadInbox     int        `json:"unread_inbox"`
	ServerTime      time.Time  `json:"server_time"`
}

// SessionInfo describes one of the caller's active Stories sessions.
type SessionInfo struct {
	ID          string    `json:"id"`
	ClientKind  string    `json:"client_kind"`
	ClientLabel string    `json:"client_label"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Current     bool      `json:"current"`
}

// PendingLogin is returned by POST /auth/cli/pending: the code and URL a
// person uses to approve a device-flow-style login.
type PendingLogin struct {
	PendingLoginID  string    `json:"pending_login_id"`
	PollingSecret   string    `json:"polling_secret"`
	UserCode        string    `json:"user_code"`
	VerificationURL string    `json:"verification_url"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// PendingLoginPoll is one POST /auth/cli/poll response.
type PendingLoginPoll struct {
	Status    string      `json:"status"` // pending, approved, denied, expired
	Token     string      `json:"token,omitempty"`
	ExpiresAt time.Time   `json:"expires_at,omitempty"`
	User      *PublicUser `json:"user,omitempty"`
}

// MediaVariant is one rendition of a Story item's media.
type MediaVariant struct {
	Kind       string `json:"kind"` // image, video, poster, thumb, terminal
	URL        string `json:"url"`
	MIME       string `json:"mime"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DurationMS int    `json:"duration_ms"`
	ByteSize   int64  `json:"byte_size"`
	HasAudio   bool   `json:"has_audio"`
}

// Variant returns the first variant of the given kind, or nil.
func (s *StoryItem) Variant(kind string) *MediaVariant {
	for i := range s.Variants {
		if s.Variants[i].Kind == kind {
			return &s.Variants[i]
		}
	}
	return nil
}

// StoryItem is one Story item (photo or video), the unit everything in this
// product revolves around.
type StoryItem struct {
	ID             string         `json:"id"`
	Author         PublicUser     `json:"author"`
	State          string         `json:"state"` // processing, published, failed, deleted, removed
	Caption        string         `json:"caption"`
	AltText        string         `json:"alt_text"`
	Visibility     string         `json:"visibility"`
	AudienceListID string         `json:"audience_list_id,omitempty"`
	AudienceLabel  string         `json:"audience_label"`
	AllowReplies   bool           `json:"allow_replies"`
	AllowReactions bool           `json:"allow_reactions"`
	MediaKind      string         `json:"media_kind"` // image, video
	Variants       []MediaVariant `json:"variants"`
	PublishedAt    *time.Time     `json:"published_at,omitempty"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
	Seen           bool           `json:"seen"`
	IsOwner        bool           `json:"is_owner"`
	ViewerCount    int            `json:"viewer_count"`
	ReactionCount  int            `json:"reaction_count"`
	MyReaction     string         `json:"my_reaction,omitempty"`
	FailureCode    string         `json:"failure_code,omitempty"`
	FailureMessage string         `json:"failure_message,omitempty"`
}

// Expired reports whether the item is past its own expiry as of now. The
// boundary is exclusive, matching internal/domain.StoryItem.Expired: at
// exactly ExpiresAt the item is expired.
func (s *StoryItem) Expired(now time.Time) bool {
	if s.ExpiresAt == nil {
		return false
	}
	return !now.Before(*s.ExpiresAt)
}

// AuthorGroup is one author's active, authorized Story sequence.
type AuthorGroup struct {
	Author           PublicUser  `json:"author"`
	Items            []StoryItem `json:"items"`
	HasUnseen        bool        `json:"has_unseen"`
	Muted            bool        `json:"muted"`
	FollowProvenance string      `json:"follow_provenance,omitempty"`
}

// Feed is one page of the authorized Story feed, grouped by author.
type Feed struct {
	Me         *AuthorGroup  `json:"me,omitempty"`
	Groups     []AuthorGroup `json:"groups"`
	NextCursor string        `json:"next_cursor,omitempty"`
	ServerTime time.Time     `json:"server_time"`
	Version    string        `json:"version,omitempty"`
}

// UploadIntentRequest declares the media a caller wants to upload, before
// any bytes are sent.
type UploadIntentRequest struct {
	MIME           string `json:"mime"`
	ByteSize       int64  `json:"byte_size"`
	Filename       string `json:"filename,omitempty"`
	Caption        string `json:"caption,omitempty"`
	AltText        string `json:"alt_text,omitempty"`
	Visibility     string `json:"visibility,omitempty"`
	AudienceListID string `json:"audience_list_id,omitempty"`
	AllowReplies   *bool  `json:"allow_replies,omitempty"`
	AllowReactions *bool  `json:"allow_reactions,omitempty"`
}

// UploadIntent authorizes uploading exactly one server-chosen private
// object.
type UploadIntent struct {
	UploadID  string            `json:"upload_id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
	MaxBytes  int64             `json:"max_bytes"`
}

// StoryUpdate is a partial update to a Story item's caption, audience or
// interaction settings. Nil pointer fields are left unchanged by the
// server.
type StoryUpdate struct {
	Caption        *string `json:"caption,omitempty"`
	AltText        *string `json:"alt_text,omitempty"`
	Visibility     *string `json:"visibility,omitempty"`
	AudienceListID *string `json:"audience_list_id,omitempty"`
	AllowReplies   *bool   `json:"allow_replies,omitempty"`
	AllowReactions *bool   `json:"allow_reactions,omitempty"`
}

// Viewer is one named entry in a Story's viewer list.
type Viewer struct {
	User     PublicUser `json:"user"`
	ViewedAt time.Time  `json:"viewed_at"`
	Reaction string     `json:"reaction,omitempty"`
}

// ViewerList is the author-only named viewer list for one Story item.
type ViewerList struct {
	StoryID string   `json:"story_id"`
	Total   int      `json:"total"`
	Viewers []Viewer `json:"viewers"`
}

// Reaction is the caller's reaction to a Story item.
type Reaction struct {
	StoryID   string    `json:"story_id"`
	Emoji     string    `json:"emoji"`
	CreatedAt time.Time `json:"created_at"`
}

// Reply is a private reply sent to a Story's author.
type Reply struct {
	ID           string     `json:"id"`
	StoryID      string     `json:"story_id"`
	Sender       PublicUser `json:"sender"`
	Recipient    PublicUser `json:"recipient"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	StoryExpired bool       `json:"story_expired"`
}

// InboxEntry is one reply, reaction or new-follower notification.
type InboxEntry struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"` // reply, reaction, follow
	Actor         PublicUser `json:"actor"`
	StoryID       string     `json:"story_id,omitempty"`
	StoryExpired  bool       `json:"story_expired"`
	StoryThumbURL string     `json:"story_thumb_url,omitempty"`
	Body          string     `json:"body,omitempty"`
	Emoji         string     `json:"emoji,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ReadAt        *time.Time `json:"read_at,omitempty"`
	Outgoing      bool       `json:"outgoing"`
}

// Inbox is one page of the caller's private inbox.
type Inbox struct {
	Entries    []InboxEntry `json:"entries"`
	Unread     int          `json:"unread"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// Relationship is a follow/mute/block/hide edge between the caller and
// another account.
type Relationship struct {
	User       PublicUser `json:"user"`
	Provenance string     `json:"provenance,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FollowsMe  bool       `json:"follows_me"`
	Muted      bool       `json:"muted"`
	Blocked    bool       `json:"blocked"`
	Hidden     bool       `json:"hidden"`
}

// RelationshipPage is one page of a following/followers/mutes/blocks/hides
// listing.
type RelationshipPage struct {
	Relationships []Relationship `json:"relationships"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	Total         int            `json:"total"`
}

// AudienceList is a caller-defined custom audience.
type AudienceList struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Members     []PublicUser `json:"members,omitempty"`
	MemberCount int          `json:"member_count"`
}

// Settings is the caller's account settings.
type Settings struct {
	DefaultVisibility     string         `json:"default_visibility"`
	DefaultAudienceListID string         `json:"default_audience_list_id,omitempty"`
	DefaultAllowReplies   bool           `json:"default_allow_replies"`
	DefaultAllowReactions bool           `json:"default_allow_reactions"`
	AudienceLists         []AudienceList `json:"audience_lists"`
	HiddenFrom            []PublicUser   `json:"hidden_from"`
	Muted                 []PublicUser   `json:"muted"`
	Blocked               []PublicUser   `json:"blocked"`
	Sessions              []SessionInfo  `json:"sessions"`
	ReplyRetentionDays    int            `json:"reply_retention_days"`
}

// SettingsUpdate is a partial update to account settings.
type SettingsUpdate struct {
	DefaultVisibility     *string `json:"default_visibility,omitempty"`
	DefaultAudienceListID *string `json:"default_audience_list_id,omitempty"`
	DefaultAllowReplies   *bool   `json:"default_allow_replies,omitempty"`
	DefaultAllowReactions *bool   `json:"default_allow_reactions,omitempty"`
}

// ReportRequest files a report against a Story or a user.
type ReportRequest struct {
	SubjectKind string `json:"subject_kind"` // story, user
	StoryID     string `json:"story_id,omitempty"`
	Login       string `json:"login,omitempty"`
	Reason      string `json:"reason"`
	Details     string `json:"details,omitempty"`
}

// Report is a filed moderation report.
type Report struct {
	ID          string     `json:"id"`
	SubjectKind string     `json:"subject_kind"`
	StoryID     string     `json:"story_id,omitempty"`
	Subject     PublicUser `json:"subject"`
	Reporter    PublicUser `json:"reporter"`
	Reason      string     `json:"reason"`
	Details     string     `json:"details,omitempty"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
}

// RingStatus is one batch feed-ring entry from POST /stories/status.
type RingStatus struct {
	GitHubID  int64  `json:"github_user_id"`
	Login     string `json:"login,omitempty"`
	HasActive bool   `json:"has_active"`
	HasUnseen bool   `json:"has_unseen"`
	Muted     bool   `json:"muted"`
	IsSelf    bool   `json:"is_self"`
}
