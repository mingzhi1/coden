package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mingzhi1/coden/internal/core/board"
)

// ── Session Handlers ─────────────────────────────────────────────────────────
// These handlers require s.clientAPI to be non-nil (guarded by route registration).

// handleListSessions returns all active sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.clientAPI.ListSessions(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type sessionDTO struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}

	out := make([]sessionDTO, 0, len(sessions))
	for _, sess := range sessions {
		name := sess.Name
		if name == "" {
			name = sess.ID
		}
		out = append(out, sessionDTO{
			ID:        sess.ID,
			Name:      name,
			CreatedAt: sess.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateSession creates a new session.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	sessionID := board.GenerateCardID() // reuse hash-based ID generator
	sess, err := s.clientAPI.CreateSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Rename if a name was provided.
	if req.Name != "" {
		sess, err = s.clientAPI.RenameSession(r.Context(), sess.ID, req.Name)
		if err != nil {
			slog.Warn("[web] rename session failed", "err", err)
		}
	}

	type sessionDTO struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	name := sess.Name
	if name == "" {
		name = sess.ID
	}
	dto := sessionDTO{
		ID:        sess.ID,
		Name:      name,
		CreatedAt: sess.CreatedAt.Format(time.RFC3339),
	}

	// Broadcast to WebSocket clients so other tabs see the new session.
	data, _ := json.Marshal(dto)
	s.wsHub.Broadcast(&WSMessage{Type: "session.created", Data: data})

	writeJSON(w, http.StatusCreated, dto)
}

// handleSessionChanges returns workspace changes for a session.
func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")

	changes, err := s.clientAPI.WorkspaceChanges(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type changeDTO struct {
		Path      string `json:"path"`
		Operation string `json:"operation"`
	}

	out := make([]changeDTO, 0, len(changes))
	for _, c := range changes {
		out = append(out, changeDTO{
			Path:      c.Path,
			Operation: c.Operation,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSubmitCard binds a card to a session and triggers workflow execution.
func (s *Server) handleSubmitCard(w http.ResponseWriter, r *http.Request) {
	cardID := r.PathValue("cardId")

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	// Look up the card.
	card, err := s.store.GetCard(cardID)
	if err != nil {
		writeError(w, http.StatusNotFound, "card not found")
		return
	}

	// Submit the card's description as a workflow prompt.
	prompt := card.Title
	if card.Description != "" {
		prompt = card.Title + "\n\n" + card.Description
	}

	workflowID, err := s.clientAPI.Submit(r.Context(), req.SessionID, prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Update card with session and workflow binding.
	now := time.Now().UTC()
	card.SessionID = req.SessionID
	card.WorkflowID = workflowID
	card.ColumnID = board.ColumnInProgress
	card.StartedAt = &now
	card.UpdatedAt = now
	if err := s.store.UpdateCard(card); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update card: "+err.Error())
		return
	}

	// Broadcast update.
	s.broadcastCardEvent("card.updated", card)

	type submitResp struct {
		CardID     string `json:"card_id"`
		SessionID  string `json:"session_id"`
		WorkflowID string `json:"workflow_id"`
	}
	writeJSON(w, http.StatusOK, submitResp{
		CardID:     cardID,
		SessionID:  req.SessionID,
		WorkflowID: workflowID,
	})
}
