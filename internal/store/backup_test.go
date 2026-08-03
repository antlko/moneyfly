package store

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/config"
)

func TestBackup_WritesVerifiedArchive(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	_ = u

	dir := t.TempDir()
	backup := NewBackup(h.db, dir, 14)

	result, err := backup.Now(context.Background(), fixedNow)
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	if !strings.HasSuffix(result.Path, ".db.gz") {
		t.Fatalf("path = %q, want a gzipped archive", result.Path)
	}
	if result.Bytes == 0 {
		t.Fatal("the archive is empty")
	}

	archives, err := backup.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("archives = %d, want 1", len(archives))
	}
}

// TestBackup_IntegrityChecked is the point of the whole file: an archive nobody
// has opened is a hope, not a backup.
func TestBackup_IntegrityChecked(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	backup := NewBackup(h.db, dir, 14)

	if _, err := backup.Now(context.Background(), fixedNow); err != nil {
		t.Fatalf("the first backup: %v", err)
	}
	before, _ := backup.List()

	// A closed database cannot be vacuumed, which is the closest a test can get
	// to a write that fails. The previous archive must survive it.
	if err := h.db.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if _, err := backup.Now(context.Background(), fixedNow.Add(time.Second)); err == nil {
		t.Fatal("a backup of an unusable database must fail loudly")
	}

	after, _ := backup.List()
	if len(after) != len(before) {
		t.Fatalf("archives = %d, want the previous %d kept", len(after), len(before))
	}
}

func TestBackup_RetentionPrunes(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	backup := NewBackup(h.db, dir, 3)

	for i := 0; i < 5; i++ {
		if _, err := backup.Now(context.Background(), fixedNow.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}
	archives, err := backup.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(archives) != 3 {
		t.Fatalf("archives = %d, want the 3 most recent kept", len(archives))
	}
	// The ones kept are the newest: the names sort chronologically.
	first := filepath.Base(archives[0])
	if !strings.Contains(first, fixedNow.Add(2*time.Second).UTC().Format("20060102-150405")) {
		t.Fatalf("oldest kept = %q, want the third backup", first)
	}
}

// TestBackup_Restorable performs the drill the stage file insists on: an
// untested backup is not a backup.
func TestBackup_Restorable(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	acct := h.accountByName(u.ID, "Cash EUR")
	food := h.categoryByName(u.ID, "Food")
	if _, err := h.transactionService().Create(h.ctx, u.ID, "EUR",
		newExpense(acct.ID, food.ID, "2026-01-10", 12345, "EUR")); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	dir := t.TempDir()
	result, err := NewBackup(h.db, dir, 14).Now(context.Background(), fixedNow)
	if err != nil {
		t.Fatalf("Now: %v", err)
	}

	// Restore into a fresh file, exactly as the documented drill does.
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := gunzip(result.Path, restored); err != nil {
		t.Fatalf("restoring: %v", err)
	}

	db, err := Open(configFor(restored))
	if err != nil {
		t.Fatalf("opening the restored database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var (
		count int
		minor int64
	)
	if err := db.QueryRow(`
		SELECT count(*), COALESCE(SUM(amount_minor), 0) FROM transaction_entry
		WHERE user_id = ? AND deleted_at IS NULL`, u.ID).Scan(&count, &minor); err != nil {
		t.Fatalf("reading the restored data: %v", err)
	}
	if count != 1 || minor != 12345 {
		t.Fatalf("restored %d rows totalling %d, want 1 row of 12345", count, minor)
	}

	// And the schema is at head, so the restored file serves without migrating.
	version, err := SchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != ExpectedSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, ExpectedSchemaVersion())
	}
}

// gunzip is the `gunzip -c backup.db.gz > moneyapp.db` from the documented drill.
func gunzip(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, zr)
	return err
}

func configFor(path string) config.DatabaseConfig {
	return config.DatabaseConfig{Path: path, MaxOpenConns: 2, MaxIdleConns: 1, BusyTimeoutMS: 5000}
}
