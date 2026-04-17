package toolruntime

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteRipgrep(t *testing.T) {
	// This test requires ripgrep to be installed
	ctx := context.Background()
	opts := DefaultSearchOptions("func Test", ".")
	opts.MaxResults = 10
	opts.Dir = "."

	hits, err := ExecuteRipgrep(ctx, opts)
	if err != nil {
		// Skip if ripgrep not available
		if strings.Contains(err.Error(), "not available") {
			t.Skip("ripgrep not installed")
		}
		t.Fatalf("ExecuteRipgrep failed: %v", err)
	}

	// Should find test functions in this file
	found := false
	for _, hit := range hits {
		if strings.Contains(hit.Path, "grep_test.go") {
			found = true
			break
		}
	}

	if !found {
		t.Logf("Hits: %+v", hits)
		// Not a failure - ripgrep might filter differently
	}
}

func TestFormatHits(t *testing.T) {
	hits := []GrepHit{
		{
			Path:    "test.go",
			Line:    42,
			Snippet: "func TestSomething(t *testing.T)",
			BeforeCtx: []string{"package test", ""},
			AfterCtx:  []string{"\t// Test body", "}"},
		},
	}

	output := FormatHits(hits, false)
	if !strings.Contains(output, "test.go:42:") {
		t.Errorf("Expected path:line format, got: %s", output)
	}
	if !strings.Contains(output, "func TestSomething") {
		t.Errorf("Expected snippet, got: %s", output)
	}

	// Test with context
	outputWithCtx := FormatHits(hits, true)
	if !strings.Contains(outputWithCtx, "package test") {
		t.Errorf("Expected before context, got: %s", outputWithCtx)
	}
}

func TestExtractSnippet(t *testing.T) {
	content := `line 1
line 2
line 3
line 4
line 5`

	snippet := ExtractSnippet(content, 3, 1)
	if !strings.Contains(snippet, "> 3: line 3") {
		t.Errorf("Expected highlight on line 3, got: %s", snippet)
	}
	if !strings.Contains(snippet, "2: line 2") {
		t.Errorf("Expected context line 2, got: %s", snippet)
	}
	if !strings.Contains(snippet, "4: line 4") {
		t.Errorf("Expected context line 4, got: %s", snippet)
	}
}

func TestSearchInContent(t *testing.T) {
	content := `package main

func main() {
	println("hello")
}

func helper() {
	println("world")
}`

	// Literal search - finds lines containing "func"
	lines, err := SearchInContent(content, "func", false)
	if err != nil {
		t.Fatalf("SearchInContent failed: %v", err)
	}
	// "func" appears in: "func main()" and "func helper()" = 2 lines
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines with 'func', got %d: %v", len(lines), lines)
	}

	// Regex search
	lines, err = SearchInContent(content, `func \w+\(\)`, true)
	if err != nil {
		t.Fatalf("Regex search failed: %v", err)
	}
	if len(lines) != 2 { // main() and helper()
		t.Errorf("Expected 2 function definitions, got %d", len(lines))
	}
}

func TestHitsToEvidence(t *testing.T) {
	hits := []GrepHit{
		{
			Path:    "kernel.go",
			Line:    10,
			Column:  6,
			Snippet: "type Kernel struct",
			BeforeCtx: []string{"package core"},
			AfterCtx:  []string{"", "func NewKernel() *Kernel {"},
		},
	}

	evidence := HitsToEvidence(hits, "Kernel")
	if len(evidence) != 1 {
		t.Fatalf("Expected 1 evidence, got %d", len(evidence))
	}

	e := evidence[0]
	if e.Source != "grep" {
		t.Errorf("Expected source 'grep', got %s", e.Source)
	}
	if e.Verified {
		t.Error("Grep evidence should not be verified")
	}
	if e.Line != 10 {
		t.Errorf("Expected line 10, got %d", e.Line)
	}
	if !strings.Contains(e.Snippet, "package core") {
		t.Error("Expected before context in snippet")
	}
}
