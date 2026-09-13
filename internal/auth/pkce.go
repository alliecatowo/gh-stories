// Package auth implements the GitHub OAuth authorization-code-with-PKCE
// flow, this service's own revocable sessions, and the service-mediated
// "pending login" flow the CLI uses to sign in over SSH without ever seeing
// a browser. Identity is always real GitHub OAuth; nothing here trusts a
// claim from the client about who they are — every authorization
// revalidates against GitHub itself.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
)

// verifierBytes is the amount of randomness behind a PKCE code verifier. 32
// raw bytes base64url-encodes (no padding) to 43 characters — the minimum
// length RFC 7636 allows and comfortably within its 43-128 range — while the
// base64url alphabet (A-Z a-z 0-9 - _) is itself a subset of the RFC's
// "unreserved" character set, so no further filtering is needed.
const verifierBytes = 32

// stateBytes and tokenBytes are both 32 raw bytes (256 bits), which is the
// entropy floor this package targets for anything an attacker could try to
// guess or replay.
const (
	stateBytes = 32
	tokenBytes = 32
)

// NewVerifier generates a fresh PKCE code verifier per RFC 7636: 43-128
// characters from the unreserved character set, sourced from crypto/rand.
// The verifier is held server side for the lifetime of one authorization
// attempt and never sent to GitHub or the browser — only its S256 Challenge
// is.
func NewVerifier() (string, error) {
	b := make([]byte, verifierBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Challenge computes the S256 PKCE code challenge for a verifier:
// base64url(sha256(verifier)) with no padding. This package always uses
// S256, never the weaker "plain" method.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewState generates an unguessable OAuth state value. State binds the
// authorization request we sent to the callback we receive, so a forged or
// replayed callback can be told apart from a real one.
func NewState() (string, error) {
	b := make([]byte, stateBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashState one-way transforms a state value before it is used as a database
// lookup key, so a database dump contains no state value an attacker could
// replay directly — same rationale as store.HashToken for session tokens.
func HashState(state string) []byte {
	return sha256Sum(state)
}

// NewToken generates an opaque, >=256-bit-entropy bearer token for a Stories
// session. The raw token is returned to the caller exactly once and is never
// itself stored — store.HashToken (sha256) is what lands in the database.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// userCodeAlphabet deliberately excludes 0/O, 1/I/L: characters that are easy
// to confuse when a person is reading a code off one screen and typing or
// confirming it on another, which is exactly how the pending-login flow uses
// it.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const (
	userCodeGroupLen   = 4
	userCodeGroupCount = 2
)

// NewUserCode generates a short, human-matchable code such as "WDJB-MJHT"
// for the pending-login (device-style) flow: the CLI displays it, and the
// person approving the login on a separate, already-authenticated screen
// must see the same code before their approval is accepted. It is sourced
// from crypto/rand via rejection-free uniform sampling (crypto/rand.Int),
// not a biased modulo reduction.
func NewUserCode() (string, error) {
	var b strings.Builder
	for i := 0; i < userCodeGroupLen*userCodeGroupCount; i++ {
		if i > 0 && i%userCodeGroupLen == 0 {
			b.WriteByte('-')
		}
		c, err := randomAlphabetByte(userCodeAlphabet)
		if err != nil {
			return "", err
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

func randomAlphabetByte(alphabet string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
	if err != nil {
		return 0, fmt.Errorf("auth: generate user code: %w", err)
	}
	return alphabet[n.Int64()], nil
}

// sha256Sum is the shared one-way transform behind HashState and the
// pending-login polling-secret hash: neither value should be recoverable
// from a database dump.
func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// constantTimeEqual compares two strings without leaking their contents
// through a timing side channel — used for the user-code check in
// ApprovePendingLogin, which is the one place in this package that compares
// a secret-ish value in application code (everywhere else, an equality
// check is a hashed lookup the database performs).
func constantTimeEqual(a, b string) bool {
	// subtle.ConstantTimeCompare itself returns early on differing lengths
	// with no timing dependency on content, which is the property we need:
	// user codes are a fixed, non-secret-length format, so a length
	// mismatch reveals nothing an attacker didn't already know.
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
