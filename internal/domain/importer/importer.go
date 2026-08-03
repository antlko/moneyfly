// Package importer is the Monefy CSV pipeline: parse, resolve aliases, dedup by
// natural key, reconcile against the file's date range, preview, commit, revert.
//
// The identity rule it depends on — transaction.NaturalKey — is frozen in
// internal/domain/transaction, so imported and manually entered rows share one
// definition (docs/adr/0008-natural-key-dedup.md).
//
// The guarantee this package exists to provide: an unrecognised category name
// blocks the batch. It is never auto-created, never coerced, never zero. Silent
// lookup failure is how 14% of the real dataset disappeared
// (docs/04-import-monefy.md §4.2).
package importer

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/antlko/moneyapp/internal/domain/transaction"
)

// Errors this package returns for a file it cannot use at all. Everything else
// is a per-row status, because one bad line must not cost the other 1,682.
var (
	// ErrEmptyFile is returned for an upload with no content.
	ErrEmptyFile = errors.New("importer: the file is empty")
	// ErrUnrecognisedFormat is returned when the header is not a Monefy export.
	ErrUnrecognisedFormat = errors.New("importer: unrecognised file format")
)

// Origin says where an upload arrived from. It matches the CHECK constraint on
// import_batch.origin.
type Origin string

// The three origins.
const (
	OriginWeb      Origin = "web"
	OriginTelegram Origin = "telegram"
	OriginCLI      Origin = "cli"
)

// Valid reports whether the origin is one of the three allowed values.
func (o Origin) Valid() bool {
	return o == OriginWeb || o == OriginTelegram || o == OriginCLI
}

// Status is the batch state (docs/04-import-monefy.md §4.10).
type Status string

// The batch states.
const (
	StatusReceived     Status = "received"
	StatusParsed       Status = "parsed"
	StatusNeedsMapping Status = "needs_mapping"
	StatusPreviewed    Status = "previewed"
	StatusCommitted    Status = "committed"
	StatusReverted     Status = "reverted"
	StatusFailed       Status = "failed"
)

// Terminal reports whether the batch has reached a state the analysis must not
// overwrite. Re-previewing a committed batch would otherwise re-derive its counts
// from a database that now contains its own rows — reporting 0 new, 1,683
// duplicate, and quietly resetting the status to previewed.
func (s Status) Terminal() bool {
	return s == StatusCommitted || s == StatusReverted || s == StatusFailed
}

// RowStatus is the per-row outcome.
type RowStatus string

// The row statuses, matching the CHECK constraint on import_row.status.
const (
	RowNew       RowStatus = "new"
	RowDuplicate RowStatus = "duplicate"
	RowUnmapped  RowStatus = "unmapped"
	RowRejected  RowStatus = "rejected"
	RowCommitted RowStatus = "committed"
)

// Valid reports whether the row status is one of the five allowed values.
func (r RowStatus) Valid() bool {
	switch r {
	case RowNew, RowDuplicate, RowUnmapped, RowRejected, RowCommitted:
		return true
	}
	return false
}

// RawRow is one parsed line, before any resolution.
type RawRow struct {
	LineNo       int
	Raw          string
	Date         time.Time
	AccountName  string
	CategoryName string
	AmountMinor  int64 // absolute value; the sign is carried by Kind
	Currency     string
	Kind         transaction.Kind
	Description  string // UNTRIMMED — feeds NaturalKey
	// ConvertedCurrency is Monefy's own base at export time. It is read only so
	// the preview can say which currency was discarded (§4.4).
	ConvertedCurrency string
}

// ParseError is one line that could not be read. It never aborts the file.
type ParseError struct {
	LineNo int
	Raw    string
	Reason string
}

// Source is one import format. Only the parser is format-specific; everything
// after it — aliases, dedup, reconcile, commit — is shared (§4.12).
type Source interface {
	Key() string
	Parse(r io.Reader) ([]RawRow, []ParseError, error)
}

// Batch is one upload.
type Batch struct {
	ID            int64
	UserID        int64
	Source        string
	Origin        Origin
	Filename      string
	FileSHA256    string
	StoredPath    string
	Status        Status
	RowsTotal     int
	RowsNew       int
	RowsDuplicate int
	RowsUnmapped  int
	RowsRejected  int
	Error         string
	CreatedAt     time.Time
	CommittedAt   *time.Time
	RevertedAt    *time.Time

	// AlreadyImported marks a re-upload of a file whose sha256 was committed
	// before. The batch returned is the original; nothing is reprocessed (§4.13).
	AlreadyImported bool
}

// Row is one line of an upload as stored in import_row.
type Row struct {
	ID            int64
	BatchID       int64
	LineNo        int
	Raw           string
	Date          *time.Time
	AccountName   string
	CategoryName  string
	AmountMinor   *int64
	Currency      string
	Description   *string
	Status        RowStatus
	Reason        string
	TransactionID *int64
}

// resolved is a row that survived parsing and alias resolution. It exists only
// between analysis and commit and is never stored: re-deriving it from the
// retained file keeps dedup correct at the moment of commit rather than at the
// moment of upload.
type resolved struct {
	LineNo     int
	AccountID  int64
	CategoryID int64
	Row        RawRow
	NaturalKey string
	Occurrence int
}

// UnmappedName is one source name the user must decide about. RowCount is what
// makes the cost of the decision visible: "HotelTrip — 58 rows".
type UnmappedName struct {
	SourceName string
	RowCount   int
	Reason     string
	Suggestion *Suggestion
}

// Suggestion is a proposal, never an assumption. Confirming it creates the
// alias; nothing is applied until then (§4.7).
type Suggestion struct {
	ID         int64
	Name       string
	Confidence string // "high" for a case/whitespace variant, "medium" for edit distance
}

// VanishedRow is a stored row inside the file's date range that the file does
// not contain. It is reported, never deleted: auto-deleting would make a parsing
// bug destructive (§4.6).
type VanishedRow struct {
	TransactionID int64
	OccurredOn    time.Time
	AccountName   string
	CategoryName  string
	AmountMinor   int64
	Currency      string
	Description   string
}

// DateRange is the span the file covers.
type DateRange struct {
	From time.Time
	To   time.Time
}

// Preview is everything the user needs before deciding to commit.
type Preview struct {
	Batch              Batch
	DateRange          *DateRange
	MonthsTouched      int
	ByCurrency         map[string]int
	UnmappedCategories []UnmappedName
	UnmappedAccounts   []UnmappedName
	Vanished           []VanishedRow
	Rejected           []Row
	Warnings           []string
}

// Mapping binds one source name to one target.
type Mapping struct {
	SourceName string
	TargetID   int64
}

// Mappings are the decisions taken on the mapping screen.
type Mappings struct {
	Categories []Mapping
	Accounts   []Mapping
}

// Service is the seam stage 08 plugs into: the bot calls Receive and relays the
// result, and gets no import logic of its own.
type Service interface {
	// Receive stores the file, parses, resolves and dedups. It never commits.
	Receive(ctx context.Context, userID int64, origin Origin, filename string, r io.Reader) (*Batch, error)
	Preview(ctx context.Context, userID, batchID int64) (*Preview, error)
	ApplyMappings(ctx context.Context, userID, batchID int64, m Mappings) (*Preview, error)
	// Commit returns apperr.ErrConflict when RowsUnmapped > 0 or state is not
	// previewed.
	Commit(ctx context.Context, userID, batchID int64) (*Batch, error)
	Revert(ctx context.Context, userID, batchID int64) (*Batch, error)
	List(ctx context.Context, userID int64, limit int) ([]Batch, error)
	Get(ctx context.Context, userID, batchID int64) (*Batch, error)
	Rows(ctx context.Context, userID, batchID int64, status RowStatus, limit int) ([]Row, error)
}

// StoredRow is a transaction already in the database, as the reconcile needs it.
type StoredRow struct {
	TransactionID int64
	NaturalKey    string
	Occurrence    int
	OccurredOn    time.Time
	AccountName   string
	CategoryName  string
	AmountMinor   int64
	Currency      string
	Description   string
}

// CommitEntry pairs a transaction to insert with the import row that produced it.
type CommitEntry struct {
	LineNo      int
	Transaction transaction.Transaction
}

// Counts are the batch tallies written alongside a commit.
type Counts struct {
	Total     int
	New       int
	Duplicate int
	Unmapped  int
	Rejected  int
}

// Repo is the storage contract.
type Repo interface {
	CreateBatch(ctx context.Context, b Batch, now time.Time) (Batch, error)
	GetBatch(ctx context.Context, userID, id int64) (Batch, error)
	ListBatches(ctx context.Context, userID int64, limit int) ([]Batch, error)
	UpdateBatch(ctx context.Context, b Batch) error
	// FindCommittedByChecksum finds a committed batch for the same file, so an
	// identical upload is reported rather than reprocessed.
	FindCommittedByChecksum(ctx context.Context, userID int64, source, sha256 string) (*Batch, error)

	ReplaceRows(ctx context.Context, userID, batchID int64, rows []Row) error
	ListRows(ctx context.Context, userID, batchID int64, status RowStatus, limit int) ([]Row, error)

	// MaxOccurrences returns the highest live occurrence stored per natural key.
	MaxOccurrences(ctx context.Context, userID int64, keys []string) (map[string]int, error)
	// ImportedInRange returns rows previously produced by an import of this
	// source within [from, to]. Manually entered rows are excluded: their
	// natural key is built from canonical names rather than source names, so
	// they would otherwise all look vanished.
	ImportedInRange(ctx context.Context, userID int64, source string, from, to time.Time) ([]StoredRow, error)

	// Commit inserts every entry, stamps import_batch_id, updates the counts and
	// sets committed_at — all in one SQL transaction, so a failure part-way
	// stores nothing.
	Commit(ctx context.Context, userID, batchID int64, entries []CommitEntry, counts Counts, now time.Time) (Batch, error)
	// Revert soft-deletes exactly the rows carrying this batch id.
	Revert(ctx context.Context, userID, batchID int64, now time.Time) (Batch, error)
}

// FileStore retains the raw upload so a batch can be reprocessed after an
// importer fix without re-exporting from the phone (§4.13).
type FileStore interface {
	Put(userID int64, sha256 string, data []byte) (path string, err error)
	Open(path string) (io.ReadCloser, error)
}
