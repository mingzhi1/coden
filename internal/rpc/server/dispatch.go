package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"time"

	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

type clientConnContextKey struct{}

// defaultRequestTimeout is the per-request timeout for handler execution.
const defaultRequestTimeout = 5 * time.Minute

// ServeConn handles a single client connection.
// It reads requests, dispatches them, and sends responses.
// Blocks until the connection is closed or ctx is cancelled.
func (s *Server) ServeConn(ctx context.Context, rwc io.ReadWriteCloser) {
	ctx, cancel := context.WithCancel(ctx)
	codec := transport.NewCodec(rwc)
	cc := &clientConn{codec: codec, connCtx: ctx, cancel: cancel, subscriptions: make(map[string]subscription)}
	ctx = context.WithValue(ctx, clientConnContextKey{}, cc)

	s.mu.Lock()
	s.clients[cc] = struct{}{}
	s.mu.Unlock()

	defer func() {
		cc.cancelAllSubscriptions()
		cancel()
		codec.Close()
		s.mu.Lock()
		delete(s.clients, cc)
		s.mu.Unlock()
	}()

	for {
		raw, err := codec.ReadMessage()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			return
		}

		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			resp := protocol.NewError(nil, protocol.CodeParseError, "parse error")
			if wErr := codec.WriteMessage(resp); wErr != nil {
				log.Printf("rpc/server: write parse-error response failed: %v", wErr)
				return
			}
			continue
		}

		if req.JSONRPC != protocol.Version {
			resp := protocol.NewError(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version")
			if wErr := codec.WriteMessage(resp); wErr != nil {
				log.Printf("rpc/server: write invalid-version response failed: %v", wErr)
				return
			}
			continue
		}

		// Dispatch asynchronously so long-running handlers (e.g. workflow.submit)
		// do not block subsequent requests on the same connection.
		go s.dispatch(ctx, cc, req)
	}
}

func (s *Server) dispatch(ctx context.Context, cc *clientConn, req protocol.Request) {
	if !protocol.SupportsKernelServer(req.Method) {
		if !req.IsNotification() {
			resp := protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method))
			s.writeOrLog(cc, resp)
		}
		return
	}

	handler, ok := s.handlers[req.Method]
	if !ok {
		if !req.IsNotification() {
			resp := protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method))
			s.writeOrLog(cc, resp)
		}
		return
	}

	// Per-request timeout so a single slow handler cannot block forever.
	reqCtx, reqCancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer reqCancel()

	result, err := handler(reqCtx, req.Params)
	if req.IsNotification() {
		return // notifications don't get responses
	}

	if err != nil {
		resp := protocol.NewErrorFromErr(req.ID, err)
		s.writeOrLog(cc, resp)
		return
	}

	resp := protocol.NewResult(req.ID, result)
	s.writeOrLog(cc, resp)
}

// writeOrLog writes a message to the client, logging on failure.
func (s *Server) writeOrLog(cc *clientConn, msg any) {
	if err := cc.codec.WriteMessage(msg); err != nil {
		log.Printf("rpc/server: write to client failed: %v", err)
	}
}

func clientConnFromContext(ctx context.Context) *clientConn {
	cc, _ := ctx.Value(clientConnContextKey{}).(*clientConn)
	return cc
}

func (s *Server) notifyClient(cc *clientConn, method string, params any) {
	if cc == nil {
		return
	}
	notif, err := protocol.NewNotification(method, params)
	if err != nil {
		return
	}
	if wErr := cc.codec.WriteMessage(notif); wErr != nil {
		log.Printf("rpc/server: notify client failed: %v", wErr)
	}
}

// Broadcast sends a notification to all connected clients.
func (s *Server) Broadcast(method string, params any) {
	notif, err := protocol.NewNotification(method, params)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for cc := range s.clients {
		if wErr := cc.codec.WriteMessage(notif); wErr != nil {
			log.Printf("rpc/server: broadcast to client failed: %v", wErr)
		}
	}
}
