package store

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup takes consistent snapshots of the database.
//
// SQLite's VACUUM INTO writes a complete, self-contained copy while writers
// continue — the online-backup guarantee, without stopping the application. A
// file copy of a live WAL database is not a backup; it is a coin toss.
type Backup struct {
	db  *sql.DB
	dir string
	// keep is how many archives are retained. The oldest are pruned after a
	// successful write, never before: pruning first would trade a good backup for
	// a hoped-for one.
	keep int
}

// NewBackup builds the backup service.
func NewBackup(db *sql.DB, dir string, keep int) *Backup {
	if keep <= 0 {
		keep = 14
	}
	return &Backup{db: db, dir: dir, keep: keep}
}

// Result describes one backup.
type Result struct {
	Path    string
	Bytes   int64
	Took    time.Duration
	Pruned  []string
	Skipped bool
}

// backupPrefix names an archive so the sort order is chronological.
const backupPrefix = "moneyapp-"

// Now takes a backup, verifies it, then prunes.
//
// The verification is the point: an archive nobody has opened is a hope, not a
// backup. A copy that fails its integrity check is deleted and the previous one
// left in place, so a corrupt write can never displace a good archive.
func (b *Backup) Now(ctx context.Context, at time.Time) (Result, error) {
	if err := os.MkdirAll(b.dir, 0o750); err != nil {
		return Result{}, fmt.Errorf("store: creating %s: %w", b.dir, err)
	}
	started := time.Now()

	raw := filepath.Join(b.dir, fmt.Sprintf("%s%s.db", backupPrefix, at.UTC().Format("20060102-150405")))
	// VACUUM INTO refuses to overwrite, which is the behaviour we want: two
	// backups in the same second is a bug, not something to paper over.
	if _, err := b.db.ExecContext(ctx, `VACUUM INTO ?`, raw); err != nil {
		return Result{}, fmt.Errorf("store: writing the backup: %w", err)
	}

	if err := verify(ctx, raw); err != nil {
		// The archive is unusable. Remove it and keep what came before.
		_ = os.Remove(raw)
		return Result{}, fmt.Errorf("store: the backup failed its integrity check and was discarded: %w", err)
	}

	archive := raw + ".gz"
	if err := compress(raw, archive); err != nil {
		_ = os.Remove(raw)
		return Result{}, err
	}
	_ = os.Remove(raw)

	info, err := os.Stat(archive)
	if err != nil {
		return Result{}, fmt.Errorf("store: reading the backup back: %w", err)
	}

	pruned, err := b.prune()
	if err != nil {
		// The backup itself is good; failing to tidy is worth reporting, not
		// worth discarding it over.
		return Result{Path: archive, Bytes: info.Size(), Took: time.Since(started)}, err
	}
	return Result{
		Path: archive, Bytes: info.Size(), Took: time.Since(started), Pruned: pruned,
	}, nil
}

// verify opens the copy and runs SQLite's own integrity check over it.
func verify(ctx context.Context, path string) error {
	db, err := sql.Open(DriverName, "file:"+path+"?_pragma=query_only(1)")
	if err != nil {
		return fmt.Errorf("opening the copy: %w", err)
	}
	defer func() { _ = db.Close() }()

	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("running integrity_check: %w", err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("integrity_check said %q", result)
	}

	// And it has to actually contain the schema, not merely be well-formed: an
	// empty file passes integrity_check.
	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return fmt.Errorf("counting tables: %w", err)
	}
	if tables == 0 {
		return fmt.Errorf("the copy has no tables")
	}
	return nil
}

func compress(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a path this package just wrote
	if err != nil {
		return fmt.Errorf("store: reading the backup: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: creating the archive: %w", err)
	}
	defer func() { _ = out.Close() }()

	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		return fmt.Errorf("store: compressing the backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("store: finishing the archive: %w", err)
	}
	return out.Sync()
}

// prune removes the oldest archives beyond the retention count.
func (b *Backup) prune() ([]string, error) {
	archives, err := b.List()
	if err != nil {
		return nil, err
	}
	if len(archives) <= b.keep {
		return nil, nil
	}
	var pruned []string
	for _, path := range archives[:len(archives)-b.keep] {
		if err := os.Remove(path); err != nil {
			return pruned, fmt.Errorf("store: pruning %s: %w", path, err)
		}
		pruned = append(pruned, filepath.Base(path))
	}
	return pruned, nil
}

// List returns the archives oldest first. The timestamped names sort
// chronologically, so this needs no stat call per file.
func (b *Backup) List() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: listing backups: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), backupPrefix) || !strings.HasSuffix(e.Name(), ".db.gz") {
			continue
		}
		out = append(out, filepath.Join(b.dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
