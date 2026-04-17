package kernel

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

func TestValidateToolPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind    string
		path    string
		wantErr bool
	}{
		{"write_file", "src/main.go", false},
		{"write_file", "artifacts/output.md", false},
		{"read_file", "../../../etc/passwd", false}, // read_file not validated
		{"write_file", "../../../etc/passwd", true},
		{"write_file", "/absolute/path.go", true},
		{"edit_file", "C:\\Windows\\system32\\file.txt", true},
		{"write_file", "src/../../../escape.go", true},
		{"write_file", "", true},
		{"edit_file", "valid/path.go", false},
		{"list_dir", "../escape", false}, // list_dir not validated
	}

	for _, tt := range tests {
		err := validateToolPath(tt.kind, tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateToolPath(%q, %q) = %v, wantErr=%v", tt.kind, tt.path, err, tt.wantErr)
		}
	}
}

func TestValidateTaskDAG(t *testing.T) {
	t.Parallel()

	t.Run("no tasks", func(t *testing.T) {
		if err := validateTaskDAG(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("linear chain", func(t *testing.T) {
		tasks := []model.Task{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B", DependsOn: []string{"a"}},
			{ID: "c", Title: "C", DependsOn: []string{"b"}},
		}
		if err := validateTaskDAG(tasks); err != nil {
			t.Fatalf("valid DAG rejected: %v", err)
		}
	})

	t.Run("diamond DAG", func(t *testing.T) {
		tasks := []model.Task{
			{ID: "root", Title: "root"},
			{ID: "left", Title: "left", DependsOn: []string{"root"}},
			{ID: "right", Title: "right", DependsOn: []string{"root"}},
			{ID: "merge", Title: "merge", DependsOn: []string{"left", "right"}},
		}
		if err := validateTaskDAG(tasks); err != nil {
			t.Fatalf("valid DAG rejected: %v", err)
		}
	})

	t.Run("direct cycle", func(t *testing.T) {
		tasks := []model.Task{
			{ID: "a", Title: "A", DependsOn: []string{"b"}},
			{ID: "b", Title: "B", DependsOn: []string{"a"}},
		}
		if err := validateTaskDAG(tasks); err == nil {
			t.Fatal("cycle not detected")
		}
	})

	t.Run("three-node cycle", func(t *testing.T) {
		tasks := []model.Task{
			{ID: "a", Title: "A", DependsOn: []string{"c"}},
			{ID: "b", Title: "B", DependsOn: []string{"a"}},
			{ID: "c", Title: "C", DependsOn: []string{"b"}},
		}
		if err := validateTaskDAG(tasks); err == nil {
			t.Fatal("cycle not detected")
		}
	})

	t.Run("tasks without IDs treated as independent", func(t *testing.T) {
		tasks := []model.Task{
			{Title: "no id 1"},
			{Title: "no id 2"},
		}
		if err := validateTaskDAG(tasks); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateToolScope(t *testing.T) {
	t.Parallel()

	t.Run("no allowed paths — unrestricted", func(t *testing.T) {
		req := toolruntime.Request{Kind: "write_file", Path: "any/file.go"}
		warn, err := validateToolScope(req, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning: %s", warn)
		}
	})

	t.Run("read_file always allowed", func(t *testing.T) {
		req := toolruntime.Request{Kind: "read_file", Path: "secret/file.go"}
		warn, err := validateToolScope(req, []string{"other/file.go"})
		if err != nil {
			t.Fatalf("read should be unrestricted: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning for read: %s", warn)
		}
	})

	t.Run("write within declared path", func(t *testing.T) {
		req := toolruntime.Request{Kind: "write_file", Path: "internal/foo/bar.go"}
		warn, err := validateToolScope(req, []string{"internal/foo/bar.go"})
		if err != nil {
			t.Fatalf("unexpected scope error: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning: %s", warn)
		}
	})

	t.Run("write in declared directory prefix", func(t *testing.T) {
		req := toolruntime.Request{Kind: "edit_file", Path: "internal/foo/bar.go"}
		warn, err := validateToolScope(req, []string{"internal/foo/"})
		if err != nil {
			t.Fatalf("unexpected scope error: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning: %s", warn)
		}
	})

	t.Run("write outside declared paths — soft warning", func(t *testing.T) {
		req := toolruntime.Request{Kind: "write_file", Path: "internal/other/secret.go"}
		warn, err := validateToolScope(req, []string{"internal/foo/bar.go"})
		if err != nil {
			t.Fatalf("scope should be soft, got error: %v", err)
		}
		if warn == "" {
			t.Fatal("expected scope warning for out-of-scope write")
		}
	})
}
