// Package store holds every repository and all SQL. Nothing above it writes SQL,
// and every statement against a user-owned table filters on user_id as its first
// predicate — enforced by TestNoUnscopedQueries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	// Pure-Go SQLite driver: CGO_ENABLED=0, which is what keeps arm64 a
	// cross-compile rather than an emulated build (docs/10-deployment-ci.md §10.4).
	_ "modernc.org/sqlite"

	"github.com/antlko/moneyapp/internal/config"
)

// DriverName is the registered database/sql driver.
const DriverName = "sqlite"

// Open returns a connection pool with the pragmas this application requires.
//
// foreign_keys is off by default in SQLite and must be enabled per connection,
// so it goes in the DSN rather than being executed once.
func Open(cfg config.DatabaseConfig) (*sql.DB, error) {
	dsn := DSN(cfg)
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", cfg.Path, err)
	}
	// SQLite serialises writes; a large pool only adds lock contention.
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: pinging %s: %w", cfg.Path, err)
	}
	return db, nil
}

// DSN builds the driver connection string from configuration.
func DSN(cfg config.DatabaseConfig) string {
	busy := cfg.BusyTimeoutMS
	if busy <= 0 {
		busy = 5000
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busy))
	// NORMAL is the documented companion to WAL: durable across process crashes,
	// and a power loss can cost only the last transaction.
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_txlock", "immediate")
	return "file:" + cfg.Path + "?" + q.Encode()
}

// OpenTest returns an isolated database in a temp file, migrated to head.
// Repository tests use real SQLite rather than a mock, because the constraints
// in the DDL are part of what is being tested (conventions §9).
func OpenTest(tb testingTB) *sql.DB {
	tb.Helper()
	path := tb.TempDir() + "/moneyapp-test.db"
	db, err := Open(config.DatabaseConfig{Path: path, MaxOpenConns: 4, MaxIdleConns: 2, BusyTimeoutMS: 5000})
	if err != nil {
		tb.Fatalf("store.OpenTest: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	if err := MigrateUp(context.Background(), db); err != nil {
		tb.Fatalf("store.OpenTest: migrate: %v", err)
	}
	return db
}

// testingTB is the slice of *testing.T that OpenTest needs, declared locally so
// the production build does not import the testing package.
type testingTB interface {
	Helper()
	TempDir() string
	Fatalf(format string, args ...any)
	Cleanup(func())
}
