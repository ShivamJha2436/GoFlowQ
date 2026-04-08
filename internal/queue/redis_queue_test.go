package queue

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestParsePriority covers the accepted producer-facing priority values.
func TestParsePriority(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Priority
		wantErr bool
	}{
		{name: "default empty", input: "", want: PriorityDefault},
		{name: "high", input: "high", want: PriorityHigh},
		{name: "low", input: "low", want: PriorityLow},
		{name: "bad", input: "urgent", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePriority(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// TestNewJobDefaults verifies server-side defaults applied during enqueue.
func TestNewJobDefaults(t *testing.T) {
	q := &RedisQueue{
		defaultJobTimeout: 15 * time.Second,
		now: func() time.Time {
			return time.Date(2026, time.April, 8, 12, 0, 0, 0, time.UTC)
		},
	}

	job, err := q.newJob(EnqueueParams{
		Type:       "echo",
		Priority:   PriorityDefault,
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if job.TimeoutSeconds != 15 {
		t.Fatalf("got timeout=%d want=15", job.TimeoutSeconds)
	}
	if job.Attempt != 0 {
		t.Fatalf("got attempt=%d want=0", job.Attempt)
	}
	if string(job.Payload) != "{}" {
		t.Fatalf("got payload=%s want {}", string(job.Payload))
	}
	if job.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be populated")
	}
}

// TestDecodeJobs makes sure Redis payloads round-trip into typed jobs.
func TestDecodeJobs(t *testing.T) {
	raw, err := json.Marshal(Job{ID: "abc", Type: "echo", Priority: PriorityHigh})
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}

	jobs, err := decodeJobs([]string{string(raw)})
	if err != nil {
		t.Fatalf("decodeJobs error: %v", err)
	}

	if len(jobs) != 1 || jobs[0].ID != "abc" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
}

// TestFindJobByID verifies the helper used by dashboard action endpoints.
func TestFindJobByID(t *testing.T) {
	first, err := json.Marshal(Job{ID: "abc", Type: "echo", Priority: PriorityHigh})
	if err != nil {
		t.Fatalf("marshal first job: %v", err)
	}

	second, err := json.Marshal(Job{ID: "def", Type: "sleep", Priority: PriorityLow})
	if err != nil {
		t.Fatalf("marshal second job: %v", err)
	}

	job, raw, err := findJobByID([]string{string(first), string(second)}, "def")
	if err != nil {
		t.Fatalf("findJobByID error: %v", err)
	}

	if job.ID != "def" {
		t.Fatalf("got job id=%s want def", job.ID)
	}
	if raw != string(second) {
		t.Fatalf("got raw=%q want %q", raw, string(second))
	}
}

// TestFindJobByIDNotFound documents the error returned for missing dashboard actions.
func TestFindJobByIDNotFound(t *testing.T) {
	_, _, err := findJobByID(nil, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("got error=%v want ErrJobNotFound", err)
	}
}
