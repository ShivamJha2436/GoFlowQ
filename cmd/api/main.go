package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"goflowq/internal/config"
	"goflowq/internal/httpapi"
	"goflowq/internal/queue"

	"github.com/redis/go-redis/v9"
)

// main boots the producer-facing HTTP API and keeps it alive until shutdown.
func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(),syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer client.Close()

	// The API only needs queue access; job execution lives in the worker binary.
	q := queue.NewRedisQueue(client, cfg.QueuePrefix, cfg.DefaultJobTimeout)
	server := httpapi.NewServer(q)

	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      server.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		<-ctx.Done()

		// Give in-flight HTTP requests a short grace period before exiting.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("api shutdown error: %v", err)
		}
	}()

	log.Printf("producer api listening on %s", cfg.HTTPAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("api server failed: %v", err)
	}
}
