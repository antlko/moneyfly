// Package metrics is the spreadsheet as code: every formula from
// docs/07-metrics-and-budgets.md as a pure function over loaded data, which is
// what makes the Excel parity tests possible.
//
// Stage 05 fills it in. The single-period budget report lives in
// internal/domain/budget until then, because stage 03 needs only that.
package metrics
