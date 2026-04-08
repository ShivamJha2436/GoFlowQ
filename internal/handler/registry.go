package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"goflowq/internal/queue"
)

// Handler is the function signature implemented by job executors.
type Handler func(context.Context, queue.Job) error

// Registry stores handlers by job type so workers can dispatch dynamically.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry creates an empty handler registry.
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]Handler),
	}
}

// Register adds or replaces the handler for a given job type.
func (r *Registry) Register(jobType string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[jobType] = handler
}

// Get looks up the handler for a job type.
func (r *Registry) Get(jobType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, ok := r.handlers[jobType]
	return handler, ok
}

// List returns registered handler names in stable order for API responses.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	jobTypes := make([]string, 0, len(r.handlers))
	for jobType := range r.handlers {
		jobTypes = append(jobTypes, jobType)
	}

	sort.Strings(jobTypes)
	return jobTypes
}

// RegisterBuiltins installs a few demo handlers so the system is runnable out of the box.
func RegisterBuiltins(registry *Registry) {
	registry.Register("echo", func(ctx context.Context, job queue.Job) error {
		// Echo is intentionally simple: it proves the enqueue/execute flow works.
		log.Printf("echo job=%s payload=%s", job.ID, string(job.Payload))
		return nil
	})

	registry.Register("sleep", func(ctx context.Context, job queue.Job) error {
		var payload struct {
			DurationMS int `json:"duration_ms"`
		}

		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return fmt.Errorf("decode sleep payload: %w", err)
			}
		}

		if payload.DurationMS <= 0 {
			payload.DurationMS = 500
		}

		// Use the worker-provided context so job timeouts cancel the sleep.
		timer := time.NewTimer(time.Duration(payload.DurationMS) * time.Millisecond)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})

	registry.Register("flaky", func(ctx context.Context, job queue.Job) error {
		var payload struct {
			FailUntilAttempt int    `json:"fail_until_attempt"`
			Message          string `json:"message"`
		}

		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return fmt.Errorf("decode flaky payload: %w", err)
			}
		}

		if job.Attempt <= payload.FailUntilAttempt {
			// This handler exists to exercise retries and the dead-letter queue.
			return fmt.Errorf("simulated failure for attempt %d", job.Attempt)
		}

		log.Printf("flaky job=%s recovered message=%q", job.ID, payload.Message)
		return nil
	})
}
