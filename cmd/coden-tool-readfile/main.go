package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/rpc/transport"
	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

func main() {
	serve := flag.String("serve", "", "serve read/search tool on this TCP address; default is stdio")
	workspaceRoot := flag.String("workspace", filepath.Join(".", "workspace"), "workspace root")
	flag.Parse()

	srv := toolserver.New(
		"coden-tool-readfile",
		"filesystem-read",
		"short",
		[]string{"read_file", "list_dir", "search"},
		toolruntime.NewLocalExecutor(workspace.New(*workspaceRoot)),
	)
	ctx := context.Background()

	if *serve == "" {
		srv.ServeConn(ctx, transport.Stdio())
		return
	}

	ln, err := net.Listen("tcp", *serve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Fprintf(os.Stderr, "CodeN read/search tool serving on %s\n", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
			continue
		}
		go srv.ServeConn(ctx, conn)
	}
}
