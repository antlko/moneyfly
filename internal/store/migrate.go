package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/pressly/goose/v3"

	"github.com/antlko/moneyapp/migrations"
)

// migrationsDir is the root inside the embedded FS.
const migrationsDir = "."

var gooseOnce sync.Once

func configureGoose() {
	gooseOnce.Do(func() {
		goose.SetBaseFS(migrations.FS)
		goose.SetTableName("goose_db_version")
		if err := goose.SetDialect("sqlite3"); err != nil {
			panic("store: goose dialect: " + err.Error())
		}
		goose.SetLogger(goose.NopLogger())
	})
}

// SetMigrationLogger sends goose's own output somewhere visible. The CLI points
// it at stdout; tests and the server leave it silent.
func SetMigrationLogger(w io.Writer) {
	configureGoose()
	goose.SetLogger(&writerLogger{w: w})
}

// MigrateUp applies every pending migration.
func MigrateUp(ctx context.Context, db *sql.DB) error {
	configureGoose()
	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, db *sql.DB) error {
	configureGoose()
	if err := goose.DownContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("store: migrate down: %w", err)
	}
	return nil
}

// MigrateDownAll rolls back to an empty schema. CI uses it for the up/down/up
// cycle; it is deliberately not reachable from `migrate down`.
func MigrateDownAll(ctx context.Context, db *sql.DB) error {
	configureGoose()
	if err := goose.DownToContext(ctx, db, migrationsDir, 0); err != nil {
		return fmt.Errorf("store: migrate down-all: %w", err)
	}
	return nil
}

// MigrateStatus prints the applied state of every migration.
func MigrateStatus(ctx context.Context, db *sql.DB) error {
	configureGoose()
	if err := goose.StatusContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("store: migrate status: %w", err)
	}
	return nil
}

// SchemaVersion is the migration version recorded in the database. It is 0 for a
// database that has never been migrated.
func SchemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	configureGoose()
	// goose creates its version table on demand; a fresh database has none, and
	// that is "version 0" rather than an error.
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	v, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}

// ExpectedSchemaVersion is the highest migration this binary carries. /readyz
// compares it against the database and fails closed when the database is behind
// (docs/adr/0011-explicit-migrations.md).
func ExpectedSchemaVersion() int64 {
	entries, err := fs.ReadDir(migrations.FS, migrationsDir)
	if err != nil {
		return 0
	}
	var max int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		name := path.Base(e.Name())
		numeric := name[:strings.IndexByte(name, '_')]
		n, err := strconv.ParseInt(numeric, 10, 64)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max
}

// Readiness answers /readyz without letting the transport layer query the
// database itself.
type Readiness struct{ db *sql.DB }

// NewReadiness builds the readiness checker.
func NewReadiness(db *sql.DB) *Readiness { return &Readiness{db: db} }

// Ready returns the database's schema version and the one this binary expects.
// An unreachable database is an error, which /readyz turns into a 503.
func (r *Readiness) Ready(ctx context.Context) (current, expected int64, err error) {
	expected = ExpectedSchemaVersion()
	if r.db == nil {
		return 0, expected, fmt.Errorf("store: no database configured")
	}
	if err := r.db.PingContext(ctx); err != nil {
		return 0, expected, fmt.Errorf("store: database unreachable: %w", err)
	}
	current, err = SchemaVersion(ctx, r.db)
	if err != nil {
		return 0, expected, err
	}
	return current, expected, nil
}

type writerLogger struct{ w io.Writer }

// Fatalf implements goose's logger. A failed write to the migration log is not
// worth failing a migration over, so the error is deliberately dropped.
func (l *writerLogger) Fatalf(format string, v ...any) { _, _ = fmt.Fprintf(l.w, format, v...) }

// Printf implements goose's logger.
func (l *writerLogger) Printf(format string, v ...any) { _, _ = fmt.Fprintf(l.w, format, v...) }
