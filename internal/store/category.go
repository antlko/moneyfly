package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// CategoryRepo stores categories and their aliases. It implements category.Repo.
type CategoryRepo struct{ db *sql.DB }

// NewCategoryRepo builds the repository.
func NewCategoryRepo(db *sql.DB) *CategoryRepo { return &CategoryRepo{db: db} }

const categoryColumns = `id, user_id, name, kind, is_essential,
	COALESCE(icon,''), COALESCE(color,''), sort_order, parent_id, archived_at`

func scanCategory(row interface{ Scan(...any) error }) (category.Category, error) {
	var (
		c         category.Category
		essential int
		parentID  sql.NullInt64
		archived  sql.NullString
	)
	if err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Kind, &essential,
		&c.Icon, &c.Color, &c.SortOrder, &parentID, &archived); err != nil {
		return category.Category{}, err
	}
	c.IsEssential = essential == 1
	c.ParentID = scanNullInt64(parentID)
	at, err := scanNullTime(archived)
	if err != nil {
		return category.Category{}, err
	}
	c.ArchivedAt = at
	return c, nil
}

// List returns the user's categories in sort order.
func (r *CategoryRepo) List(ctx context.Context, userID int64, kind category.Kind, includeArchived bool) ([]category.Category, error) {
	query := `SELECT ` + categoryColumns + ` FROM category WHERE user_id = ?`
	args := []any{userID}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, string(kind))
	}
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY sort_order, id`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing categories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []category.Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one category, archived or not — history must stay readable by id.
func (r *CategoryRepo) Get(ctx context.Context, userID, id int64) (category.Category, error) {
	c, err := scanCategory(r.db.QueryRowContext(ctx,
		`SELECT `+categoryColumns+` FROM category WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return category.Category{}, apperr.NotFoundf("category %d", id)
	}
	if err != nil {
		return category.Category{}, fmt.Errorf("store: reading category %d: %w", id, err)
	}
	return c, nil
}

// Create inserts a category. The unique index covers non-archived names only, so
// an archived name is reusable.
func (r *CategoryRepo) Create(ctx context.Context, c category.Category, now time.Time) (category.Category, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO category (user_id, name, kind, is_essential, icon, color, sort_order,
		                      parent_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.UserID, c.Name, string(c.Kind), boolToInt(c.IsEssential),
		nullString(&c.Icon), nullString(&c.Color), c.SortOrder, nullInt64(c.ParentID), ts(now), ts(now))
	if err != nil {
		if isUniqueViolation(err) {
			return category.Category{}, apperr.Conflictf("a category named %q already exists", c.Name)
		}
		return category.Category{}, fmt.Errorf("store: creating category: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return category.Category{}, fmt.Errorf("store: creating category: %w", err)
	}
	c.ID = id
	return c, nil
}

// Update writes a category.
func (r *CategoryRepo) Update(ctx context.Context, c category.Category, now time.Time) (category.Category, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE category
		SET name = ?, kind = ?, is_essential = ?, icon = ?, color = ?, sort_order = ?,
		    parent_id = ?, updated_at = ?
		WHERE user_id = ? AND id = ?`,
		c.Name, string(c.Kind), boolToInt(c.IsEssential), nullString(&c.Icon), nullString(&c.Color),
		c.SortOrder, nullInt64(c.ParentID), ts(now), c.UserID, c.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return category.Category{}, apperr.Conflictf("a category named %q already exists", c.Name)
		}
		return category.Category{}, fmt.Errorf("store: updating category %d: %w", c.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return category.Category{}, apperr.NotFoundf("category %d", c.ID)
	}
	return c, nil
}

// Archive sets archived_at. Nothing is ever hard-deleted, so historical rows keep
// a resolvable category.
func (r *CategoryRepo) Archive(ctx context.Context, userID, id int64, now time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE category SET archived_at = ?, updated_at = ? WHERE user_id = ? AND id = ? AND archived_at IS NULL`,
		ts(now), ts(now), userID, id)
	if err != nil {
		return fmt.Errorf("store: archiving category %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("active category %d", id)
	}
	return nil
}

// Merge moves every reference from id to intoID, leaves an alias behind so the
// old name still resolves on import, and archives the source — all in one
// transaction.
func (r *CategoryRepo) Merge(ctx context.Context, userID, id, intoID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: merge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sourceName string
	err = tx.QueryRowContext(ctx, `SELECT name FROM category WHERE user_id = ? AND id = ?`, userID, id).Scan(&sourceName)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFoundf("category %d", id)
	}
	if err != nil {
		return fmt.Errorf("store: merge: reading source: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE transaction_entry SET category_id = ?, updated_at = ? WHERE user_id = ? AND category_id = ?`,
		intoID, ts(now), userID, id); err != nil {
		return fmt.Errorf("store: merge: moving transactions: %w", err)
	}
	// A budget row for the same category and month already exists on the target
	// in some cases; the target's plan wins and the source's row is dropped,
	// because adding two plans together would silently invent a third figure.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM budget
		WHERE user_id = ? AND category_id = ?
		  AND period_month IN (SELECT period_month FROM budget WHERE user_id = ? AND category_id = ?)`,
		userID, id, userID, intoID); err != nil {
		return fmt.Errorf("store: merge: pruning budgets: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE budget SET category_id = ?, updated_at = ? WHERE user_id = ? AND category_id = ?`,
		intoID, ts(now), userID, id); err != nil {
		return fmt.Errorf("store: merge: moving budgets: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE category_alias SET category_id = ? WHERE user_id = ? AND category_id = ?`,
		intoID, userID, id); err != nil {
		return fmt.Errorf("store: merge: moving aliases: %w", err)
	}
	// The source's own name becomes an alias of the target.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO category_alias (user_id, category_id, source, source_name, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (user_id, source, source_name) DO UPDATE SET category_id = excluded.category_id`,
		userID, intoID, category.SourceMonefy, sourceName, ts(now)); err != nil {
		return fmt.Errorf("store: merge: leaving alias: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE category SET archived_at = ?, updated_at = ? WHERE user_id = ? AND id = ?`,
		ts(now), ts(now), userID, id); err != nil {
		return fmt.Errorf("store: merge: archiving source: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: merge: commit: %w", err)
	}
	return nil
}

// NextSortOrder returns one past the highest sort order in use.
func (r *CategoryRepo) NextSortOrder(ctx context.Context, userID int64) (int, error) {
	var n sql.NullInt64
	if err := r.db.QueryRowContext(ctx,
		`SELECT MAX(sort_order) FROM category WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: next category sort order: %w", err)
	}
	return int(n.Int64) + 1, nil
}

// ListAliases returns the mapping table with the target category's name.
func (r *CategoryRepo) ListAliases(ctx context.Context, userID int64) ([]category.Alias, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.user_id, a.category_id, a.source, a.source_name, c.name
		FROM category_alias a
		JOIN category c ON c.id = a.category_id
		WHERE a.user_id = ?
		ORDER BY c.name, a.source_name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing category aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []category.Alias{}
	for rows.Next() {
		var a category.Alias
		if err := rows.Scan(&a.ID, &a.UserID, &a.CategoryID, &a.Source, &a.SourceName, &a.TargetName); err != nil {
			return nil, fmt.Errorf("store: scanning category alias: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateAlias records a mapping, replacing any previous target for that name.
func (r *CategoryRepo) CreateAlias(ctx context.Context, a category.Alias, now time.Time) (category.Alias, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO category_alias (user_id, category_id, source, source_name, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (user_id, source, source_name) DO UPDATE SET category_id = excluded.category_id`,
		a.UserID, a.CategoryID, a.Source, a.SourceName, ts(now))
	if err != nil {
		return category.Alias{}, fmt.Errorf("store: creating category alias: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil {
		a.ID = id
	}
	return a, nil
}

// DeleteAlias removes a mapping.
func (r *CategoryRepo) DeleteAlias(ctx context.Context, userID, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM category_alias WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return fmt.Errorf("store: deleting category alias %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFoundf("category alias %d", id)
	}
	return nil
}

// FindAlias resolves a source name to its category, or (nil, nil) when unknown.
//
// The match is exact and case-sensitive: "Family " and "Family" are different
// source names, because raw text drives the natural key. Fuzzy matching lives in
// the importer and only ever proposes.
func (r *CategoryRepo) FindAlias(ctx context.Context, userID int64, source, sourceName string) (*category.Category, error) {
	c, err := scanCategory(r.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.kind, c.is_essential,
		       COALESCE(c.icon,''), COALESCE(c.color,''), c.sort_order, c.parent_id, c.archived_at
		FROM category_alias a
		JOIN category c ON c.id = a.category_id
		WHERE a.user_id = ? AND a.source = ? AND a.source_name = ?`, userID, source, sourceName))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: resolving category alias %q: %w", sourceName, err)
	}
	return &c, nil
}
