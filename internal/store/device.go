package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// DeviceLogin is the server-side record for one GitHub device
// authorization. The device code is retained only so the server can poll
// GitHub; it is never returned to an HTTP caller.
type DeviceLogin struct {
	ID          uuid.UUID
	DeviceCode  string
	ClientKind  domain.ClientKind
	ClientLabel string
	State       string
	UserID      *uuid.UUID
	ExpiresAt   time.Time
}

// CreateDeviceLogin records a freshly issued GitHub device authorization.
func (s *Store) CreateDeviceLogin(ctx context.Context, deviceCode string,
	kind domain.ClientKind, label string, expiresAt time.Time) (*DeviceLogin, error) {
	var d DeviceLogin
	var kindStr string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO device_logins (device_code, client_kind, client_label, expires_at)
		VALUES ($1,$2,$3,$4)
		RETURNING id, device_code, client_kind, client_label, state, expires_at`,
		deviceCode, string(kind), label, expiresAt).
		Scan(&d.ID, &d.DeviceCode, &kindStr, &d.ClientLabel, &d.State, &d.ExpiresAt)
	d.ClientKind = domain.ClientKind(kindStr)
	return &d, wrap("create device login", err)
}

// PollDeviceLogin reads the row under lock, lazily marking expiry, and
// consumes a previously approved token exactly once. It returns the row
// without the device code for approval decisions plus the one-shot token
// when this poll is the one that collects it.
func (s *Store) PollDeviceLogin(ctx context.Context, id uuid.UUID) (*DeviceLogin, string, error) {
	now := s.Clock.Now()
	var d DeviceLogin
	var token *string
	var kind string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var code string
		err := tx.QueryRow(ctx, `
			SELECT device_code, client_kind, client_label, state, user_id, expires_at, issued_token
			FROM device_logins WHERE id=$1 FOR UPDATE`, id).
			Scan(&code, &kind, &d.ClientLabel, &d.State, &d.UserID, &d.ExpiresAt, &token)
		if err != nil {
			return norm(err)
		}
		d.ID, d.DeviceCode, d.ClientKind = id, code, domain.ClientKind(kind)
		if d.State == "pending" && !now.Before(d.ExpiresAt) {
			d.State = "expired"
			if _, err := tx.Exec(ctx,
				`UPDATE device_logins SET state='expired' WHERE id=$1`, id); err != nil {
				return wrap("expire device login", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE device_logins SET poll_count = poll_count + 1, last_poll_at = $2 WHERE id=$1`,
			id, now); err != nil {
			return wrap("count device poll", err)
		}
		if d.State == "approved" && token != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE device_logins SET state='consumed', issued_token=NULL WHERE id=$1`, id); err != nil {
				return wrap("consume device login", err)
			}
			d.State = "consumed"
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if token != nil && d.State == "consumed" {
		return &d, *token, nil
	}
	return &d, "", nil
}

// ApproveDeviceLogin stores the one-shot Stories token when the row is
// still pending. It returns false without an error when a competing poll
// already consumed or expired the row.
func (s *Store) ApproveDeviceLogin(ctx context.Context, id, userID uuid.UUID, token string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE device_logins
		SET state='approved', user_id=$2, issued_token=$3
		WHERE id=$1 AND state='pending' AND expires_at > $4`,
		id, userID, token, s.Clock.Now())
	if err != nil {
		return false, wrap("approve device login", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ExpireDeviceLogin marks a row expired after GitHub reports denial or
// expiry. This keeps poll responses honest without deleting history.
func (s *Store) ExpireDeviceLogin(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE device_logins SET state='expired', issued_token=NULL WHERE id=$1 AND state='pending'`, id)
	return wrap("expire device login", err)
}
