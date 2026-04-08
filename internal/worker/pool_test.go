package worker

import (
	"testing"
	"time"
)

// TestRetryDelay documents the exponential backoff curve used by failed jobs.
func TestRetryDelay(t *testing.T) {
	base := 2 * time.Second

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 2 * time.Second},
		{attempt: 1, want: 2 * time.Second},
		{attempt: 2, want: 4 * time.Second},
		{attempt: 3, want: 8 * time.Second},
		{attempt: 10, want: time.Minute},
	}

	for _, tt := range tests {
		got := retryDelay(tt.attempt, base)
		if got != tt.want {
			t.Fatalf("attempt=%d got=%s want=%s", tt.attempt, got, tt.want)
		}
	}
}
