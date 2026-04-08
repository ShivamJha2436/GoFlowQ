package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"goflowq/internal/config"
	"goflowq/internal/handler"
	"goflowq/internal/queue"
	"goflowq/internal/worker"

	"github.com/redis/go-redis/v9"
)

// main boots the worker pool process, which is responsible for executing jobs.
func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer client.Close()

	// Workers share the same Redis queue implementation as the API.
	q := queue.NewRedisQueue(client, cfg.QueuePrefix, cfg.DefaultJobTimeout)
	registry := handler.NewRegistry()
	handler.RegisterBuiltins(registry)

	// The pool owns both job execution and promotion of scheduled retries.
	pool := worker.NewPool(worker.Options{
		Queue:             q,
		Registry:          registry,
		Concurrency:       cfg.WorkerConcurrency,
		PollInterval:      cfg.PollInterval,
		RetryBaseDelay:    cfg.RetryBaseDelay,
		SchedulerInterval: cfg.SchedulerInterval,
		SchedulerBatch:    cfg.SchedulerBatch,
	})

	log.Printf("worker pool starting with concurrency=%d", cfg.WorkerConcurrency)
	if err := pool.Start(ctx); err != nil {
		log.Fatalf("worker pool failed: %v", err)
	}
}
