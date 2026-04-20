package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/board"
	"github.com/mingzhi1/coden/internal/core/model"
)

// WSMessage is the JSON envelope for WebSocket messages.
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WSHub manages WebSocket client connections and broadcasts.
type WSHub struct {
	mu      sync.RWMutex
	clients map[*WSClient]struct{}
}

// WSClient represents a single WebSocket connection.
type WSClient struct {
	hub     *WSHub
	conn    http.ResponseWriter
	flusher http.Flusher
	done    chan struct{}
	send    chan []byte
}

// NewWSHub creates a new WebSocket hub.
func NewWSHub() *WSHub {
	return &WSHub{
		clients: make(map[*WSClient]struct{}),
	}
}

// Run processes hub lifecycle. Currently a placeholder for future cleanup.
func (h *WSHub) Run(ctx context.Context) {
	<-ctx.Done()
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		close(c.done)
		delete(h.clients, c)
	}
}

// Register adds a client to the hub.
func (h *WSHub) Register(c *WSClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// Unregister removes a client from the hub.
func (h *WSHub) Unregister(c *WSClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Broadcast sends a message to all connected clients.
func (h *WSHub) Broadcast(msg *WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("[ws] marshal broadcast", "err", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Client too slow, skip
		}
	}
}

// ClientCount returns the number of connected clients.
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// mapEventToWSMessage converts a CodeN Event Bus event to a WebSocket message.
// Returns nil if the event should not be forwarded.
func mapEventToWSMessage(ev model.Event) *WSMessage {
	switch ev.Topic {
	case model.EventWorkflowStarted:
		return makeWSMsg("workflow.started", ev.Payload)
	case model.EventWorkflowStepUpdate:
		return makeWSMsg("workflow.step", ev.Payload)
	case model.EventWorkerStarted:
		return makeWSMsg("agent.progress", ev.Payload)
	case model.EventWorkerFinished:
		return makeWSMsg("agent.progress", ev.Payload)
	case model.EventToolStarted:
		return makeWSMsg("tool.activity", ev.Payload)
	case model.EventToolFinished:
		return makeWSMsg("tool.activity", ev.Payload)
	case model.EventCheckpointUpdated:
		return makeWSMsg("workflow.checkpoint", ev.Payload)
	case model.EventWorkflowFailed:
		return makeWSMsg("workflow.failed", ev.Payload)
	case model.EventWorkflowTaskAppended:
		return makeWSMsg("card.created", ev.Payload)
	case model.EventWorkflowTaskSkipped:
		return makeWSMsg("card.updated", ev.Payload)
	case model.EventWorkspaceChanged:
		return makeWSMsg("workspace.changed", ev.Payload)
	case model.EventSessionCreated:
		return makeWSMsg("session.created", ev.Payload)
	case model.EventSessionAttached:
		return makeWSMsg("session.attached", ev.Payload)
	case model.EventSessionDetached:
		return makeWSMsg("session.detached", ev.Payload)
	case model.EventWorkflowCanceled:
		return makeWSMsg("workflow.canceled", ev.Payload)
	case model.EventWorkflowRetry:
		return makeWSMsg("workflow.retry", ev.Payload)
	case model.EventWorkerMessage:
		return makeWSMsg("worker.message", ev.Payload)
	default:
		return nil
	}
}

func makeWSMsg(typ string, payload json.RawMessage) *WSMessage {
	return &WSMessage{
		Type: typ,
		Data: payload,
	}
}

// handleWebSocket upgrades an HTTP connection to a WebSocket connection
// using Server-Sent Events (SSE) as a fallback since we don't want
// external WebSocket library dependencies.
//
// For a production WebSocket implementation, replace this with
// gorilla/websocket or nhooyr.io/websocket.
//
// This SSE-based approach provides the same real-time push capability
// and the frontend JS handles both transports transparently.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Check if this is a WebSocket upgrade request
	if r.Header.Get("Upgrade") == "websocket" {
		// For true WebSocket, we'd need gorilla/websocket.
		// For now, fall through to SSE which provides equivalent push functionality.
		slog.Debug("[ws] WebSocket upgrade requested, falling back to SSE")
	}

	// Use SSE (Server-Sent Events) — no external dependencies needed.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &WSClient{
		hub:     s.wsHub,
		conn:    w,
		flusher: flusher,
		done:    make(chan struct{}),
		send:    make(chan []byte, 64),
	}
	s.wsHub.Register(client)
	defer s.wsHub.Unregister(client)

	slog.Info("[ws] SSE client connected", "clients", s.wsHub.ClientCount())

	// Send initial sync
	s.sendBoardSync(client)

	// Keep-alive ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			slog.Info("[ws] SSE client disconnected")
			return
		case <-client.done:
			return
		case data := <-client.send:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// sendBoardSync sends a full board state to a newly connected client.
func (s *Server) sendBoardSync(client *WSClient) {
	boards, err := s.store.ListBoards()
	if err != nil || len(boards) == 0 {
		return
	}

	// Send first board's data
	b := boards[0]
	cards, _ := s.store.ListCards(b.ID)

	type syncData struct {
		Board    *board.Board  `json:"board"`
		Cards    []*board.Card `json:"cards"`
		Sessions []any         `json:"sessions,omitempty"`
	}

	sd := syncData{Board: b, Cards: cards}

	// Include sessions if ClientAPI is available
	if s.clientAPI != nil {
		if sessions, err := s.clientAPI.ListSessions(context.Background(), 50); err == nil {
			sd.Sessions = make([]any, len(sessions))
			for i, sess := range sessions {
				sd.Sessions[i] = sess
			}
		}
	}

	data, _ := json.Marshal(sd)
	msg := WSMessage{Type: "board.sync", Data: data}
	msgBytes, _ := json.Marshal(msg)
	select {
	case client.send <- msgBytes:
	default:
	}
}
