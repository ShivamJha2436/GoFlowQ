package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"goflowq/internal/handler"
	"goflowq/internal/queue"
)

// Server exposes enqueue and inspection endpoints over HTTP.
type Server struct {
	queue    *queue.RedisQueue
	registry *handler.Registry
}

// NewServer wires the API to the queue and mirrors the built-in handlers for discovery.
func NewServer(queue *queue.RedisQueue) *Server {
	registry := handler.NewRegistry()
	handler.RegisterBuiltins(registry)

	return &Server{
		queue:    queue,
		registry: registry,
	}
}

// Routes returns the complete HTTP surface for producers and operators.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /handlers", s.handleHandlers)
	mux.HandleFunc("GET /dashboard/overview", s.handleDashboardOverview)
	mux.HandleFunc("POST /queues/scheduled/{id}/promote", s.handlePromoteScheduled)
	mux.HandleFunc("POST /queues/dead/{id}/retry", s.handleRetryDead)
	mux.HandleFunc("DELETE /queues/dead/{id}", s.handleDeleteDead)
	mux.HandleFunc("POST /jobs", s.handleEnqueue)
	mux.HandleFunc("GET /queues/stats", s.handleStats)
	mux.HandleFunc("GET /queues/dead", s.handleDead)
	mux.HandleFunc("GET /queues/scheduled", s.handleScheduled)
	mux.Handle("/", s.dashboardAppHandler())

	return mux
}

// enqueueRequest is the JSON payload accepted by POST /jobs.
type enqueueRequest struct {
	Type           string          `json:"type"`
	Priority       string          `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	MaxRetries     int             `json:"max_retries"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

// handleHealth verifies that Redis is reachable.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded",
			"error":  err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleHandlers returns registered job types so callers know what can be enqueued.
func (s *Server) handleHandlers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"handlers": s.registry.List(),
	})
}

// handleDashboardOverview returns the main dashboard payload in a single response.
func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	stats, err := s.queue.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	scheduled, err := s.queue.ScheduledJobs(r.Context(), getLimit(r, 25))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dead, err := s.queue.DeadJobs(r.Context(), getLimit(r, 25))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	activity, err := s.queue.Activity(r.Context(), getLimit(r, 25))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC(),
		"handlers":     s.registry.List(),
		"stats":        stats,
		"scheduled":    scheduled,
		"dead":         dead,
		"activity":     activity,
	})
}

// handleEnqueue validates the request and pushes a new job into the ready queue.
func (s *Server) handleEnqueue(w http.ResponseWriter, r *http.Request) {
	var req enqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	priority, err := queue.ParsePriority(req.Priority)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, err := s.queue.Enqueue(r.Context(), queue.EnqueueParams{
		Type:           req.Type,
		Priority:       priority,
		Payload:        req.Payload,
		MaxRetries:     req.MaxRetries,
		TimeoutSeconds: req.TimeoutSeconds,
	})
	if err != nil {
		// Validation errors are client mistakes; Redis failures are server-side.
		status := http.StatusInternalServerError
		if errors.Is(err, queue.ErrInvalidJob) {
			status = http.StatusBadRequest
		}

		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "queued",
		"job":    job,
	})
}

// handleStats reports queue sizes across ready, processing, scheduled, and dead states.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.queue.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// handleDead lists jobs that exhausted retries and were dead-lettered.
func (s *Server) handleDead(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.queue.DeadJobs(r.Context(), getLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handleScheduled lists jobs waiting for their retry window.
func (s *Server) handleScheduled(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.queue.ScheduledJobs(r.Context(), getLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handlePromoteScheduled moves a delayed retry back into the ready queue immediately.
func (s *Server) handlePromoteScheduled(w http.ResponseWriter, r *http.Request) {
	job, err := s.queue.PromoteScheduledJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeQueueActionError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "promoted",
		"job":    job,
	})
}

// handleRetryDead requeues a dead-lettered job and resets its retry lifecycle.
func (s *Server) handleRetryDead(w http.ResponseWriter, r *http.Request) {
	job, err := s.queue.RetryDeadJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeQueueActionError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "retried",
		"job":    job,
	})
}

// handleDeleteDead permanently removes a dead-lettered job.
func (s *Server) handleDeleteDead(w http.ResponseWriter, r *http.Request) {
	job, err := s.queue.DeleteDeadJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeQueueActionError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deleted",
		"job":    job,
	})
}

// getLimit parses the optional list limit query parameter.
func getLimit(r *http.Request, fallback int64) int64 {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}

	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return fallback
	}

	return limit
}

// writeError standardizes error responses as JSON.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeQueueActionError maps queue action failures to consistent HTTP responses.
func writeQueueActionError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, queue.ErrJobNotFound) || errors.Is(err, queue.ErrInvalidJob) {
		status = http.StatusNotFound
	}

	writeError(w, status, err.Error())
}

// writeJSON is the common response writer for all API endpoints.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
