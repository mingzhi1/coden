package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mingzhi1/coden/internal/agent/plan"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func main() {
	serve := flag.String("serve", "", "serve planner worker on this TCP address; default is stdio")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := plan.NewServer(workflow.NewLocalPlanner())

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

	// Close the listener when context is canceled so Accept() unblocks.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	fmt.Fprintf(os.Stderr, "CodeN planner worker serving on %s\n", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return // context canceled — clean shutdown
			}
			fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
			continue
		}
		go srv.ServeConn(ctx, conn)
	}
}
