package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// uploadField is the multipart field the UI and the bot both use.
const uploadField = "file"

func (s *Server) registerImportRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged(), s.requireWritable()}

	g := api.Group("/imports", guard...)
	g.Get("/", s.handleListImports)
	g.Post("/", s.handleCreateImport)
	g.Get("/:id", s.handleGetImport)
	g.Get("/:id/rows", s.handleListImportRows)
	g.Post("/:id/mappings", s.handleApplyImportMappings)
	g.Post("/:id/commit", s.handleCommitImport)
	g.Post("/:id/revert", s.handleRevertImport)
}

// BatchDTO is an import batch on the wire.
type BatchDTO struct {
	ID            int64   `json:"id"`
	Source        string  `json:"source"`
	Origin        string  `json:"origin"`
	Filename      string  `json:"filename"`
	FileSHA256    string  `json:"file_sha256"`
	Status        string  `json:"status"`
	RowsTotal     int     `json:"rows_total"`
	RowsNew       int     `json:"rows_new"`
	RowsDuplicate int     `json:"rows_duplicate"`
	RowsUnmapped  int     `json:"rows_unmapped"`
	RowsRejected  int     `json:"rows_rejected"`
	Error         string  `json:"error,omitempty"`
	CreatedAt     string  `json:"created_at"`
	CommittedAt   *string `json:"committed_at"`
	RevertedAt    *string `json:"reverted_at"`
	// AlreadyImported marks a re-upload of a file that was committed before. The
	// batch reported is the original one; nothing was reprocessed.
	AlreadyImported bool `json:"already_imported,omitempty"`
}

func toBatchDTO(b importer.Batch) BatchDTO {
	out := BatchDTO{
		ID: b.ID, Source: b.Source, Origin: string(b.Origin), Filename: b.Filename,
		FileSHA256: b.FileSHA256, Status: string(b.Status),
		RowsTotal: b.RowsTotal, RowsNew: b.RowsNew, RowsDuplicate: b.RowsDuplicate,
		RowsUnmapped: b.RowsUnmapped, RowsRejected: b.RowsRejected, Error: b.Error,
		CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339), AlreadyImported: b.AlreadyImported,
	}
	if b.CommittedAt != nil {
		v := b.CommittedAt.UTC().Format(time.RFC3339)
		out.CommittedAt = &v
	}
	if b.RevertedAt != nil {
		v := b.RevertedAt.UTC().Format(time.RFC3339)
		out.RevertedAt = &v
	}
	return out
}

// DateRangeDTO is the span a file covers.
type DateRangeDTO struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// SuggestionDTO is a proposed mapping. It is never applied without confirmation.
type SuggestionDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Confidence string `json:"confidence"`
}

// UnmappedNameDTO is one source name blocking the batch.
type UnmappedNameDTO struct {
	SourceName string         `json:"source_name"`
	RowCount   int            `json:"row_count"`
	Reason     string         `json:"reason,omitempty"`
	Suggestion *SuggestionDTO `json:"suggestion"`
}

// VanishedRowDTO is a stored row the file no longer contains. It is reported,
// never deleted.
type VanishedRowDTO struct {
	TransactionID int64    `json:"transaction_id"`
	OccurredOn    string   `json:"occurred_on"`
	AccountName   string   `json:"account_name"`
	CategoryName  string   `json:"category_name"`
	Amount        MoneyDTO `json:"amount"`
	Description   string   `json:"description,omitempty"`
}

// ImportRowDTO is one line of an upload.
type ImportRowDTO struct {
	ID            int64     `json:"id"`
	LineNo        int       `json:"line_no"`
	Raw           string    `json:"raw_line"`
	OccurredOn    *string   `json:"occurred_on"`
	AccountName   string    `json:"account_name,omitempty"`
	CategoryName  string    `json:"category_name,omitempty"`
	Amount        *MoneyDTO `json:"amount"`
	Description   *string   `json:"description"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	TransactionID *int64    `json:"transaction_id"`
}

// PreviewDTO is the payload from docs/04-import-monefy.md §4.11: everything a
// human needs before deciding to commit.
type PreviewDTO struct {
	BatchID       int64         `json:"batch_id"`
	Filename      string        `json:"filename"`
	Status        string        `json:"status"`
	Origin        string        `json:"origin"`
	DateRange     *DateRangeDTO `json:"date_range"`
	RowsTotal     int           `json:"rows_total"`
	RowsNew       int           `json:"rows_new"`
	RowsDuplicate int           `json:"rows_duplicate"`
	RowsUnmapped  int           `json:"rows_unmapped"`
	RowsRejected  int           `json:"rows_rejected"`
	MonthsTouched int           `json:"months_touched"`

	UnmappedCategories []UnmappedNameDTO `json:"unmapped_categories"`
	UnmappedAccounts   []UnmappedNameDTO `json:"unmapped_accounts"`
	Vanished           []VanishedRowDTO  `json:"vanished"`
	Rejected           []ImportRowDTO    `json:"rejected"`
	ByCurrency         map[string]int    `json:"by_currency"`
	Warnings           []string          `json:"warnings"`
	CommittedAt        *string           `json:"committed_at"`
	RevertedAt         *string           `json:"reverted_at"`
}

func toPreviewDTO(p importer.Preview, exponents map[string]int) PreviewDTO {
	batch := toBatchDTO(p.Batch)
	out := PreviewDTO{
		BatchID: p.Batch.ID, Filename: p.Batch.Filename, Status: string(p.Batch.Status),
		Origin: string(p.Batch.Origin),
		// The counts come from the analysis just performed, so the preview can
		// never disagree with the batch row it was derived from.
		RowsTotal: p.Batch.RowsTotal, RowsNew: p.Batch.RowsNew,
		RowsDuplicate: p.Batch.RowsDuplicate, RowsUnmapped: p.Batch.RowsUnmapped,
		RowsRejected: p.Batch.RowsRejected, MonthsTouched: p.MonthsTouched,
		UnmappedCategories: toUnmappedDTOs(p.UnmappedCategories),
		UnmappedAccounts:   toUnmappedDTOs(p.UnmappedAccounts),
		Vanished:           []VanishedRowDTO{},
		Rejected:           []ImportRowDTO{},
		ByCurrency:         p.ByCurrency,
		Warnings:           p.Warnings,
		CommittedAt:        batch.CommittedAt,
		RevertedAt:         batch.RevertedAt,
	}
	if out.ByCurrency == nil {
		out.ByCurrency = map[string]int{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if p.DateRange != nil {
		out.DateRange = &DateRangeDTO{
			From: p.DateRange.From.UTC().Format(dateLayout),
			To:   p.DateRange.To.UTC().Format(dateLayout),
		}
	}
	for _, v := range p.Vanished {
		out.Vanished = append(out.Vanished, VanishedRowDTO{
			TransactionID: v.TransactionID,
			OccurredOn:    v.OccurredOn.UTC().Format(dateLayout),
			AccountName:   v.AccountName, CategoryName: v.CategoryName,
			Amount:      MoneyDTO{AmountMinor: v.AmountMinor, Currency: v.Currency, Exponent: exponents[v.Currency]},
			Description: v.Description,
		})
	}
	for _, r := range p.Rejected {
		out.Rejected = append(out.Rejected, toImportRowDTO(r, exponents))
	}
	return out
}

func toUnmappedDTOs(names []importer.UnmappedName) []UnmappedNameDTO {
	out := []UnmappedNameDTO{}
	for _, n := range names {
		item := UnmappedNameDTO{SourceName: n.SourceName, RowCount: n.RowCount, Reason: n.Reason}
		if n.Suggestion != nil {
			item.Suggestion = &SuggestionDTO{
				ID: n.Suggestion.ID, Name: n.Suggestion.Name, Confidence: n.Suggestion.Confidence,
			}
		}
		out = append(out, item)
	}
	return out
}

func toImportRowDTO(r importer.Row, exponents map[string]int) ImportRowDTO {
	out := ImportRowDTO{
		ID: r.ID, LineNo: r.LineNo, Raw: r.Raw, AccountName: r.AccountName,
		CategoryName: r.CategoryName, Description: r.Description,
		Status: string(r.Status), Reason: r.Reason, TransactionID: r.TransactionID,
	}
	if r.Date != nil {
		v := r.Date.UTC().Format(dateLayout)
		out.OccurredOn = &v
	}
	if r.AmountMinor != nil {
		out.Amount = &MoneyDTO{
			AmountMinor: *r.AmountMinor, Currency: r.Currency, Exponent: exponents[r.Currency],
		}
	}
	return out
}

// MappingDTO binds one source name to one target id.
type MappingDTO struct {
	SourceName string `json:"source_name"`
	TargetID   int64  `json:"target_id"`
}

// MappingsInputDTO is the mapping screen's decision set.
type MappingsInputDTO struct {
	Categories []MappingDTO `json:"categories"`
	Accounts   []MappingDTO `json:"accounts"`
}

func (s *Server) handleCreateImport(c *fiber.Ctx) error {
	u := currentUser(c)
	if s.deps.Imports == nil {
		return apperr.NotFoundf("import is not configured")
	}

	header, err := c.FormFile(uploadField)
	if err != nil {
		return apperr.Validation(uploadField, "a multipart file field named %q is required", uploadField)
	}
	file, err := header.Open()
	if err != nil {
		return apperr.Validation(uploadField, "the uploaded file could not be read")
	}
	defer func() { _ = file.Close() }()

	batch, err := s.deps.Imports.Receive(c.UserContext(), u.ID, importer.OriginWeb, header.Filename, file)
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if batch.AlreadyImported {
		// Not created: this is the batch that already exists.
		status = http.StatusOK
	}
	return c.Status(status).JSON(toBatchDTO(*batch))
}

func (s *Server) handleListImports(c *fiber.Ctx) error {
	u := currentUser(c)
	batches, err := s.deps.Imports.List(c.UserContext(), u.ID, c.QueryInt("limit", 50))
	if err != nil {
		return err
	}
	out := make([]BatchDTO, 0, len(batches))
	for _, b := range batches {
		out = append(out, toBatchDTO(b))
	}
	return c.JSON(out)
}

func (s *Server) handleGetImport(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	preview, err := s.deps.Imports.Preview(c.UserContext(), u.ID, id)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(toPreviewDTO(*preview, exponents))
}

func (s *Server) handleListImportRows(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	rows, err := s.deps.Imports.Rows(c.UserContext(), u.ID, id,
		importer.RowStatus(strings.TrimSpace(c.Query("status"))), c.QueryInt("limit", 500))
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]ImportRowDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toImportRowDTO(r, exponents))
	}
	return c.JSON(out)
}

func (s *Server) handleApplyImportMappings(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	var body MappingsInputDTO
	if err := bind(c, &body); err != nil {
		return err
	}
	m := importer.Mappings{}
	for _, item := range body.Categories {
		m.Categories = append(m.Categories, importer.Mapping{SourceName: item.SourceName, TargetID: item.TargetID})
	}
	for _, item := range body.Accounts {
		m.Accounts = append(m.Accounts, importer.Mapping{SourceName: item.SourceName, TargetID: item.TargetID})
	}

	preview, err := s.deps.Imports.ApplyMappings(c.UserContext(), u.ID, id, m)
	if err != nil {
		return err
	}
	exponents, err := s.exponents(c.UserContext())
	if err != nil {
		return err
	}
	return c.JSON(toPreviewDTO(*preview, exponents))
}

func (s *Server) handleCommitImport(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	batch, err := s.deps.Imports.Commit(c.UserContext(), u.ID, id)
	if err != nil {
		return err
	}
	return c.JSON(toBatchDTO(*batch))
}

func (s *Server) handleRevertImport(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	batch, err := s.deps.Imports.Revert(c.UserContext(), u.ID, id)
	if err != nil {
		return err
	}
	return c.JSON(toBatchDTO(*batch))
}
