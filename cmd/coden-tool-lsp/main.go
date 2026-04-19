// coden-tool-lsp is the LSP query subprocess for CodeN (M8-05).
//
// It wraps gopls (or any configured LSP server binary) and exposes
// lsp_symbols, lsp_definition, lsp_references, and lsp_didopen as
// JSON-RPC tool calls over stdio or a TCP listener.
//
// Usage:
//
//	coden-tool-lsp [flags]
//	  -workspace  path to the workspace root (default: ./workspace)
//	  -lsp-bin    path to the LSP server binary (default: gopls)
//	  -serve      optional TCP address to listen on; uses stdio when empty
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/mingzhi1/coden/internal/core/lsp"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/transport"
	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

func main() {
	serve := flag.String("serve", "", "serve LSP tool on this TCP address; default is stdio")
	workspaceRoot := flag.String("workspace", filepath.Join(".", "workspace"), "workspace root")
	lspBin := flag.String("lsp-bin", "gopls", "LSP server binary path (e.g. gopls, rust-analyzer)")
	flag.Parse()

	root := *workspaceRoot
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	// Build the LSP manager using the configured binary.
	cfg := lsp.DefaultManagerConfig(root)
	cfg.CmdPath = *lspBin
	manager := lsp.NewManager(cfg)

	// Wrap it in the LSPTool executor.
	executor := toolruntime.NewLSPTool(manager, cfg.Lang)

	// Serve the tool subset that the LSP tool supports.
	srv := toolserver.New(
		"coden-tool-lsp",
		"lsp",
		"medium",
		[]string{"lsp_symbols", "lsp_definition", "lsp_references", "lsp_didopen"},
		executor,
	)

	ctx := context.Background()

	if *serve == "" {
		srv.ServeConn(ctx, transport.Stdio())
		return
	}

	ln, err := net.Listen("tcp", *serve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coden-tool-lsp: listen failed: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Fprintf(os.Stderr, "CodeN LSP tool serving on %s (lsp-bin=%s)\n", ln.Addr(), *lspBin)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "coden-tool-lsp: accept error: %v\n", err)
			continue
		}
		go srv.ServeConn(ctx, conn)
	}
}
