package web

import (
	"encoding/json"
	"net/http"

	"github.com/mingzhi1/coden/internal/core/board"
)

// --- Agent Handlers ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []*board.Agent{})
		return
	}
	agents := s.pool.ListAgents()
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent pool not available")
		return
	}

	var req struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Role == "" {
		req.Role = "fullstack"
	}

	agent, err := s.pool.SpawnAgent(req.Name, req.Role, req.Provider, req.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Broadcast agent creation
	data, _ := json.Marshal(agent)
	s.wsHub.Broadcast(&WSMessage{Type: "agent.status", Data: data})

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent pool not available")
		return
	}
	agentID := r.PathValue("agentId")
	agent, err := s.pool.GetAgent(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent pool not available")
		return
	}
	// For now, agent updates are handled through the pool
	writeError(w, http.StatusNotImplemented, "agent update not yet implemented")
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent pool not available")
		return
	}
	agentID := r.PathValue("agentId")
	if err := s.pool.StopAgent(agentID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type deletePayload struct {
		AgentID string `json:"agentId"`
	}
	data, _ := json.Marshal(deletePayload{AgentID: agentID})
	s.wsHub.Broadcast(&WSMessage{Type: "agent.deleted", Data: data})

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAssignCard(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent pool not available")
		return
	}
	agentID := r.PathValue("agentId")

	var req struct {
		CardID string `json:"card_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.CardID == "" {
		writeError(w, http.StatusBadRequest, "card_id is required")
		return
	}

	if err := s.pool.AssignCard(agentID, req.CardID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Broadcast agent status update
	agent, _ := s.pool.GetAgent(agentID)
	if agent != nil {
		data, _ := json.Marshal(agent)
		s.wsHub.Broadcast(&WSMessage{Type: "agent.status", Data: data})
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "assigned",
		"agent_id": agentID,
		"card_id":  req.CardID,
	})
}
