// Package store is the only place that talks SQL. Authorization-relevant
// queries live here alongside the rules they enforce, so that a new route
// cannot accidentally skip them.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/db"
)

// ErrNotFound is returned for a missing row. Callers translate it into the
// API's 404, which is deliberately also the answer for "exists but you may not
// see it".
var ErrNotFound = errors.New("not found")

type Store struct {
	pool  *db.Pool
	Clock clock.Clock
}

func New(pool *db.Pool, clk clock.Clock) *Store {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Store{pool: pool, Clock: clk}
}

func (s *Store) Pool() *db.Pool { return s.pool }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return db.InTx(ctx, s.pool, fn)
}

// querier is satisfied by both the pool and a transaction, so every query
// helper can run inside or outside a transaction.
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *Store) q(tx pgx.Tx) querier {
	if tx != nil {
		return tx
	}
	return s.pool
}

func norm(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// isUniqueViolation reports a duplicate-key error, used to make inserts
// idempotent without a round trip.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports a missing referenced row.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%s: %w", op, err)
}
