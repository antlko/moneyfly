package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// SettingRepo stores the setting and audit_log tables. It implements
// setting.Repo.
type SettingRepo struct{ db *sql.DB }

// NewSettingRepo builds the repository.
func NewSettingRepo(db *sql.DB) *SettingRepo { return &SettingRepo{db: db} }

// settingSelect is a projection fragment; every call site appends a WHERE whose
// first predicate is `user_id = ?`, except DueForRefresh, which is the
// scheduler's cross-user sweep and is annotated at its own call site.
//
// unscoped-query-ok: projection fragment, scoped by every caller
const settingSelect = `
	SELECT id, user_id, key, mode, manual_value, provider_key,
	       refresh_interval_seconds, last_value, last_fetched_at, last_error, updated_at
	FROM setting`

func scanSetting(row interface{ Scan(...any) error }) (setting.Setting, error) {
	var (
		s         setting.Setting
		manual    sql.NullString
		provider  sql.NullString
		interval  sql.NullInt64
		lastValue sql.NullString
		fetchedAt sql.NullString
		lastErr   sql.NullString
		updatedAt string
	)
	if err := row.Scan(&s.ID, &s.UserID, &s.Key, &s.Mode, &manual, &provider,
		&interval, &lastValue, &fetchedAt, &lastErr, &updatedAt); err != nil {
		return setting.Setting{}, err
	}
	s.ManualValue = scanNullString(manual)
	s.ProviderKey = scanNullString(provider)
	if interval.Valid {
		s.RefreshInterval = time.Duration(interval.Int64) * time.Second
	}
	s.LastValue = scanNullString(lastValue)
	s.LastError = scanNullString(lastErr)
	at, err := scanNullTime(fetchedAt)
	if err != nil {
		return setting.Setting{}, err
	}
	s.LastFetchedAt = at
	if s.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return setting.Setting{}, err
	}
	return s, nil
}

// List returns every setting for a user.
func (r *SettingRepo) List(ctx context.Context, userID int64) ([]setting.Setting, error) {
	rows, err := r.db.QueryContext(ctx, settingSelect+` WHERE user_id = ? ORDER BY key`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []setting.Setting{}
	for rows.Next() {
		s, err := scanSetting(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning setting: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns one setting.
func (r *SettingRepo) Get(ctx context.Context, userID int64, key string) (setting.Setting, error) {
	s, err := scanSetting(r.db.QueryRowContext(ctx,
		settingSelect+` WHERE user_id = ? AND key = ?`, userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return setting.Setting{}, apperr.NotFoundf("setting %q", key)
	}
	if err != nil {
		return setting.Setting{}, fmt.Errorf("store: reading setting %q: %w", key, err)
	}
	return s, nil
}

// Upsert writes a setting.
func (r *SettingRepo) Upsert(ctx context.Context, s setting.Setting, now time.Time) (setting.Setting, error) {
	var interval any
	if s.RefreshInterval > 0 {
		interval = int64(s.RefreshInterval / time.Second)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO setting
			(user_id, key, mode, manual_value, provider_key, refresh_interval_seconds,
			 last_value, last_fetched_at, last_error, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id, key) DO UPDATE SET
			mode = excluded.mode,
			manual_value = excluded.manual_value,
			provider_key = excluded.provider_key,
			refresh_interval_seconds = excluded.refresh_interval_seconds,
			updated_at = excluded.updated_at`,
		s.UserID, s.Key, string(s.Mode), nullString(s.ManualValue), nullString(s.ProviderKey),
		interval, nullString(s.LastValue), nullTime(s.LastFetchedAt), nullString(s.LastError), ts(now))
	if err != nil {
		return setting.Setting{}, fmt.Errorf("store: writing setting %q: %w", s.Key, err)
	}
	return r.Get(ctx, s.UserID, s.Key)
}

// RecordFetch stores a provider outcome without touching the user's own fields.
//
// A failure keeps last_value: the point of the chain is that an outage costs
// freshness, never the number itself.
func (r *SettingRepo) RecordFetch(
	ctx context.Context, userID int64, key string, value, errMessage *string, at time.Time,
) error {
	if value != nil {
		_, err := r.db.ExecContext(ctx, `
			UPDATE setting SET last_value = ?, last_fetched_at = ?, last_error = NULL
			WHERE user_id = ? AND key = ?`, *value, ts(at), userID, key)
		if err != nil {
			return fmt.Errorf("store: recording fetch for %q: %w", key, err)
		}
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE setting SET last_error = ? WHERE user_id = ? AND key = ?`,
		nullString(errMessage), userID, key)
	if err != nil {
		return fmt.Errorf("store: recording fetch error for %q: %w", key, err)
	}
	return nil
}

// DueForRefresh returns every auto setting whose interval has elapsed, across all
// users. The scheduler is not acting for a user, so this is the one query in the
// package that deliberately spans them.
func (r *SettingRepo) DueForRefresh(ctx context.Context, now time.Time) ([]setting.Setting, error) {
	// unscoped-query-ok: the scheduler sweeps every user's auto settings; there is
	// no session to scope it to, and the caller refreshes each user's own rows.
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, key, mode, manual_value, provider_key,
		       refresh_interval_seconds, last_value, last_fetched_at, last_error, updated_at
		FROM setting
		WHERE mode = 'auto'
		  AND (last_fetched_at IS NULL
		       OR last_fetched_at <= ?)
		ORDER BY user_id, key`,
		ts(now.Add(-setting.DefaultRefresh)))
	if err != nil {
		return nil, fmt.Errorf("store: listing settings due for refresh: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []setting.Setting{}
	for rows.Next() {
		s, err := scanSetting(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning setting: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Audit appends to the audit log.
func (r *SettingRepo) Audit(ctx context.Context, userID int64, entry setting.AuditEntry, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, actor_user_id, entity, entity_id, action, before_json, after_json, at)
		VALUES (?,?,?,?,?,?,?,?)`,
		userID, nullInt64(entry.ActorUserID), entry.Entity, nullInt64(entry.EntityID),
		entry.Action, nullText(entry.Before), nullText(entry.After), ts(now))
	if err != nil {
		return fmt.Errorf("store: writing audit entry: %w", err)
	}
	return nil
}

// ListProviders returns the configured providers, highest priority first. The
// table is global rather than per-user, so it carries no user_id.
func (r *SettingRepo) ListProviders(ctx context.Context) ([]setting.Provider, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT key, kind, endpoint, priority, enabled FROM provider ORDER BY kind, priority`)
	if err != nil {
		return nil, fmt.Errorf("store: listing providers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []setting.Provider{}
	for rows.Next() {
		var (
			p       setting.Provider
			enabled int
		)
		if err := rows.Scan(&p.Key, &p.Kind, &p.Endpoint, &p.Priority, &enabled); err != nil {
			return nil, fmt.Errorf("store: scanning provider: %w", err)
		}
		p.Enabled = enabled == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}
