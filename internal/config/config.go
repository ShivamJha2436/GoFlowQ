package config

import (
	"os"
	"strconv"
	"time"
)

// Config collects all runtime knobs for the API and worker binaries.
type Config struct {
	HTTPAddr          string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	QueuePrefix       string
	WorkerConcurrency int
	PollInterval      time.Duration
	RetryBaseDelay    time.Duration
	SchedulerInterval time.Duration
	SchedulerBatch    int
	DefaultJobTimeout time.Duration
}

// Load reads configuration from environment variables and applies sensible defaults.
func Load() Config {
	return Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     os.Getenv("REDIS_PASSWORD"),
		RedisDB:           getEnvInt("REDIS_DB", 0),
		QueuePrefix:       getEnv("QUEUE_PREFIX", "goflowq"),
		WorkerConcurrency: getEnvInt("WORKER_CONCURRENCY", 5),
		PollInterval:      getEnvDuration("POLL_INTERVAL", 500*time.Millisecond),
		RetryBaseDelay:    getEnvDuration("RETRY_BASE_DELAY", 2*time.Second),
		SchedulerInterval: getEnvDuration("SCHEDULER_INTERVAL", 1*time.Second),
		SchedulerBatch:    getEnvInt("SCHEDULER_BATCH", 100),
		DefaultJobTimeout: getEnvDuration("DEFAULT_JOB_TIMEOUT", 10*time.Second),
	}
}

// getEnv reads a string env var and falls back when it is unset.
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

// getEnvInt reads an integer env var and falls back when parsing fails.
func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

// getEnvDuration reads a Go duration env var such as "500ms" or "2s".
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}

	return parsed
}
