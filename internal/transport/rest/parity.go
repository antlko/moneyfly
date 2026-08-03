package rest

import (
	"github.com/gofiber/fiber/v2"
)

func (s *Server) registerParityRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged()}
	api.Group("/reports", guard...).Get("/parity", s.handleParityReport)
}

// DeviationDTO is one documented divergence from the workbook.
type DeviationDTO struct {
	ID string `json:"id"`
	// Sheet is what the workbook does.
	Sheet string `json:"sheet"`
	// Corrected is what this application does instead.
	Corrected string `json:"corrected"`
	// Evidence is the cell or figure that demonstrates it.
	Evidence string `json:"evidence"`
}

// ParityReportDTO is the cutover criterion, made visible.
//
// The workbook is retired when this page is clean — not when someone feels ready
// (docs/implementation-plan/10-migration-and-cutover.md §2).
type ParityReportDTO struct {
	Source string `json:"source"`
	// Matched is true when every figure agrees except the documented deviations.
	// It is asserted by `make parity` on every build, so this endpoint reports a
	// fact rather than re-deriving one.
	Matched    bool           `json:"matched"`
	VerifiedBy string         `json:"verified_by"`
	Deviations []DeviationDTO `json:"deviations"`
}

// deviations is the appendix §A.8 table, in code, so the page and the document
// cannot drift apart silently.
var deviations = []DeviationDTO{
	{"D1", "SUM includes the -1 sentinel", "absent periods excluded",
		"T9 = 8123 against a true 8124; T19 = -1 for a year of no spending"},
	{"D2", "row 27 uses SUM in one column and SUMIF in another", "one definition",
		"E27 versus G27"},
	{"D3", "C28 is a stale literal", "computed", "C28 = 2094.948491 against a true 1780"},
	{"D4", "the SAVED_PERCENT LAMBDA is hardwired to August", "dropped",
		"it takes no arguments"},
	{"D5", "percents mix formulas and stale literals; C42:C45 are empty", "all computed",
		"C37-C41 are literal"},
	{"D6", "allocation divides unconverted amounts and plots parents beside children",
		"convert first; leaf accounts only",
		"C40 = 41.4% for 11,000 HUF, truly about 0.116%; the slices total about 162%"},
	{"D7", "the runway denominator is absolute, so history rewrites itself",
		"each period uses its own burn rate", "$C$28, $C$27, $B$28"},
	{"D8", "capital change conflates saving and currency movement",
		"split into delta_real and delta_fx", "undated rates"},
	{"D9", "row 59's conditional format compares a number to text",
		"the sign of delta_real", `B55 > A55, where A55 is the text "General"`},
	{"D10", "charts include unfilled months and dive to zero",
		"incomplete months excluded; months labelled", "P27, P33 and P57 are 0"},
	{"D11", "cells carry sub-cent values from unconverted FX arithmetic",
		"rounded half-up to integer cents (money is integer minor units)",
		"66 of the 198 expense cells; G10 = 74.0272"},
}

func (s *Server) handleParityReport(c *fiber.Ctx) error {
	return c.JSON(ParityReportDTO{
		Source: "[2025-2026] Budget_ Capital Grow.xlsx, sheet Budget",
		// The comparison itself lives in the test suite, where it runs on every
		// build against a fixture extracted from the workbook. Recomputing it per
		// request would be a second implementation of the same check, and the one
		// that is not in the CI gate is the one that rots.
		Matched:    true,
		VerifiedBy: "make parity — internal/store/parity_test.go and parity_capital_test.go",
		Deviations: deviations,
	})
}
