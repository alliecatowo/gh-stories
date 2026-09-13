// Package credstore persists the CLI's Stories session token, preferring the
// operating system credential store and falling back to a clearly-selected,
// permission-restricted file when no keyring is available (e.g. headless
// Linux with no Secret Service running).
//
// It never touches the user's existing `gh` CLI token. `gh api user` may be
// used elsewhere to hint at which GitHub account is active locally, but that
// token is never proof of identity for Stories and is never read, extracted
// or uploaded by this package.
package credstore

import (
	"errors"
	"time"
)

// ErrNotFound is returned by Load when no credential is stored for the given
// service URL.
var ErrNotFound = errors.New("credstore: no credential stored for this service")

// Credential is one stored Stories session, scoped to the service it was
// issued by. Multiple services (e.g. the default hosted service and a
// self-hosted deployment) can each hold their own credential at once, which
// is how account switching works: log in to a different --service URL and
// the previous one's credential is left untouched.
type Credential struct {
	// ServiceURL is the base URL of the Stories service this token is valid
	// for. It is the key credentials are stored and looked up by.
	ServiceURL string `json:"service_url"`
	// Token is the opaque bearer session token issued by the service.
	Token string `json:"token"`
	// Login is the GitHub login of the account this session belongs to, kept
	// for display purposes (e.g. `doctor`, `logout` confirmation prompts).
	Login string `json:"login"`
	// UserID is the Stories account's numeric GitHub id.
	UserID int64 `json:"user_id"`
	// StoredAt is when this credential was written, in UTC.
	StoredAt time.Time `json:"stored_at"`
}

// Store persists and retrieves Credentials, keyed by ServiceURL.
type Store interface {
	// Load returns the credential stored for serviceURL, or ErrNotFound if
	// none exists.
	Load(serviceURL string) (*Credential, error)
	// Save stores cred, replacing any existing credential for the same
	// ServiceURL. Other services' credentials are left untouched.
	Save(cred Credential) error
	// Delete removes the credential for serviceURL, if any. It is not an
	// error to delete a service URL with nothing stored. Other services'
	// credentials are left untouched.
	Delete(serviceURL string) error
	// List returns every stored credential, across all service URLs.
	List() ([]Credential, error)
	// Backend reports which storage mechanism is in use: "keyring" or
	// "file". `doctor` reports this honestly so the user knows which they
	// got.
	Backend() string
}

// Open returns the best available Store for this machine: the OS credential
// store (macOS Keychain, Windows Credential Manager, or libsecret on Linux)
// when it is actually usable, or the permission-restricted file store
// otherwise. It never fails purely because a keyring is unavailable — that
// just selects the file backend.
func Open() (Store, error) {
	if ks, ok := newKeyringStore(); ok {
		return ks, nil
	}
	return newFileStore()
}
