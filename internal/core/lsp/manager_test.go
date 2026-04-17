package lsp

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestManagerNotInstalled(t *testing.T) {
	config := ManagerConfig{
		RootDir:      ".",
		Lang:         "go",
		CmdPath:      "nonexistent_lsp_binary",
		StartTimeout: 5 * time.Second,
		CallTimeout:  5 * time.Second,
		MaxRestarts:  1,
	}
	
	mgr := NewManager(config)
	ctx := context.Background()
	
	err := mgr.Start(ctx)
	if err == nil {
		t.Error("Expected error for non-existent LSP binary")
	}
	
	if mgr.IsReady() {
		t.Error("Manager should not be ready after failed start")
	}
}

func TestManagerGoplsLifecycle(t *testing.T) {
	// Skip if gopls not installed
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	
	config := DefaultManagerConfig(".")
	config.StartTimeout = 30 * time.Second
	
	mgr := NewManager(config)
	ctx := context.Background()
	
	// Start
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	
	if !mgr.IsReady() {
		t.Error("Manager should be ready after start")
	}
	
	// Test document symbol on a Go file
	symbols, err := mgr.DocumentSymbol(ctx, "client.go")
	if err != nil {
		// This may fail if file doesn't exist or LSP hasn't indexed
		t.Logf("DocumentSymbol error (may be expected): %v", err)
	} else {
		t.Logf("Found %d symbols", len(symbols))
	}
	
	// Stop
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	
	if err := mgr.Stop(stopCtx); err != nil {
		t.Errorf("Stop error: %v", err)
	}
	
	if mgr.IsReady() {
		t.Error("Manager should not be ready after stop")
	}
}

func TestManagerRestart(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	
	config := DefaultManagerConfig(".")
	config.MaxRestarts = 2
	
	mgr := NewManager(config)
	ctx := context.Background()
	
	// First start
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	
	// Restart
	if err := mgr.Restart(ctx); err != nil {
		t.Errorf("Restart failed: %v", err)
	}
	
	if !mgr.IsReady() {
		t.Error("Manager should be ready after restart")
	}
	
	// Cleanup
	mgr.Stop(ctx)
}

func TestSymbolsToEvidence(t *testing.T) {
	symbols := []DocumentSymbol{
		{
			Name:   "Client",
			Kind:   SymbolKindStruct,
			Detail: "struct",
			Range: Range{
				Start: Position{Line: 50, Character: 5},
				End:   Position{Line: 60, Character: 1},
			},
			Children: []DocumentSymbol{
				{
					Name:   "Call",
					Kind:   SymbolKindMethod,
					Detail: "func(context.Context, string, interface{}) (*Response, error)",
					Range: Range{
						Start: Position{Line: 55, Character: 0},
						End:   Position{Line: 58, Character: 1},
					},
				},
			},
		},
	}
	
	evidence := SymbolsToEvidence(symbols, "client.go")
	
	if len(evidence) != 2 { // Parent + child
		t.Errorf("Expected 2 evidence, got %d", len(evidence))
	}
	
	e := evidence[0]
	if e.Source != "lsp" {
		t.Errorf("Expected source 'lsp', got %s", e.Source)
	}
	if !e.Verified {
		t.Error("LSP evidence should be verified")
	}
	if e.Line != 51 { // 0-based to 1-based
		t.Errorf("Expected line 51, got %d", e.Line)
	}
}
