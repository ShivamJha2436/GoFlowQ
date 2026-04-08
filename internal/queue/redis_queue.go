package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrInvalidJob is returned when enqueue parameters fail validation.
	ErrInvalidJob = errors.New("invalid job")
	// ErrNoJobs is returned when all priority queues are currently empty.
	ErrNoJobs = errors.New("no jobs available")
	// ErrJobNotFound is returned when a job cannot be found in the requested queue state.
	ErrJobNotFound = errors.New("job not found")
)

// Priority controls which ready queue a job is pushed into.
type Priority string

const (
	PriorityHigh    Priority = "high"
	PriorityDefault Priority = "default"
	PriorityLow     Priority = "low"
)

var priorities = []Priority{PriorityHigh, PriorityDefault, PriorityLow}

const activityHistoryLimit = 200

// Job is the persisted unit of work stored in Redis.
type Job struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Priority       Priority        `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	Attempt        int             `json:"attempt"`
	MaxRetries     int             `json:"max_retries"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	CreatedAt      time.Time       `json:"created_at"`
	AvailableAt    *time.Time      `json:"available_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	FailedAt       *time.Time      `json:"failed_at,omitempty"`
}

// ActivityEvent is a compact audit record for dashboard history.
type ActivityEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	JobID     string    `json:"job_id,omitempty"`
	JobType   string    `json:"job_type,omitempty"`
	Priority  Priority  `json:"priority,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	WorkerID  int       `json:"worker_id,omitempty"`
}

// EnqueueParams contains the producer-supplied fields for a new job.
type EnqueueParams struct {
	Type           string
	Priority       Priority
	Payload        json.RawMessage
	MaxRetries     int
	TimeoutSeconds int
}

// ScheduledJob is a job paired with its retry execution time.
type ScheduledJob struct {
	Job     Job       `json:"job"`
	RunAt   time.Time `json:"run_at"`
	RunUnix int64     `json:"run_unix"`
}

// Stats captures queue counts for observability endpoints.
type Stats struct {
	Ready      map[Priority]int64 `json:"ready"`
	Processing map[Priority]int64 `json:"processing"`
	Scheduled  int64              `json:"scheduled"`
	Dead       int64              `json:"dead"`
}

// RedisQueue manages job state transitions across ready, processing, scheduled, and dead keys.
type RedisQueue struct {
	client            *redis.Client
	prefix            string
	defaultJobTimeout time.Duration
	now               func() time.Time
}

// NewRedisQueue builds a Redis-backed queue with a configurable key prefix.
func NewRedisQueue(client *redis.Client, prefix string, defaultJobTimeout time.Duration) *RedisQueue {
	return &RedisQueue{
		client:            client,
		prefix:            prefix,
		defaultJobTimeout: defaultJobTimeout,
		now:               time.Now,
	}
}

// ParsePriority normalizes and validates incoming priority strings.
func ParsePriority(raw string) (Priority, error) {
	switch Priority(raw) {
	case "", PriorityDefault:
		return PriorityDefault, nil
	case PriorityHigh:
		return PriorityHigh, nil
	case PriorityLow:
		return PriorityLow, nil
	default:
		return "", fmt.Errorf("%w: priority must be high, default, or low", ErrInvalidJob)
	}
}

// Ping lets the API expose a simple queue health check.
func (q *RedisQueue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

// Enqueue validates a new job and pushes it into the ready list for its priority.
func (q *RedisQueue) Enqueue(ctx context.Context, params EnqueueParams) (Job, error) {
	job, err := q.newJob(params)
	if err != nil {
		return Job{}, err
	}

	if err := q.pushReady(ctx, job); err != nil {
		return Job{}, err
	}

	_ = q.RecordActivity(ctx, ActivityEvent{
		Timestamp: q.now().UTC(),
		Kind:      "job_enqueued",
		Message:   fmt.Sprintf("Queued %s job at %s priority", job.Type, job.Priority),
		JobID:     job.ID,
		JobType:   job.Type,
		Priority:  job.Priority,
	})

	return job, nil
}

// newJob constructs the canonical job record stored in Redis.
func (q *RedisQueue) newJob(params EnqueueParams) (Job, error) {
	if params.Type == "" {
		return Job{}, fmt.Errorf("%w: type is required", ErrInvalidJob)
	}

	priority, err := ParsePriority(string(params.Priority))
	if err != nil {
		return Job{}, err
	}

	if params.MaxRetries < 0 {
		return Job{}, fmt.Errorf("%w: max_retries must be >= 0", ErrInvalidJob)
	}

	timeout := params.TimeoutSeconds
	if timeout <= 0 {
		timeout = int(q.defaultJobTimeout.Seconds())
		if timeout <= 0 {
			timeout = 10
		}
	}

	payload := params.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	return Job{
		ID:             newID(),
		Type:           params.Type,
		Priority:       priority,
		Payload:        payload,
		Attempt:        0,
		MaxRetries:     params.MaxRetries,
		TimeoutSeconds: timeout,
		CreatedAt:      q.now().UTC(),
	}, nil
}

// Dequeue pulls the next available job, checking higher priorities first.
func (q *RedisQueue) Dequeue(ctx context.Context) (Job, string, Priority, error) {
	for _, priority := range priorities {
		// Move the payload atomically so a job is marked in-flight before a worker sees it.
		raw, err := q.client.LMove(ctx, q.readyKey(priority), q.processingKey(priority), "RIGHT", "LEFT").Result()
		if err == redis.Nil {
			continue
		}

		if err != nil {
			return Job{}, "", "", err
		}

		var job Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			// If the payload is corrupt, drop it from processing so it does not poison the queue.
			_ = q.client.LRem(ctx, q.processingKey(priority), 1, raw).Err()
			return Job{}, "", "", fmt.Errorf("decode job payload: %w", err)
		}

		return job, raw, priority, nil
	}

	return Job{}, "", "", ErrNoJobs
}

// Ack removes a successfully handled job from the processing list.
func (q *RedisQueue) Ack(ctx context.Context, priority Priority, raw string) error {
	return q.client.LRem(ctx, q.processingKey(priority), 1, raw).Err()
}

// RetryOrDead removes a failed in-flight job and routes it either to retry or to the DLQ.
func (q *RedisQueue) RetryOrDead(ctx context.Context, job Job, priority Priority, raw string, failure error, delay time.Duration) error {
	if failure != nil {
		job.LastError = failure.Error()
	}

	if err := q.Ack(ctx, priority, raw); err != nil {
		return err
	}

	if job.Attempt <= job.MaxRetries {
		// Retries are time-based, so they live in a sorted set until the scheduler promotes them.
		runAt := q.now().UTC().Add(delay)
		job.AvailableAt = &runAt
		job.FailedAt = nil
		return q.schedule(ctx, job, runAt)
	}

	failedAt := q.now().UTC()
	job.AvailableAt = nil
	job.FailedAt = &failedAt
	return q.pushDead(ctx, job)
}

// MoveDueScheduled promotes any retry jobs whose scheduled time has arrived.
func (q *RedisQueue) MoveDueScheduled(ctx context.Context, limit int64) (int, error) {
	if limit <= 0 {
		limit = 100
	}

	nowUnix := q.now().Unix()
	rawJobs, err := q.client.ZRangeByScore(ctx, q.scheduledKey(), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(nowUnix, 10),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return 0, err
	}

	moved := 0
	for _, raw := range rawJobs {
		var job Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			// Bad retry payloads are discarded from the schedule to keep promotion unblocked.
			if removeErr := q.client.ZRem(ctx, q.scheduledKey(), raw).Err(); removeErr != nil {
				return moved, removeErr
			}
			continue
		}

		if err := q.client.ZRem(ctx, q.scheduledKey(), raw).Err(); err != nil {
			return moved, err
		}

		// Once promoted, the job looks like any other ready job again.
		job.AvailableAt = nil
		if err := q.pushReady(ctx, job); err != nil {
			return moved, err
		}

		moved++
	}

	return moved, nil
}

// Stats returns queue lengths for each lifecycle bucket.
func (q *RedisQueue) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{
		Ready:      make(map[Priority]int64, len(priorities)),
		Processing: make(map[Priority]int64, len(priorities)),
	}

	for _, priority := range priorities {
		readyCount, err := q.client.LLen(ctx, q.readyKey(priority)).Result()
		if err != nil {
			return Stats{}, err
		}

		processingCount, err := q.client.LLen(ctx, q.processingKey(priority)).Result()
		if err != nil {
			return Stats{}, err
		}

		stats.Ready[priority] = readyCount
		stats.Processing[priority] = processingCount
	}

	scheduled, err := q.client.ZCard(ctx, q.scheduledKey()).Result()
	if err != nil {
		return Stats{}, err
	}

	dead, err := q.client.LLen(ctx, q.deadKey()).Result()
	if err != nil {
		return Stats{}, err
	}

	stats.Scheduled = scheduled
	stats.Dead = dead
	return stats, nil
}

// DeadJobs returns the most recent dead-lettered jobs.
func (q *RedisQueue) DeadJobs(ctx context.Context, limit int64) ([]Job, error) {
	rawJobs, err := q.client.LRange(ctx, q.deadKey(), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}

	return decodeJobs(rawJobs)
}

// ScheduledJobs returns retry jobs with their scheduled execution times.
func (q *RedisQueue) ScheduledJobs(ctx context.Context, limit int64) ([]ScheduledJob, error) {
	items, err := q.client.ZRangeWithScores(ctx, q.scheduledKey(), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}

	jobs := make([]ScheduledJob, 0, len(items))
	for _, item := range items {
		raw, ok := item.Member.(string)
		if !ok {
			continue
		}

		var job Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			continue
		}

		runUnix := int64(item.Score)
		jobs = append(jobs, ScheduledJob{
			Job:     job,
			RunAt:   time.Unix(runUnix, 0).UTC(),
			RunUnix: runUnix,
		})
	}

	return jobs, nil
}

// Activity returns the newest activity events first.
func (q *RedisQueue) Activity(ctx context.Context, limit int64) ([]ActivityEvent, error) {
	rawEvents, err := q.client.LRange(ctx, q.activityKey(), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}

	events := make([]ActivityEvent, 0, len(rawEvents))
	for _, raw := range rawEvents {
		var event ActivityEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, err
		}

		events = append(events, event)
	}

	return events, nil
}

// RecordActivity appends an event to the capped dashboard activity history.
func (q *RedisQueue) RecordActivity(ctx context.Context, event ActivityEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = q.now().UTC()
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	pipe := q.client.TxPipeline()
	pipe.LPush(ctx, q.activityKey(), payload)
	pipe.LTrim(ctx, q.activityKey(), 0, activityHistoryLimit-1)
	_, err = pipe.Exec(ctx)
	return err
}

// PromoteScheduledJob removes a scheduled retry and pushes it back into the ready queue immediately.
func (q *RedisQueue) PromoteScheduledJob(ctx context.Context, jobID string) (Job, error) {
	job, raw, err := q.findScheduledJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}

	if err := q.client.ZRem(ctx, q.scheduledKey(), raw).Err(); err != nil {
		return Job{}, err
	}

	job.AvailableAt = nil
	if err := q.pushReady(ctx, job); err != nil {
		return Job{}, err
	}

	_ = q.RecordActivity(ctx, ActivityEvent{
		Kind:     "scheduled_promoted",
		Message:  fmt.Sprintf("Promoted scheduled retry for %s", job.Type),
		JobID:    job.ID,
		JobType:  job.Type,
		Priority: job.Priority,
		Attempt:  job.Attempt,
	})

	return job, nil
}

// RetryDeadJob requeues a dead-lettered job and resets its retry state.
func (q *RedisQueue) RetryDeadJob(ctx context.Context, jobID string) (Job, error) {
	job, raw, err := q.findDeadJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}

	if err := q.client.LRem(ctx, q.deadKey(), 1, raw).Err(); err != nil {
		return Job{}, err
	}

	job.Attempt = 0
	job.AvailableAt = nil
	job.FailedAt = nil
	job.LastError = ""
	if err := q.pushReady(ctx, job); err != nil {
		return Job{}, err
	}

	_ = q.RecordActivity(ctx, ActivityEvent{
		Kind:     "dead_job_retried",
		Message:  fmt.Sprintf("Retried dead-lettered %s job", job.Type),
		JobID:    job.ID,
		JobType:  job.Type,
		Priority: job.Priority,
	})

	return job, nil
}

// DeleteDeadJob permanently removes a job from the dead-letter queue.
func (q *RedisQueue) DeleteDeadJob(ctx context.Context, jobID string) (Job, error) {
	job, raw, err := q.findDeadJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}

	if err := q.client.LRem(ctx, q.deadKey(), 1, raw).Err(); err != nil {
		return Job{}, err
	}

	_ = q.RecordActivity(ctx, ActivityEvent{
		Kind:     "dead_job_deleted",
		Message:  fmt.Sprintf("Deleted dead-lettered %s job", job.Type),
		JobID:    job.ID,
		JobType:  job.Type,
		Priority: job.Priority,
		Attempt:  job.Attempt,
	})

	return job, nil
}

// pushReady serializes a job and prepends it to its ready list.
func (q *RedisQueue) pushReady(ctx context.Context, job Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return q.client.LPush(ctx, q.readyKey(job.Priority), payload).Err()
}

// schedule stores a retryable job in the sorted set keyed by its next run time.
func (q *RedisQueue) schedule(ctx context.Context, job Job, runAt time.Time) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return q.client.ZAdd(ctx, q.scheduledKey(), redis.Z{
		Score:  float64(runAt.Unix()),
		Member: string(payload),
	}).Err()
}

// pushDead appends a permanently failed job to the dead-letter queue.
func (q *RedisQueue) pushDead(ctx context.Context, job Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return q.client.LPush(ctx, q.deadKey(), payload).Err()
}

// readyKey returns the Redis list for jobs waiting to run at a given priority.
func (q *RedisQueue) readyKey(priority Priority) string {
	return fmt.Sprintf("%s:queue:%s", q.prefix, priority)
}

// processingKey returns the Redis list for jobs currently owned by workers.
func (q *RedisQueue) processingKey(priority Priority) string {
	return fmt.Sprintf("%s:processing:%s", q.prefix, priority)
}

// scheduledKey returns the Redis sorted set for delayed retries.
func (q *RedisQueue) scheduledKey() string {
	return fmt.Sprintf("%s:scheduled", q.prefix)
}

// deadKey returns the Redis list for exhausted jobs.
func (q *RedisQueue) deadKey() string {
	return fmt.Sprintf("%s:dead", q.prefix)
}

// activityKey returns the Redis list that backs the dashboard activity feed.
func (q *RedisQueue) activityKey() string {
	return fmt.Sprintf("%s:activity", q.prefix)
}

// decodeJobs unmarshals a Redis list payload into typed jobs.
func decodeJobs(rawJobs []string) ([]Job, error) {
	jobs := make([]Job, 0, len(rawJobs))
	for _, raw := range rawJobs {
		var job Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return nil, err
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// findDeadJob scans the dead-letter list for a matching job ID.
func (q *RedisQueue) findDeadJob(ctx context.Context, jobID string) (Job, string, error) {
	rawJobs, err := q.client.LRange(ctx, q.deadKey(), 0, -1).Result()
	if err != nil {
		return Job{}, "", err
	}

	return findJobByID(rawJobs, jobID)
}

// findScheduledJob scans the scheduled retry set for a matching job ID.
func (q *RedisQueue) findScheduledJob(ctx context.Context, jobID string) (Job, string, error) {
	rawJobs, err := q.client.ZRange(ctx, q.scheduledKey(), 0, -1).Result()
	if err != nil {
		return Job{}, "", err
	}

	return findJobByID(rawJobs, jobID)
}

// findJobByID returns the decoded job and its raw Redis payload for removal/update operations.
func findJobByID(rawJobs []string, jobID string) (Job, string, error) {
	for _, raw := range rawJobs {
		var job Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			continue
		}

		if job.ID == jobID {
			return job, raw, nil
		}
	}

	return Job{}, "", fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
}

// newID generates a compact random job identifier.
func newID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}

	return hex.EncodeToString(buf)
}
