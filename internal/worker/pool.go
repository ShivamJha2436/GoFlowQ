package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"goflowq/internal/handler"
	"goflowq/internal/queue"
)

// Options controls how the pool polls, retries, and promotes scheduled jobs.
type Options struct {
	Queue             *queue.RedisQueue
	Registry          *handler.Registry
	Concurrency       int
	PollInterval      time.Duration
	RetryBaseDelay    time.Duration
	SchedulerInterval time.Duration
	SchedulerBatch    int
}

// Pool owns a set of worker goroutines plus a retry scheduler loop.
type Pool struct {
	queue             *queue.RedisQueue
	registry          *handler.Registry
	concurrency       int
	pollInterval      time.Duration
	retryBaseDelay    time.Duration
	schedulerInterval time.Duration
	schedulerBatch    int64
}

// NewPool normalizes options and constructs a ready-to-run worker pool.
func NewPool(options Options) *Pool {
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}

	retryBaseDelay := options.RetryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = 2 * time.Second
	}

	schedulerInterval := options.SchedulerInterval
	if schedulerInterval <= 0 {
		schedulerInterval = time.Second
	}

	schedulerBatch := int64(options.SchedulerBatch)
	if schedulerBatch <= 0 {
		schedulerBatch = 100
	}

	return &Pool{
		queue:             options.Queue,
		registry:          options.Registry,
		concurrency:       concurrency,
		pollInterval:      pollInterval,
		retryBaseDelay:    retryBaseDelay,
		schedulerInterval: schedulerInterval,
		schedulerBatch:    schedulerBatch,
	}
}

// Start launches the scheduler and all worker goroutines, then blocks until shutdown.
func (p *Pool) Start(ctx context.Context) error {
	if p.queue == nil {
		return errors.New("worker pool requires a queue")
	}
	if p.registry == nil {
		return errors.New("worker pool requires a handler registry")
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		// The scheduler is separate so retries keep flowing even when workers are idle.
		p.schedulerLoop(ctx)
	}()

	for workerID := 1; workerID <= p.concurrency; workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.workerLoop(ctx, id)
		}(workerID)
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

// workerLoop continuously pulls jobs, executes them, and acks or retries them.
func (p *Pool) workerLoop(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, raw, priority, err := p.queue.Dequeue(ctx)
		if errors.Is(err, queue.ErrNoJobs) {
			time.Sleep(p.pollInterval)
			continue
		}
		if err != nil {
			log.Printf("worker=%d dequeue error: %v", workerID, err)
			time.Sleep(p.pollInterval)
			continue
		}

		// Attempts are incremented at execution time so handlers can see the current try number.
		job.Attempt++
		if err := p.queue.RecordActivity(ctx, queue.ActivityEvent{
			Kind:     "job_started",
			Message:  fmt.Sprintf("Worker %d started %s", workerID, job.Type),
			JobID:    job.ID,
			JobType:  job.Type,
			Priority: job.Priority,
			Attempt:  job.Attempt,
			WorkerID: workerID,
		}); err != nil {
			log.Printf("worker=%d activity write failed job=%s: %v", workerID, job.ID, err)
		}

		handlerFunc, ok := p.registry.Get(job.Type)
		if !ok {
			p.failJob(ctx, workerID, job, priority, raw, fmt.Errorf("unknown handler %q", job.Type))
			continue
		}

		// Each job runs with its own timeout derived from the enqueued job record.
		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		err = handlerFunc(runCtx, job)
		cancel()

		if err != nil {
			p.failJob(ctx, workerID, job, priority, raw, err)
			continue
		}

		if err := p.queue.Ack(ctx, priority, raw); err != nil {
			log.Printf("worker=%d ack failed job=%s: %v", workerID, job.ID, err)
			continue
		}

		if err := p.queue.RecordActivity(ctx, queue.ActivityEvent{
			Kind:     "job_completed",
			Message:  fmt.Sprintf("Worker %d completed %s", workerID, job.Type),
			JobID:    job.ID,
			JobType:  job.Type,
			Priority: job.Priority,
			Attempt:  job.Attempt,
			WorkerID: workerID,
		}); err != nil {
			log.Printf("worker=%d activity write failed job=%s: %v", workerID, job.ID, err)
		}

		log.Printf(
			"worker=%d completed job=%s type=%s priority=%s attempt=%d",
			workerID,
			job.ID,
			job.Type,
			job.Priority,
			job.Attempt,
		)
	}
}

// failJob sends an unsuccessful job to the retry schedule or the dead-letter queue.
func (p *Pool) failJob(ctx context.Context, workerID int, job queue.Job, priority queue.Priority, raw string, failure error) {
	delay := retryDelay(job.Attempt, p.retryBaseDelay)
	if err := p.queue.RetryOrDead(ctx, job, priority, raw, failure, delay); err != nil {
		log.Printf("worker=%d failed to move job=%s after error=%v queueError=%v", workerID, job.ID, failure, err)
		return
	}

	if job.Attempt <= job.MaxRetries {
		if err := p.queue.RecordActivity(ctx, queue.ActivityEvent{
			Kind:     "job_retried",
			Message:  fmt.Sprintf("Worker %d scheduled retry for %s in %s", workerID, job.Type, delay),
			JobID:    job.ID,
			JobType:  job.Type,
			Priority: job.Priority,
			Attempt:  job.Attempt,
			WorkerID: workerID,
		}); err != nil {
			log.Printf("worker=%d activity write failed job=%s: %v", workerID, job.ID, err)
		}

		log.Printf(
			"worker=%d retrying job=%s type=%s attempt=%d/%d in=%s error=%v",
			workerID,
			job.ID,
			job.Type,
			job.Attempt,
			job.MaxRetries+1,
			delay,
			failure,
		)
		return
	}

	if err := p.queue.RecordActivity(ctx, queue.ActivityEvent{
		Kind:     "job_dead_lettered",
		Message:  fmt.Sprintf("Worker %d dead-lettered %s after error: %v", workerID, job.Type, failure),
		JobID:    job.ID,
		JobType:  job.Type,
		Priority: job.Priority,
		Attempt:  job.Attempt,
		WorkerID: workerID,
	}); err != nil {
		log.Printf("worker=%d activity write failed job=%s: %v", workerID, job.ID, err)
	}

	log.Printf(
		"worker=%d moved job=%s type=%s to dead-letter queue after attempt=%d error=%v",
		workerID,
		job.ID,
		job.Type,
		job.Attempt,
		failure,
	)
}

// schedulerLoop periodically promotes due retries back into the ready queues.
func (p *Pool) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(p.schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			moved, err := p.queue.MoveDueScheduled(ctx, p.schedulerBatch)
			if err != nil {
				log.Printf("scheduler move error: %v", err)
				continue
			}

			if moved > 0 {
				if err := p.queue.RecordActivity(ctx, queue.ActivityEvent{
					Kind:    "scheduled_promoted_batch",
					Message: fmt.Sprintf("Scheduler promoted %d retryable jobs", moved),
				}); err != nil {
					log.Printf("scheduler activity write failed: %v", err)
				}

				log.Printf("scheduler promoted %d retriable jobs", moved)
			}
		}
	}
}

// retryDelay applies exponential backoff with a hard one-minute ceiling.
func retryDelay(attempt int, base time.Duration) time.Duration {
	if attempt <= 0 {
		return base
	}

	multiplier := math.Pow(2, float64(attempt-1))
	delay := time.Duration(multiplier) * base
	maxDelay := time.Minute
	if delay > maxDelay {
		return maxDelay
	}

	return delay
}
