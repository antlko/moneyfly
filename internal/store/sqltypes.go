package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// TimeLayout is the storage format for timestamps: ISO-8601, UTC, fixed width, so
// string comparison in SQL is chronological comparison.
const TimeLayout = "2006-01-02T15:04:05Z"

// DateLayout is the storage format for dates.
const DateLayout = "2006-01-02"

// ts renders a timestamp for storage.
func ts(t time.Time) string { return t.UTC().Format(TimeLayout) }

// date renders a date for storage.
func date(t time.Time) string { return t.UTC().Format(DateLayout) }

// parseTS reads a stored timestamp.
func parseTS(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(TimeLayout, s); err == nil {
		return t, nil
	}
	// Tolerate the fuller RFC 3339 form, which hand-written SQL or an older
	// migration may have produced.
	return time.Parse(time.RFC3339, s)
}

// parseDate reads a stored date.
func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(DateLayout, s)
}

func scanNullTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := parseTS(v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullString(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func scanNullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func scanNullInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueViolation reports whether err is a uniqueness conflict. The driver's
// error code is used rather than its message, so a wording change upstream cannot
// silently turn a 409 into a 500.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		switch serr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
