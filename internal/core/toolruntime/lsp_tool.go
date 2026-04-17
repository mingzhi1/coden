package toolruntime

import (
	"context"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/lsp"
	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// LSPTool implements Executor for LSP operations.
type LSPTool struct {
	manager *lsp.Manager
	lang    string
}

// Manager exposes the underlying LSP manager for in-process discovery logic.
// This is intentionally narrow and does not change the tool protocol surface.
func (t *LSPTool) Manager() *lsp.Manager {
	if t == nil {
		return nil
	}
	return t.manager
}

// NewLSPTool creates a new LSP tool that wraps the LSP manager.
func NewLSPTool(manager *lsp.Manager, lang string) *LSPTool {
	return &LSPTool{
		manager: manager,
		lang:    lang,
	}
}

// Execute implements Executor.
func (t *LSPTool) Execute(ctx context.Context, req Request) (Result, error) {
	// Lazy start: if not ready, try to start once.
	if !t.manager.IsReady() {
		if err := t.manager.Start(ctx); err != nil {
			return Result{}, fmt.Errorf("LSP %s not available: %w", t.lang, err)
		}
	}

	switch req.Kind {
	case "lsp_symbols":
		return t.executeSymbols(ctx, req)
	case "lsp_definition":
		return t.executeDefinition(ctx, req)
	case "lsp_references":
		return t.executeReferences(ctx, req)
	case "lsp_didopen":
		return t.executeDidOpen(ctx, req)
	default:
		return Result{}, fmt.Errorf("unsupported LSP tool kind: %s", req.Kind)
	}
}

func (t *LSPTool) executeSymbols(ctx context.Context, req Request) (Result, error) {
	symbols, err := t.manager.DocumentSymbol(ctx, req.Path)
	if err != nil {
		return Result{}, err
	}

	evidence := lsp.SymbolsToEvidence(symbols, req.Path)
	output := formatEvidence(evidence)

	return Result{
		Summary: fmt.Sprintf("found %d symbols in %s", len(symbols), req.Path),
		Output:  output,
	}, nil
}

func (t *LSPTool) executeDefinition(ctx context.Context, req Request) (Result, error) {
	locations, err := t.manager.Definition(ctx, req.Path, req.Line, req.Column)
	if err != nil {
		return Result{}, err
	}

	evidence := lsp.LocationsToEvidence(locations, "definition")
	output := formatEvidence(evidence)

	return Result{
		Summary: fmt.Sprintf("found %d definition(s)", len(locations)),
		Output:  output,
	}, nil
}

func (t *LSPTool) executeReferences(ctx context.Context, req Request) (Result, error) {
	locations, err := t.manager.References(ctx, req.Path, req.Line, req.Column)
	if err != nil {
		return Result{}, err
	}

	evidence := lsp.LocationsToEvidence(locations, "references")
	output := formatEvidence(evidence)

	return Result{
		Summary: fmt.Sprintf("found %d reference(s)", len(locations)),
		Output:  output,
	}, nil
}

func (t *LSPTool) executeDidOpen(ctx context.Context, req Request) (Result, error) {
	err := t.manager.DidOpen(req.Path, req.Content)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Summary: fmt.Sprintf("opened %s in LSP", req.Path),
	}, nil
}

// formatEvidence formats RetrievalEvidence using the unified format.
func formatEvidence(evidence []retrieval.RetrievalEvidence) string {
	return retrieval.FormatForPrompt(evidence)
}

// createLSPTools creates LSP tool instances for all configured languages.
func createLSPTools(rootDir string, cfg *config.ToolsConfig) (map[string]Executor, error) {
	tools := make(map[string]Executor)

	for lang, lspConfig := range cfg.LSP {
		if !lspConfig.Enabled {
			continue
		}

		manager, err := lsp.NewManagerFromConfig(rootDir, lang, lspConfig)
		if err != nil {
			return nil, fmt.Errorf("create LSP manager for %s: %w", lang, err)
		}

		// Auto-start in background if configured (non-blocking).
		if lspConfig.AutoStart {
			go func(m *lsp.Manager, l string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = m.Start(ctx) // best-effort; lazy start in Execute is the fallback
			}(manager, lang)
		}

		tool := NewLSPTool(manager, lang)
		tools[lang] = tool
	}

	return tools, nil
}
