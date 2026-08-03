package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// TelegramRepo stores telegram_link and the poll offset. It implements
// telegram.Repo and telegram.OffsetStore.
type TelegramRepo struct{ db *sql.DB }

// NewTelegramRepo builds the repository.
func NewTelegramRepo(db *sql.DB) *TelegramRepo { return &TelegramRepo{db: db} }

// linkSelect is a projection fragment; every call site appends its own WHERE.
// Two of them look a link up by code or by chat id rather than by user, because
// that is the direction the bot resolves in — a chat arrives before an account
// is known.
//
// unscoped-query-ok: resolved by code or chat id, which is what identifies the user
const linkSelect = `
	SELECT id, user_id, chat_id, link_code, expires_at, linked_at, revoked_at, created_at
	FROM telegram_link`

func scanLink(row interface{ Scan(...any) error }) (telegram.Link, error) {
	var (
		l         telegram.Link
		chatID    sql.NullInt64
		expiresAt string
		linkedAt  sql.NullString
		revokedAt sql.NullString
		createdAt string
	)
	if err := row.Scan(&l.ID, &l.UserID, &chatID, &l.Code, &expiresAt,
		&linkedAt, &revokedAt, &createdAt); err != nil {
		return telegram.Link{}, err
	}
	l.ChatID = scanNullInt64(chatID)
	var err error
	if l.ExpiresAt, err = parseTS(expiresAt); err != nil {
		return telegram.Link{}, err
	}
	if l.LinkedAt, err = scanNullTime(linkedAt); err != nil {
		return telegram.Link{}, err
	}
	if l.RevokedAt, err = scanNullTime(revokedAt); err != nil {
		return telegram.Link{}, err
	}
	if l.CreatedAt, err = parseTS(createdAt); err != nil {
		return telegram.Link{}, err
	}
	return l, nil
}

// Create mints a code.
func (r *TelegramRepo) Create(ctx context.Context, l telegram.Link, now time.Time) (telegram.Link, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_link (user_id, link_code, expires_at, created_at)
		VALUES (?,?,?,?)`,
		l.UserID, l.Code, ts(l.ExpiresAt), ts(now))
	if err != nil {
		return telegram.Link{}, fmt.Errorf("store: creating telegram link: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return telegram.Link{}, fmt.Errorf("store: creating telegram link: %w", err)
	}
	return r.byID(ctx, id)
}

func (r *TelegramRepo) byID(ctx context.Context, id int64) (telegram.Link, error) {
	// unscoped-query-ok: reading back a row this repository just wrote by id
	l, err := scanLink(r.db.QueryRowContext(ctx, linkSelect+` WHERE id = ?`, id))
	if err != nil {
		return telegram.Link{}, fmt.Errorf("store: reading telegram link %d: %w", id, err)
	}
	return l, nil
}

// ByCode finds a link by its code.
func (r *TelegramRepo) ByCode(ctx context.Context, code string) (telegram.Link, error) {
	// unscoped-query-ok: the code is the identifier; it is what names the user
	l, err := scanLink(r.db.QueryRowContext(ctx, linkSelect+` WHERE link_code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return telegram.Link{}, apperr.NotFoundf("link code")
	}
	if err != nil {
		return telegram.Link{}, fmt.Errorf("store: reading telegram link by code: %w", err)
	}
	return l, nil
}

// LiveByChat resolves a chat to its account.
func (r *TelegramRepo) LiveByChat(ctx context.Context, chatID int64) (telegram.Link, error) {
	// unscoped-query-ok: the chat id is what resolves to a user in the first place
	l, err := scanLink(r.db.QueryRowContext(ctx,
		linkSelect+` WHERE chat_id = ? AND linked_at IS NOT NULL AND revoked_at IS NULL`, chatID))
	if errors.Is(err, sql.ErrNoRows) {
		return telegram.Link{}, apperr.NotFoundf("no live link for chat %d", chatID)
	}
	if err != nil {
		return telegram.Link{}, fmt.Errorf("store: resolving telegram chat: %w", err)
	}
	return l, nil
}

// ListByUser returns a user's links, newest first.
func (r *TelegramRepo) ListByUser(ctx context.Context, userID int64) ([]telegram.Link, error) {
	rows, err := r.db.QueryContext(ctx,
		linkSelect+` WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing telegram links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []telegram.Link{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning telegram link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Bind attaches a chat to a code.
func (r *TelegramRepo) Bind(ctx context.Context, id, chatID int64, now time.Time) (telegram.Link, error) {
	// unscoped-query-ok: the id came from ByCode, which is the user's own code
	res, err := r.db.ExecContext(ctx, `
		UPDATE telegram_link SET chat_id = ?, linked_at = ?
		WHERE id = ? AND linked_at IS NULL`,
		chatID, ts(now), id)
	if err != nil {
		if isUniqueViolation(err) {
			return telegram.Link{}, apperr.Conflictf("that chat is already linked")
		}
		return telegram.Link{}, fmt.Errorf("store: binding telegram link: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return telegram.Link{}, apperr.Conflictf("that code has already been used")
	}
	return r.byID(ctx, id)
}

// Revoke ends a link from the web UI.
func (r *TelegramRepo) Revoke(ctx context.Context, userID, id int64, now time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE telegram_link SET revoked_at = ?
		WHERE user_id = ? AND id = ? AND revoked_at IS NULL`,
		ts(now), userID, id)
	if err != nil {
		return fmt.Errorf("store: revoking telegram link: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("telegram link %d", id)
	}
	return nil
}

// RevokeByChat ends every live link for a chat, for /unlink and for re-linking.
func (r *TelegramRepo) RevokeByChat(ctx context.Context, chatID int64, now time.Time) (int, error) {
	// unscoped-query-ok: a chat revoking its own link knows no user id
	res, err := r.db.ExecContext(ctx, `
		UPDATE telegram_link SET revoked_at = ?
		WHERE chat_id = ? AND linked_at IS NOT NULL AND revoked_at IS NULL`,
		ts(now), chatID)
	if err != nil {
		return 0, fmt.Errorf("store: revoking telegram links for chat: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// offsetKey is where the poll offset lives.
const offsetKey = "telegram.poll_offset"

// Offset reads the last acknowledged update id.
func (r *TelegramRepo) Offset(ctx context.Context) (int64, error) {
	var value string
	// unscoped-query-ok: process state, not user data
	err := r.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, offsetKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: reading the telegram offset: %w", err)
	}
	offset, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		// A corrupt offset would replay the whole backlog. Starting from zero is
		// what Telegram itself does, and the importer's dedup makes it harmless.
		return 0, nil
	}
	return offset, nil
}

// SetOffset records the last acknowledged update id.
func (r *TelegramRepo) SetOffset(ctx context.Context, offset int64, now time.Time) error {
	// unscoped-query-ok: process state, not user data
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO kv (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		offsetKey, strconv.FormatInt(offset, 10), ts(now))
	if err != nil {
		return fmt.Errorf("store: writing the telegram offset: %w", err)
	}
	return nil
}
