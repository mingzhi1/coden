package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mingzhi1/coden/internal/core/board"
)

// --- Board Handlers ---

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := s.store.ListBoards()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, boards)
}

func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		req.Name = "Default Board"
	}

	b := &board.Board{
		ID:        board.GenerateCardID(), // reuse ID generator
		Name:      req.Name,
		ProjectID: req.ProjectID,
		Columns:   board.DefaultColumns(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateBoard(b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("boardId")
	b, err := s.store.GetBoard(boardID)
	if err != nil {
		writeError(w, http.StatusNotFound, "board not found")
		return
	}

	cards, _ := s.store.ListCards(boardID)

	type boardWithCards struct {
		*board.Board
		Cards []*board.Card `json:"cards"`
	}
	writeJSON(w, http.StatusOK, boardWithCards{Board: b, Cards: cards})
}

// --- Card Handlers ---

func (s *Server) handleListCards(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("boardId")
	cards, err := s.store.ListCards(boardID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cards)
}

func (s *Server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("boardId")

	var req struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Priority    int      `json:"priority"`
		ColumnID    string   `json:"column_id"`
		Labels      []string `json:"labels"`
		DependsOn   []string `json:"depends_on"`
		Files       []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.ColumnID == "" {
		req.ColumnID = board.ColumnBacklog
	}

	card := &board.Card{
		ID:          board.GenerateCardID(),
		BoardID:     boardID,
		ColumnID:    req.ColumnID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Labels:      req.Labels,
		DependsOn:   req.DependsOn,
		Files:       req.Files,
		Status:      board.StatusForColumn(req.ColumnID),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := s.store.CreateCard(card); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Broadcast to WebSocket clients
	s.broadcastCardEvent("card.created", card)

	writeJSON(w, http.StatusCreated, card)
}

func (s *Server) handleGetCard(w http.ResponseWriter, r *http.Request) {
	cardID := r.PathValue("cardId")
	card, err := s.store.GetCard(cardID)
	if err != nil {
		writeError(w, http.StatusNotFound, "card not found")
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (s *Server) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	cardID := r.PathValue("cardId")
	card, err := s.store.GetCard(cardID)
	if err != nil {
		writeError(w, http.StatusNotFound, "card not found")
		return
	}

	var updates map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Apply updates
	if v, ok := updates["title"]; ok {
		json.Unmarshal(v, &card.Title)
	}
	if v, ok := updates["description"]; ok {
		json.Unmarshal(v, &card.Description)
	}
	if v, ok := updates["priority"]; ok {
		json.Unmarshal(v, &card.Priority)
	}
	if v, ok := updates["labels"]; ok {
		json.Unmarshal(v, &card.Labels)
	}
	if v, ok := updates["assignee_id"]; ok {
		json.Unmarshal(v, &card.AssigneeID)
	}
	if v, ok := updates["depends_on"]; ok {
		json.Unmarshal(v, &card.DependsOn)
	}
	if v, ok := updates["files"]; ok {
		json.Unmarshal(v, &card.Files)
	}
	card.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateCard(card); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.broadcastCardEvent("card.updated", card)
	writeJSON(w, http.StatusOK, card)
}

func (s *Server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	cardID := r.PathValue("cardId")
	if err := s.store.DeleteCard(cardID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type deletePayload struct {
		CardID string `json:"cardId"`
	}
	data, _ := json.Marshal(deletePayload{CardID: cardID})
	s.wsHub.Broadcast(&WSMessage{Type: "card.deleted", Data: data})

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMoveCard(w http.ResponseWriter, r *http.Request) {
	cardID := r.PathValue("cardId")

	var req struct {
		Column   string `json:"column"`
		Position int    `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	card, err := s.store.GetCard(cardID)
	if err != nil {
		writeError(w, http.StatusNotFound, "card not found")
		return
	}

	fromColumn := card.ColumnID
	if err := s.store.MoveCard(cardID, req.Column, req.Position); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Refresh card after move
	card, _ = s.store.GetCard(cardID)

	// Broadcast move event
	type movePayload struct {
		CardID     string `json:"cardId"`
		FromColumn string `json:"fromColumn"`
		ToColumn   string `json:"toColumn"`
		Position   int    `json:"position"`
	}
	data, _ := json.Marshal(movePayload{
		CardID:     cardID,
		FromColumn: fromColumn,
		ToColumn:   req.Column,
		Position:   req.Position,
	})
	s.wsHub.Broadcast(&WSMessage{Type: "card.moved", Data: data})

	// If moved to in_progress and has no assignee, this is where
	// the AgentPool integration would trigger auto-assignment.
	if req.Column == board.ColumnInProgress && card.AssigneeID == "" && s.pool != nil {
		slog.Info("[web] card moved to in_progress without assignee, awaiting assignment",
			"card", cardID)
	}

	writeJSON(w, http.StatusOK, card)
}

// broadcastCardEvent marshals a card and broadcasts it via WebSocket.
func (s *Server) broadcastCardEvent(eventType string, card *board.Card) {
	data, err := json.Marshal(card)
	if err != nil {
		return
	}
	s.wsHub.Broadcast(&WSMessage{Type: eventType, Data: data})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("[web] json encode", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
