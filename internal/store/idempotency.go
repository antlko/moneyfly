package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// IdempotencyRepo stores POST outcomes by client-supplied key.
//
// Quick entry saves optimistically and retries on failure, so the same create can
// arrive twice. Content-based dedup cannot serve here: two identical coffees on
// one day are two real transactions, so replay protection has to be keyed on the
// client's own key.
//
// The signatures use primitive types deliberately, so the transport layer can
// depend on the behaviour without importing this package.
type IdempotencyRepo struct{ db *sql.DB }

// NewIdempotencyRepo builds the repository.
func NewIdempotencyRepo(db *sql.DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

// Find returns a stored outcome. found is false when the key is new.
//
// A key replayed with a different body is a client bug, so it is a conflict rather
// than a silent second create.
func (r *IdempotencyRepo) Find(
	ctx context.Context,
	userID int64,
	key, method, path, requestHash string,
) (found bool, status int, body []byte, err error) {
	var (
		gotMethod, gotPath, gotHash string
	)
	err = r.db.QueryRowContext(ctx, `
		SELECT method, path, request_hash, response_status, response_body
		FROM idempotency_key
		WHERE user_id = ? AND key = ?`, userID, key).
		Scan(&gotMethod, &gotPath, &gotHash, &status, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil, nil
	}
	if err != nil {
		return false, 0, nil, fmt.Errorf("store: reading idempotency key: %w", err)
	}
	if gotMethod != method || gotPath != path || gotHash != requestHash {
		return false, 0, nil, apperr.Conflictf("Idempotency-Key %q was already used for a different request", key)
	}
	return true, status, body, nil
}

// Save records an outcome. A racing duplicate loses harmlessly: the stored
// response is the one the first writer produced.
func (r *IdempotencyRepo) Save(
	ctx context.Context,
	userID int64,
	key, method, path, requestHash string,
	status int,
	body []byte,
	now time.Time,
) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO idempotency_key
			(user_id, key, method, path, request_hash, response_status, response_body, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id, key) DO NOTHING`,
		userID, key, method, path, requestHash, status, body, ts(now))
	if err != nil {
		return fmt.Errorf("store: storing idempotency key: %w", err)
	}
	return nil
}

// Prune removes records older than the retention window, so the table cannot grow
// without bound.
func (r *IdempotencyRepo) Prune(ctx context.Context, userID int64, before time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM idempotency_key WHERE user_id = ? AND created_at < ?`, userID, ts(before))
	if err != nil {
		return fmt.Errorf("store: pruning idempotency keys: %w", err)
	}
	return nil
}
