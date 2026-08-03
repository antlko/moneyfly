package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/antlko/moneyapp/internal/config"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(config.DatabaseConfig{
		Path: filepath.Join(t.TempDir(), "moneyapp.db"), MaxOpenConns: 4, MaxIdleConns: 2, BusyTimeoutMS: 5000,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func schemaDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT type || ' ' || name || ' ' || COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name NOT LIKE 'goose_%'
		ORDER BY type, name`)
	if err != nil {
		t.Fatalf("schema dump: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := ""
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out += s + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestMigrations_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	if v, err := SchemaVersion(ctx, db); err != nil || v != 0 {
		t.Fatalf("a fresh database must report version 0, got %d (%v)", v, err)
	}
	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("up: %v", err)
	}
	before := schemaDump(t, db)
	if before == "" {
		t.Fatal("migrations produced no schema")
	}
	v, err := SchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != ExpectedSchemaVersion() {
		t.Fatalf("after up, version = %d, want %d", v, ExpectedSchemaVersion())
	}

	if err := MigrateDownAll(ctx, db); err != nil {
		t.Fatalf("down: %v", err)
	}
	if empty := schemaDump(t, db); empty != "" {
		t.Fatalf("down must leave an empty schema, got:\n%s", empty)
	}
	if v, err := SchemaVersion(ctx, db); err != nil || v != 0 {
		t.Fatalf("after down, version = %d (%v)", v, err)
	}

	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if after := schemaDump(t, db); after != before {
		t.Fatalf("schema differs after up/down/up:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestMigrations_ForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("up: %v", err)
	}
	// A session for a user that does not exist must be refused: foreign_keys is
	// off by default in SQLite and has to be enabled per connection.
	_, err := db.ExecContext(ctx,
		`INSERT INTO session (token_hash, user_id, created_at, expires_at) VALUES ('x', 999, '2026-07-30T00:00:00Z', '2026-08-30T00:00:00Z')`)
	if err == nil {
		t.Fatal("foreign keys are not being enforced")
	}
}

func TestMigrations_StrictTablesRejectWrongType(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("up: %v", err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO currency (code, exponent, name) VALUES ('XXX', 'two', 'Nonsense')`)
	if err == nil {
		t.Fatal("STRICT tables must reject a text value in an INTEGER column")
	}
}

func TestSeed_CurrencyExponents(t *testing.T) {
	ctx := context.Background()
	db := OpenTest(t)
	repo := NewCurrencyRepo(db)

	want := map[string]int{"EUR": 2, "USD": 2, "HUF": 0, "UAH": 2}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]int{}
	for _, c := range list {
		got[c.Code] = c.Exponent
	}
	for code, exp := range want {
		if got[code] != exp {
			t.Fatalf("%s exponent = %d, want %d", code, got[code], exp)
		}
	}
	if _, err := repo.Get(ctx, "ZZZ"); err == nil {
		t.Fatal("an unknown currency must not resolve")
	}
	if c, err := repo.Get(ctx, "huf"); err != nil || c.Exponent != 0 {
		t.Fatalf("lookup must be case-insensitive: %+v %v", c, err)
	}
}
