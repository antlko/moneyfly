package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func value(v float64) *float64 { return &v }

// registry is the fixed set of identifiers a metric may name. Everything outside
// it is a named error, not a silent zero.
func registry(values map[string]*float64) Resolver {
	return func(name, arg string) (*float64, error) {
		key := name
		if arg != "" {
			key = name + "('" + arg + "')"
		}
		v, ok := values[key]
		if !ok {
			return nil, &ParseError{Message: "unknown identifier " + key}
		}
		return v, nil
	}
}

func TestMetricParser_ValidExpression(t *testing.T) {
	node, err := Parse("ready_for_usage / possible_minimum")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Eval(node, registry(map[string]*float64{
		"ready_for_usage":  value(18319.66),
		"possible_minimum": value(1780),
	}))
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got == nil || *got < 10.29 || *got > 10.30 {
		t.Fatalf("result = %v, want about 10.29", got)
	}
}

func TestMetricParser_PrecedenceAndParentheses(t *testing.T) {
	cases := map[string]float64{
		"2 + 3 * 4":     14,
		"(2 + 3) * 4":   20,
		"10 / 2 / 5":    1,
		"-3 + 5":        2,
		"2 * 3 > 5":     1,
		"abs(0 - 7)":    7,
		"min(4, 9, 2)":  2,
		"max(4, 9, 2)":  9,
		"mean(2, 4, 9)": 5,
	}
	for expression, want := range cases {
		t.Run(expression, func(t *testing.T) {
			node, err := Parse(expression)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, err := Eval(node, nil)
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got == nil || *got != want {
				t.Fatalf("= %v, want %v", got, want)
			}
		})
	}
}

func TestMetricParser_CategoryReference(t *testing.T) {
	node, err := Parse("spend('Food') / spend_total")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Eval(node, registry(map[string]*float64{
		"spend('Food')": value(300),
		"spend_total":   value(1200),
	}))
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got == nil || *got != 0.25 {
		t.Fatalf("food share = %v, want 0.25", got)
	}
}

func TestMetricParser_RejectsFunctionCall(t *testing.T) {
	// An arbitrary call is the whole thing this grammar exists to prevent.
	for _, expression := range []string{
		"exec('rm -rf /')",
		"fetch('http://example.test')",
		"eval('1+1')",
		"require('fs')",
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := Parse(expression); err == nil {
				t.Fatal("must be rejected")
			}
		})
	}
}

func TestMetricParser_RejectsUnknownIdentifier(t *testing.T) {
	known := func(name string) bool { return name == "spend_total" }

	if err := Validate("spend_total * 2", known); err != nil {
		t.Fatalf("a known identifier must validate: %v", err)
	}
	err := Validate("mystery_number * 2", known)
	if err == nil {
		t.Fatal("an unknown identifier must be rejected")
	}
	if !strings.Contains(err.Error(), "mystery_number") {
		t.Fatalf("error = %q, want it to name the identifier", err)
	}
}

func TestMetricParser_DivByZeroIsNull(t *testing.T) {
	node, err := Parse("spend_total / income")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Eval(node, registry(map[string]*float64{
		"spend_total": value(1200),
		"income":      value(0),
	}))
	if err != nil {
		t.Fatalf("division by zero must not be an error: %v", err)
	}
	if got != nil {
		t.Fatalf("result = %v, want nil — not Inf, not 0", *got)
	}
}

func TestMetricParser_AbsentInputPropagates(t *testing.T) {
	node, _ := Parse("spend_total / income")
	got, err := Eval(node, registry(map[string]*float64{
		"spend_total": value(1200),
		"income":      nil, // the month recorded no income at all
	}))
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != nil {
		t.Fatalf("result = %v, want nil — an unrecorded input makes the metric unrecorded", *got)
	}
}

func TestMetricParser_CountOfComparison(t *testing.T) {
	// The spec's savings_streak, written out.
	node, err := Parse("count(a > 0.2, b > 0.2, c > 0.2)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Eval(node, registry(map[string]*float64{
		"a": value(0.36), "b": value(0.04), "c": value(0.37),
	}))
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got == nil || *got != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
}

func TestMetricParser_SyntaxErrorsAreLocated(t *testing.T) {
	for _, expression := range []string{
		"1 +",
		"(1 + 2",
		"1 $ 2",
		"min()",
		"abs(1, 2)",
		"'just a string'",
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := Parse(expression)
			if err == nil {
				t.Fatal("must be rejected")
			}
			if err.Error() == "" {
				t.Fatal("the error must say something")
			}
		})
	}
}

// TestMetricParser_NoEval is the architectural guard: the evaluator walks an AST.
// The moment anything in this package reaches for a code path that executes
// text, the restricted grammar stops being a restriction.
func TestMetricParser_NoEval(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	forbidden := []string{"os/exec", "plugin", "text/template", "go/eval"}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(body), needle) {
				t.Errorf("%s imports %q; a metric must never be executed as code", e.Name(), needle)
			}
		}
	}
}
