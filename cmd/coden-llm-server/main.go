package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func main() {
	addr := flag.String("addr", ":7533", "TCP address to listen on")
	configPath := flag.String("config", "", "path to config.yaml (shared with CodeN)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := NewServer(*configPath)

	if *addr == "" || *addr == "-" {
		srv.ServeConn(ctx, transport.Stdio())
		return
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	slog.Info("llm-server listening", "addr", ln.Addr().String())
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return // context canceled — clean shutdown
			}
			slog.Error("accept error", "err", err)
			continue
		}
		go srv.ServeConn(ctx, conn)
	}
}
