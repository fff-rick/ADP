package api

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	convs, err := s.repo.ListConversations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, convs)
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	_ = decodeJSON(r, &req)
	conv, err := s.repo.CreateConversation(strings.TrimSpace(req.Title))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit("user", currentUser(r).Username, "conversation.created", "conversation", conv.ID, nil)
	writeJSON(w, http.StatusCreated, conv)
}

func (s *Server) handleConversationActions(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/conversations/")
	id = strings.TrimSuffix(id, "/messages")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("conversation id is required"))
		return
	}

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
		msgs, err := s.repo.ListConversationMessages(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, msgs)
	case r.Method == http.MethodGet:
		conv, err := s.repo.GetConversation(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		msgs, err := s.repo.ListConversationMessages(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversation": conv, "messages": msgs})
	case r.Method == http.MethodDelete:
		if err := s.repo.DeleteConversation(id); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		s.recordAudit("user", currentUser(r).Username, "conversation.deleted", "conversation", id, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, errors.New("unsupported method"))
	}
}
