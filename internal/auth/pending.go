package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// ErrPendingLoginNotFound covers both "no such pending login" and "this
// pending login already left the pending state" — a client polling or
// approving should not be able to distinguish "wrong id" from "too late".
var ErrPendingLoginNotFound = errors.New("auth: pending login not found or no longer pending")

// ErrUserCodeMismatch is returned by ApprovePendingLogin when the code the
// approving user confirmed does not match the pending record. This is what
// stops a phishing flow where an attacker starts a login on their own
// device, then tricks a victim into approving what they believe is their
// own login by feeding the victim a fake or wrong code on a page they
// control — the approval only succeeds if the person approving is actually
// looking at the same code the CLI displayed.
var ErrUserCodeMismatch = errors.New("auth: user code does not match")

// pollIntervalSeconds is the interval the CLI is told to poll at. It is also
// used, together with cfg.PendingLoginTTL, to size the poll budget the store
// enforces (see maxPollsFor).
const pollIntervalSeconds = 5

// PendingLoginResult is returned to the CLI when it starts a login. It holds
// the only copy of PollingSecret in existence outside the database's hash of
// it; losing it means the CLI must start over.
type PendingLoginResult struct {
	ID              uuid.UUID
	PollingSecret   string
	UserCode        string
	VerificationURL string
	ExpiresAt       time.Time
	IntervalSeconds int
}

// CreatePendingLogin starts a service-mediated CLI login: the CLI receives a
// user code to display and a polling secret to poll with, and a human then
// approves or denies the login on an already-authenticated screen (the
// browser) by confirming that same user code.
func (s *Service) CreatePendingLogin(ctx context.Context, kind domain.ClientKind, label string) (*PendingLoginResult, error) {
	secret, err := NewToken()
	if err != nil {
		return nil, err
	}
	userCode, err := NewUserCode()
	if err != nil {
		return nil, err
	}
	p, err := s.store.CreatePendingLogin(ctx, sha256Sum(secret), userCode, kind, label, s.cfg.PendingLoginTTL)
	if err != nil {
		return nil, err
	}
	verificationURL := ""
	if s.cfg.PublicURL != nil {
		verificationURL = s.cfg.PublicURL.String() + "/device"
	}
	return &PendingLoginResult{
		ID:              p.ID,
		PollingSecret:   secret,
		UserCode:        p.UserCode,
		VerificationURL: verificationURL,
		ExpiresAt:       p.ExpiresAt,
		IntervalSeconds: pollIntervalSeconds,
	}, nil
}

// ApprovePendingLogin records a human's explicit approval of a pending CLI
// login. Approval requires the caller to be an authenticated user (userID)
// looking at a screen showing the requesting client's label and user code;
// userCodeTyped is what they confirmed there.
//
// The user-code check is the security-critical step: it is what stops an
// attacker who started a pending login (e.g. via a phishing page mimicking
// this product) from getting a victim to approve *their* login by luring the
// victim into an "approve" flow that isn't showing the victim's own code. If
// the code doesn't match, nothing is approved and no session is issued.
func (s *Service) ApprovePendingLogin(ctx context.Context, pendingID, userID uuid.UUID, userCodeTyped string) error {
	p, err := s.store.PendingLogin(ctx, pendingID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrPendingLoginNotFound
		}
		return err
	}
	if p.State != "pending" {
		return ErrPendingLoginNotFound
	}
	typed := strings.ToUpper(strings.TrimSpace(userCodeTyped))
	if !constantTimeEqual(typed, p.UserCode) {
		return ErrUserCodeMismatch
	}

	// Issue the real session now: its hash goes into the sessions table
	// immediately (revocable from the moment it exists), and the raw token
	// is separately parked in pending_logins.issued_token for exactly one
	// poll to collect. The two are independent: revoking the session later
	// does not touch this approval record, and this approval record being
	// consumed does not itself create the session — it already exists.
	token, sess, err := s.IssueSession(ctx, userID, p.ClientKind, p.ClientLabel, "")
	if err != nil {
		return err
	}
	if err := s.store.ApprovePendingLogin(ctx, pendingID, userID, sess.ID, token); err != nil {
		// The session row now exists but was never handed to anyone and
		// carries no PII beyond a label; it will simply expire unused. We
		// do not attempt to revoke it here to keep this path free of a
		// second failure mode — an unused, eventually-expiring session is a
		// strictly smaller problem than compounding an existing error.
		if errors.Is(err, store.ErrNotFound) {
			return ErrPendingLoginNotFound
		}
		return err
	}
	return nil
}

// DenyPendingLogin marks a pending login as denied so a subsequent poll
// reports denial rather than leaving the CLI to time out.
func (s *Service) DenyPendingLogin(ctx context.Context, pendingID uuid.UUID) error {
	return s.store.DenyPendingLogin(ctx, pendingID)
}

// Poll checks the status of a pending login. It is bounded, rate limited,
// expiring and single-use by construction of the underlying store call: the
// poll budget is enforced in the same transaction that reads state, and a
// successful "approved" read atomically clears the stored token so a second
// poll cannot retrieve it again.
func (s *Service) Poll(ctx context.Context, pendingID uuid.UUID, pollingSecret string) (*store.PollResult, error) {
	return s.store.PollPendingLogin(ctx, pendingID, sha256Sum(pollingSecret), s.maxPolls())
}

// maxPolls sizes the poll budget from the configured TTL and interval, with
// slack for client-side jitter, so a well-behaved CLI polling at the
// suggested interval never runs out of budget before the login itself
// expires.
func (s *Service) maxPolls() int {
	budget := int(s.cfg.PendingLoginTTL/(pollIntervalSeconds*time.Second)) + 12
	if budget < 1 {
		budget = 1
	}
	return budget
}
