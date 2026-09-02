package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"adp/internal/domain/model"
)

func reviewedCaseEmbeddingText(c model.IncidentCase) string {
	parts := []struct{ label, value string }{{"症状", c.AlertSymptoms}, {"环境", strings.Join(c.EnvironmentTags, ", ")}, {"证据摘要", c.EvidenceSummary}, {"已确认根因", c.RootCause}, {"已验证处置", strings.Join(c.ResolutionSteps, "；")}, {"处置结果", c.ResolutionResult}}
	var out []string
	for _, part := range parts {
		value := model.SanitizeText(strings.TrimSpace(part.value))
		if value == "" {
			continue
		}
		if len(value) > 1200 {
			value = value[:1200] + "…[truncated]"
		}
		out = append(out, part.label+"："+value)
	}
	return strings.Join(out, "\n")
}

func (s *Server) queueApprovedCaseEmbedding(c model.IncidentCase) error {
	if s.embeddings == nil || c.Status != model.IncidentCaseStatusApproved {
		return nil
	}
	text := reviewedCaseEmbeddingText(c)
	if text == "" {
		return nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	return s.repo.QueueIncidentCaseEmbedding(c.ID, hash, s.config.RAGEmbeddingModel, s.config.RAGEmbeddingDimensions)
}

func (s *Server) runEmbeddingQueue() {
	s.processEmbeddingQueue(context.Background())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.processEmbeddingQueue(context.Background())
	}
}

func (s *Server) processEmbeddingQueue(ctx context.Context) {
	if s.embeddings == nil {
		return
	}
	ids, err := s.repo.ListQueuedIncidentCaseEmbeddingIDs(10)
	if err != nil {
		return
	}
	for _, id := range ids {
		c, err := s.repo.GetIncidentCase(id)
		if err != nil || c.Status != model.IncidentCaseStatusApproved {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		started := time.Now()
		vector, err := s.embeddings.Embed(callCtx, reviewedCaseEmbeddingText(c))
		cancel()
		if err != nil {
			s.ragMetrics.complete(time.Since(started), true)
			_ = s.repo.FailIncidentCaseEmbedding(id, model.SanitizeText(err.Error()))
			continue
		}
		if err := s.repo.CompleteIncidentCaseEmbedding(id, s.config.RAGEmbeddingModel, formatVector(vector)); err != nil {
			s.ragMetrics.complete(time.Since(started), true)
			_ = s.repo.FailIncidentCaseEmbedding(id, model.SanitizeText(err.Error()))
			continue
		}
		s.ragMetrics.complete(time.Since(started), false)
	}
}

func formatVector(vector []float32) string {
	items := make([]string, len(vector))
	for i, value := range vector {
		items[i] = fmt.Sprintf("%g", value)
	}
	return "[" + strings.Join(items, ",") + "]"
}
