package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/board"
	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/model"
)

//go:embed static
var staticFS embed.FS

// SessionAPI defines the subset of the ClientAPI used by the web layer.
// Implementations include *api.Service (production) and fakes (testing).
type SessionAPI interface {
	ListSessions(ctx context.Context, limit int) ([]model.Session, error)
	CreateSession(ctx context.Context, sessionID string) (model.Session, error)
	RenameSession(ctx context.Context, sessionID, name string) (model.Session, error)
	WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error)
	Submit(ctx context.Context, sessionID, prompt string) (string, error)
}

// Server is the Kanban web server that serves the board UI and API.
type Server struct {
	addr      string
	store     board.Store
	clientAPI SessionAPI // optional; enables session management when non-nil
	eventBus  *events.Bus
	server    *http.Server
	wsHub     *WSHub
	mu        sync.Mutex
}

// Config holds web server configuration.
type Config struct {
	Addr      string       // listen address, e.g. "127.0.0.1:7200"
	Store     board.Store
	ClientAPI SessionAPI   // optional; enables /api/sessions routes when non-nil
	EventBus  *events.Bus
}

// New creates a new web server.
func New(cfg Config) *Server {
	s := &Server{
		addr:      cfg.Addr,
		store:     cfg.Store,
		clientAPI: cfg.ClientAPI,
		eventBus:  cfg.EventBus,
		wsHub:     NewWSHub(),
	}
	return s
}

// Handler builds and returns the HTTP handler for the web server.
// It registers all API routes (boards, cards, WebSocket) with CORS
// middleware applied. Callers can wrap or serve the handler directly.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- API routes ---
	mux.HandleFunc("GET /api/boards", s.handleListBoards)
	mux.HandleFunc("POST /api/boards", s.handleCreateBoard)
	mux.HandleFunc("GET /api/boards/{boardId}", s.handleGetBoard)

	mux.HandleFunc("GET /api/boards/{boardId}/cards", s.handleListCards)
	mux.HandleFunc("POST /api/boards/{boardId}/cards", s.handleCreateCard)
	mux.HandleFunc("GET /api/boards/{boardId}/cards/{cardId}", s.handleGetCard)
	mux.HandleFunc("PATCH /api/boards/{boardId}/cards/{cardId}", s.handleUpdateCard)
	mux.HandleFunc("DELETE /api/boards/{boardId}/cards/{cardId}", s.handleDeleteCard)
	mux.HandleFunc("POST /api/boards/{boardId}/cards/{cardId}/move", s.handleMoveCard)

	// --- Session routes (require ClientAPI) ---
	if s.clientAPI != nil {
		mux.HandleFunc("GET /api/sessions", s.handleListSessions)
		mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
		mux.HandleFunc("GET /api/sessions/{sessionId}/changes", s.handleSessionChanges)
		mux.HandleFunc("POST /api/cards/{cardId}/submit", s.handleSubmitCard)
	}

	// --- WebSocket ---
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// --- Static files ---
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("[web] failed to load static files", "err", err)
		return withCORS(mux)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticContent)))

	return withCORS(mux)
}

// Start begins serving HTTP and WebSocket connections.
// It blocks until the context is canceled or an error occurs.
func (s *Server) Start(ctx context.Context) error {
	handler := s.Handler()

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start the WebSocket hub and event bridge.
	go s.wsHub.Run(ctx)
	if s.eventBus != nil {
		go s.bridgeEvents(ctx)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.addr, err)
	}
	slog.Info("[web] Kanban server started", "addr", s.addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// bridgeEvents subscribes to the CodeN Event Bus and forwards relevant
// events to all connected WebSocket clients.
func (s *Server) bridgeEvents(ctx context.Context) {
	ch, cancel := s.eventBus.Subscribe("")
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			msg := mapEventToWSMessage(ev)
			if msg != nil {
				s.wsHub.Broadcast(msg)
			}
		}
	}
}

// withCORS allows cross-origin requests only from localhost origins, preventing
// malicious web pages from making requests to this local server.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isLocalhostOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalhostOrigin reports whether the Origin header refers to a localhost address.
func isLocalhostOrigin(origin string) bool {
	// Strip scheme
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip port
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
