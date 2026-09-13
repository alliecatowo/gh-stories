// Package domain holds the core types shared by the API, the worker and the
// authorization rules. Nothing here talks to a database or to HTTP.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------- identity

// GitHubID is GitHub's numeric user id. It is the authoritative identity key:
// logins are renameable, ids are not.
type GitHubID int64

// Identity is a GitHub account we know about. It may or may not have signed up
// for Stories; Registered says which.
type Identity struct {
	GitHubID    GitHubID
	Login       string
	AvatarURL   string
	ProfileURL  string
	AccountType string
	RefreshedAt time.Time
	Registered  bool
}

// User is a registered Stories account.
type User struct {
	ID                    uuid.UUID
	GitHubID              GitHubID
	Login                 string
	AvatarURL             string
	ProfileURL            string
	CreatedAt             time.Time
	IsModerator           bool
	SuspendedAt           *time.Time
	DeletedAt             *time.Time
	OnboardedAt           *time.Time
	DefaultVisibility     Visibility
	DefaultAudienceListID *uuid.UUID
	DefaultAllowReplies   bool
	DefaultAllowReactions bool
}

// Active reports whether the account may act and be seen.
func (u *User) Active() bool { return u != nil && u.SuspendedAt == nil && u.DeletedAt == nil }

type ClientKind string

const (
	ClientBrowserExtension ClientKind = "browser_extension"
	ClientCLI              ClientKind = "cli"
	ClientWeb              ClientKind = "web"
)

type Session struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	ClientKind  ClientKind
	ClientLabel string
	UserAgent   string
	CreatedAt   time.Time
	LastUsedAt  time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

func (s *Session) Valid(now time.Time) bool {
	return s != nil && s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// ------------------------------------------------------------ social graph

type Provenance string

const (
	// ProvenanceGitHubImport marks a relationship created by importing who the
	// user follows on GitHub.
	ProvenanceGitHubImport Provenance = "github_import"
	// ProvenanceNative marks a follow made inside Stories. An explicit native
	// follow deterministically upgrades an imported relationship.
	ProvenanceNative Provenance = "stories_native"
)

type RelationshipState string

const (
	RelationshipActive RelationshipState = "active"
	// RelationshipUnfollowed is a tombstone: a later GitHub re-import must not
	// silently resurrect a relationship the user explicitly removed.
	RelationshipUnfollowed RelationshipState = "unfollowed"
)

type Relationship struct {
	FollowerUserID   uuid.UUID
	FolloweeGitHubID GitHubID
	State            RelationshipState
	Provenance       Provenance
	CreatedAt        time.Time
}

// ------------------------------------------------------------- visibility

// Visibility is the audience of one Story item. It is evaluated server side
// against the *current* graph, so follow changes take effect immediately.
type Visibility string

const (
	// VisibilityFollowersOfAuthor is "People I follow": signed-in users the
	// author follows. This is the default.
	VisibilityFollowersOfAuthor Visibility = "followers_of_author"
	// VisibilityAuthorFollows is "My followers": signed-in users who follow
	// the author.
	VisibilityAuthorFollows Visibility = "author_follows"
	VisibilityMutuals       Visibility = "mutuals"
	VisibilityCustomList    Visibility = "custom_list"
	// VisibilityPublic is "Public — anyone signed in". Identity is still
	// required, because the product exposes named viewers.
	VisibilityPublic Visibility = "public"
)

func (v Visibility) Valid() bool {
	switch v {
	case VisibilityFollowersOfAuthor, VisibilityAuthorFollows, VisibilityMutuals,
		VisibilityCustomList, VisibilityPublic:
		return true
	}
	return false
}

// Label is the plain-language audience name shown before publication.
func (v Visibility) Label() string {
	switch v {
	case VisibilityFollowersOfAuthor:
		return "People I follow"
	case VisibilityAuthorFollows:
		return "My followers"
	case VisibilityMutuals:
		return "Mutuals"
	case VisibilityCustomList:
		return "Custom list"
	case VisibilityPublic:
		return "Public — anyone signed in"
	}
	return string(v)
}

func (v Visibility) Description() string {
	switch v {
	case VisibilityFollowersOfAuthor:
		return "Only people you follow on Stories can see this."
	case VisibilityAuthorFollows:
		return "Only people who follow you on Stories can see this."
	case VisibilityMutuals:
		return "Only people you follow who also follow you back can see this."
	case VisibilityCustomList:
		return "Only the people on the list you choose can see this."
	case VisibilityPublic:
		return "Anyone signed in to GitHub Stories can see this."
	}
	return ""
}

// ------------------------------------------------------------ story items

type StoryState string

const (
	StoryProcessing StoryState = "processing"
	StoryPublished  StoryState = "published"
	StoryFailed     StoryState = "failed"
	StoryDeleted    StoryState = "deleted"
	StoryRemoved    StoryState = "removed"
)

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

type VariantKind string

const (
	VariantImage    VariantKind = "image"
	VariantVideo    VariantKind = "video"
	VariantPoster   VariantKind = "poster"
	VariantThumb    VariantKind = "thumb"
	VariantTerminal VariantKind = "terminal"
)

// ContentBearing reports whether delivering this variant to an authorized
// non-owner should record a view. A poster shows the picture, so it counts;
// a small thumbnail in an inbox row does not.
func (k VariantKind) ContentBearing() bool {
	return k == VariantImage || k == VariantVideo || k == VariantPoster || k == VariantTerminal
}

// StoryItem is one uploaded item. Each item is its own expiring Story: the
// viewer merely groups an author's active items into a sequence, and posting
// another item never extends an older one.
type StoryItem struct {
	ID             uuid.UUID
	AuthorUserID   uuid.UUID
	MediaJobID     uuid.UUID
	State          StoryState
	Caption        string
	AltText        string
	Visibility     Visibility
	AudienceListID *uuid.UUID
	AllowReplies   bool
	AllowReactions bool
	PublishedAt    *time.Time
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	DeletedAt      *time.Time
	FailureCode    string
	FailureMessage string

	Author   *Identity
	Variants []MediaVariant
}

// Expired reports whether the item is past its own expiry. The boundary is
// exclusive: at exactly expires_at the item is expired.
func (s *StoryItem) Expired(now time.Time) bool {
	if s.ExpiresAt == nil {
		return false
	}
	return !now.Before(*s.ExpiresAt)
}

// Live reports whether the item is published and still inside its window.
// Every read and write checks this independently of background cleanup.
func (s *StoryItem) Live(now time.Time) bool {
	return s != nil && s.State == StoryPublished && s.PublishedAt != nil && !s.Expired(now)
}

func (s *StoryItem) MediaKind() MediaKind {
	for _, v := range s.Variants {
		if v.Kind == VariantVideo {
			return MediaVideo
		}
	}
	return MediaImage
}

func (s *StoryItem) Variant(kind VariantKind) *MediaVariant {
	for i := range s.Variants {
		if s.Variants[i].Kind == kind {
			return &s.Variants[i]
		}
	}
	return nil
}

type MediaVariant struct {
	ID         uuid.UUID
	StoryID    uuid.UUID
	Kind       VariantKind
	ObjectKey  string
	MIME       string
	Width      int
	Height     int
	DurationMS int
	ByteSize   int64
	Checksum   string
	HasAudio   bool
}

// -------------------------------------------------------------- reactions

// Reactions are the five available emoji, in display order. There is no
// developer-themed reaction vocabulary.
var Reactions = []string{"❤️", "😂", "🔥", "😭", "💀"}

func ValidReaction(e string) bool {
	for _, r := range Reactions {
		if r == e {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------- pipeline

type UploadState string

const (
	UploadIssued    UploadState = "issued"
	UploadFinalized UploadState = "finalized"
	UploadAborted   UploadState = "aborted"
	UploadExpired   UploadState = "expired"
)

type UploadIntent struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ObjectKey    string
	DeclaredMIME string
	DeclaredSize int64
	State        UploadState
	ActualSize   *int64
	Checksum     *string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type JobState string

const (
	JobQueued    JobState = "queued"
	JobLeased    JobState = "leased"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
)

// MediaJobInput is the immutable worker input frozen at finalize time.
type MediaJobInput struct {
	ObjectKey    string `json:"object_key"`
	DeclaredMIME string `json:"declared_mime"`
	ByteSize     int64  `json:"byte_size"`
	Checksum     string `json:"checksum_sha256"`
	StoryItemID  string `json:"story_item_id"`
	// Filename is advisory metadata only. It is never used to build an object
	// key, a filesystem path, or a subprocess argument.
	Filename string `json:"filename,omitempty"`
}

type MediaJob struct {
	ID             uuid.UUID
	UploadIntentID uuid.UUID
	UserID         uuid.UUID
	State          JobState
	Input          MediaJobInput
	Attempts       int
	MaxAttempts    int
	ErrorCode      string
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ---------------------------------------------------------------- inbox

type InboxKind string

const (
	InboxReply    InboxKind = "reply"
	InboxReaction InboxKind = "reaction"
	InboxFollow   InboxKind = "follow"
)
