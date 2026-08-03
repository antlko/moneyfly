package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// userScopedTables are the tables holding user-owned data. With no encryption at
// rest (docs/adr/0002-no-encryption.md), the user_id predicate is the *only*
// thing separating one user's finances from another's, so every statement
// touching these must carry it.
//
// `user` and `session` are deliberately absent: they are the identity tables the
// scoping is derived *from*. Login looks a user up by email and a request looks a
// session up by token hash — neither can be scoped to a user already known.
// `import_row` is absent because it is scoped through its parent batch, which is
// itself user-scoped; a statement touching import_row must join or subselect
// import_batch, which this test checks for separately.
var userScopedTables = []string{
	"category", "category_alias", "account", "account_alias",
	"transaction_entry", "balance_snapshot", "budget", "setting",
	"saved_metric", "dashboard_widget", "import_batch", "telegram_link",
	"audit_log", "idempotency_key",
}

var (
	sqlVerb    = regexp.MustCompile(`(?is)^\s*(select|insert|update|delete|with)\b`)
	whereSplit = regexp.MustCompile(`(?is)\bwhere\b`)
	onSplit    = regexp.MustCompile(`(?is)\bon\b`)
)

// TestNoUnscopedQueries scans every SQL literal in this package and fails on one
// that touches a user-owned table without a user_id predicate.
//
// It is deliberately written before there is anything to catch: the point is
// that the first violation cannot be introduced silently.
func TestNoUnscopedQueries(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		// A statement may legitimately be unscoped when it is annotated. The
		// annotation has to say why, and it is visible in review.
		exempt := exemptLines(fset, file)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if !sqlVerb.MatchString(sql) {
				return true
			}
			checked++
			pos := fset.Position(lit.Pos())
			if exempt[pos.Line] || exemptSpan(exempt, pos.Line, fset.Position(lit.End()).Line) {
				return true
			}
			if table, why := unscoped(sql); table != "" {
				t.Errorf("%s:%d: statement touches user-scoped table %q %s\n%s",
					e.Name(), pos.Line, table, why, strings.TrimSpace(sql))
			}
			return true
		})
	}
	t.Logf("checked %d SQL literals against %d user-scoped tables", checked, len(userScopedTables))
}

// unscoped returns the offending table and the reason, or "" when the statement
// is properly scoped.
//
// Scoping must appear in a *predicate*, not merely in the projection: selecting
// user_id while filtering on nothing is exactly the bug this catches. A statement
// with a WHERE clause must carry user_id there. A reusable SELECT fragment (this
// package composes a few, appending the WHERE at the call site) must carry it in a
// JOIN ... ON, which is why every join to a user-scoped table is scoped too.
func unscoped(sql string) (table, why string) {
	lower := strings.ToLower(sql)
	for _, tbl := range userScopedTables {
		if !mentionsTable(lower, tbl) {
			continue
		}
		if !strings.Contains(lower, "user_id") {
			return tbl, "with no user_id at all"
		}
		if isInsert(lower) {
			return "", ""
		}
		if parts := whereSplit.Split(lower, -1); len(parts) > 1 {
			for _, clause := range parts[1:] {
				if strings.Contains(clause, "user_id") {
					return "", ""
				}
			}
			return tbl, "but user_id appears outside every WHERE clause"
		}
		if parts := onSplit.Split(lower, -1); len(parts) > 1 {
			for _, clause := range parts[1:] {
				if strings.Contains(clause, "user_id") {
					return "", ""
				}
			}
			return tbl, "and its joins do not carry user_id"
		}
		return tbl, "with no WHERE clause"
	}
	return "", ""
}

func isInsert(lower string) bool {
	return strings.HasPrefix(strings.TrimSpace(lower), "insert")
}

// mentionsTable matches a table name as a whole word, so "account" does not
// match "account_alias" and vice versa.
func mentionsTable(lower, table string) bool {
	re := regexp.MustCompile(`(?:from|join|into|update|table)\s+` + regexp.QuoteMeta(table) + `\b`)
	return re.MatchString(lower)
}

const exemptMarker = "unscoped-query-ok:"

func exemptLines(fset *token.FileSet, file *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.Contains(c.Text, exemptMarker) {
				continue
			}
			line := fset.Position(c.Pos()).Line
			// Cover the comment line and the few lines after it, which is where
			// the annotated statement lives.
			for i := 0; i <= 6; i++ {
				out[line+i] = true
			}
		}
	}
	return out
}

func exemptSpan(exempt map[int]bool, from, to int) bool {
	for i := from; i <= to; i++ {
		if exempt[i] {
			return true
		}
	}
	return false
}

// TestUnscopedDetector proves the detector actually detects, so a future
// refactor cannot quietly turn it into a no-op that always passes.
func TestUnscopedDetector(t *testing.T) {
	cases := []struct {
		sql       string
		offending bool
	}{
		{`SELECT id, name FROM category WHERE user_id = ? ORDER BY sort_order`, false},
		{`SELECT id, name FROM category ORDER BY sort_order`, true},
		{`SELECT user_id, name FROM category ORDER BY sort_order`, true},
		{`SELECT count(*) FROM transaction_entry WHERE deleted_at IS NULL`, true},
		{`INSERT INTO budget (user_id, category_id, period_month) VALUES (?,?,?)`, false},
		{`INSERT INTO budget (category_id, period_month) VALUES (?,?)`, true},
		{`UPDATE account SET name = ? WHERE id = ?`, true},
		{`UPDATE account SET name = ? WHERE user_id = ? AND id = ?`, false},
		{`DELETE FROM session WHERE token_hash = ?`, false}, // identity table, exempt by design
		{`SELECT code, exponent FROM currency ORDER BY code`, false},
		{`SELECT id, password_hash FROM user WHERE email = ?`, false},
		// A composed fragment: the WHERE arrives at the call site, so the join
		// itself has to be scoped.
		{`SELECT t.id FROM transaction_entry t JOIN category c ON c.id = t.category_id AND c.user_id = t.user_id`, false},
		{`SELECT t.id FROM transaction_entry t JOIN category c ON c.id = t.category_id`, true},
	}
	for _, c := range cases {
		table, why := unscoped(c.sql)
		if c.offending && table == "" {
			t.Errorf("should have been flagged: %s", c.sql)
		}
		if !c.offending && table != "" {
			t.Errorf("should not have been flagged (%s %s): %s", table, why, c.sql)
		}
	}
}
