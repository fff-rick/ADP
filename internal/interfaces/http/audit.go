package api

import (
	"net/http"
	"time"

	"adp/internal/domain/model"
)

func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	resourceType := r.URL.Query().Get("resource_type")
	resourceID := r.URL.Query().Get("resource_id")

	if s.repo != nil {
		logs, err := s.repo.ListAuditLogs(resourceType, resourceID)
		if err == nil {
			writeJSON(w, http.StatusOK, logs)
			return
		}
	}
	writeJSON(w, http.StatusOK, []model.AuditLog{})
}

func (s *Server) recordAudit(actorType, actorID, action, resourceType, resourceID string, details map[string]any) {
	entry := model.AuditLog{
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      model.SanitizeMap(details),
		CreatedAt:    time.Now(),
	}
	if s.repo != nil {
		_ = s.repo.AddAuditLog(entry)
	}
	fields := map[string]any{
		"actor_id":      actorID,
		"actor_type":    actorType,
		"resource_id":   resourceID,
		"resource_type": resourceType,
	}
	for key, value := range entry.Details {
		fields[key] = value
	}
	logEvent("audit", action, fields)
}
