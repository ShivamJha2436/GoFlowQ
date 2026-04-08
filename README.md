# GoFlowQ

GoFlowQ is a small Redis-backed job queue system in Go, shaped like a mini Sidekiq/Celery:

- Producer API for enqueuing jobs
- Embedded dashboard served from the API binary
- Worker pool powered by goroutines
- Exponential retry logic
- Dead-letter queue for exhausted jobs
- Priority queues (`high`, `default`, `low`)
- Worker activity history and dashboard queue actions
- Next.js + Tailwind + TypeScript frontend in the same repo

## Architecture

- `cmd/api`: HTTP producer API plus embedded dashboard bundle
- `cmd/worker`: Worker pool and retry scheduler
- `internal/httpapi/dashboardapp`: Next.js monorepo frontend, exported and embedded into the Go API
- Redis lists store ready and in-flight jobs by priority
- A Redis sorted set stores scheduled retries
- A Redis list stores dead-lettered jobs

Priority handling is strict: workers always attempt `high`, then `default`, then `low`.

## Quick Start

Start Redis:

```bash
docker compose up -d
```

Run the producer API:

```bash
go run ./cmd/api
```

Open the dashboard:

```bash
http://localhost:8080/
```

If you change the dashboard frontend itself, rebuild its static export:

```bash
cd internal/httpapi/dashboardapp
npm install
npm run build
```

Run the worker pool:

```bash
go run ./cmd/worker
```

Queue a job:

```bash
curl -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "echo",
    "priority": "high",
    "max_retries": 3,
    "payload": {"message": "hello from GoFlowQ"}
  }'
```

Queue a retryable flaky job:

```bash
curl -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "flaky",
    "priority": "default",
    "max_retries": 3,
    "payload": {
      "message": "retry me",
      "fail_until_attempt": 2
    }
  }'
```

Inspect queue state:

```bash
curl http://localhost:8080/queues/stats
curl http://localhost:8080/queues/scheduled
curl http://localhost:8080/queues/dead
curl http://localhost:8080/handlers
```

The browser dashboard uses `GET /dashboard/overview` under the hood and is ultimately served by the same Go binary, but the UI itself now lives as a proper Next.js app inside the repo and is exported into a static bundle for embedding.
It supports promoting scheduled retries, retrying dead jobs, deleting dead jobs, and viewing recent worker activity.

## API

### `POST /jobs`

Request body:

```json
{
  "type": "echo",
  "priority": "high",
  "payload": {"message": "hello"},
  "max_retries": 3,
  "timeout_seconds": 10
}
```

Notes:

- `priority` defaults to `default`
- `max_retries` is the number of retries after the first attempt
- `timeout_seconds` controls the per-job handler timeout

### `GET /queues/stats`

Returns queue lengths for ready, processing, scheduled, and dead-letter queues.

### `GET /queues/scheduled`

Returns jobs waiting for their next retry window.

### `GET /queues/dead`

Returns jobs that exhausted retries and were moved to the dead-letter queue.

### `GET /dashboard/overview`

Returns a combined payload for the embedded dashboard, including queue stats, handlers, scheduled retries, dead-lettered jobs, and recent activity.

### `POST /queues/scheduled/{id}/promote`

Promotes a scheduled retry back into the ready queue immediately.

### `POST /queues/dead/{id}/retry`

Requeues a dead-lettered job and resets its retry lifecycle.

### `DELETE /queues/dead/{id}`

Permanently removes a dead-lettered job from the DLQ.

## Built-in Handlers

- `echo`: logs the payload and succeeds
- `sleep`: sleeps for `duration_ms` from the payload
- `flaky`: fails until `fail_until_attempt` is reached, then succeeds

## Retry Behavior

- Failed jobs use exponential backoff from `RETRY_BASE_DELAY`
- Retry delays are capped at one minute
- When a job exceeds `max_retries`, it is moved to the dead-letter queue

## Configuration

See [.env.example](/home/sagethefox/dev-env/projects/go/GoFlowQ/.env.example) for supported environment variables.
