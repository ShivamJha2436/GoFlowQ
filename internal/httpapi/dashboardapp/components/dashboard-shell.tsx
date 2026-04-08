"use client";

import { startTransition, useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";

import type {
  ActivityEvent,
  DashboardOverview,
  Job,
  QueueActionResponse,
  ScheduledJob,
} from "@/lib/types";

type Feedback = {
  tone: "idle" | "success" | "error";
  message: string;
};

const defaultOverview: DashboardOverview = {
  generated_at: "",
  handlers: [],
  stats: {
    ready: {},
    processing: {},
    scheduled: 0,
    dead: 0,
  },
  scheduled: [],
  dead: [],
  activity: [],
};

export function DashboardShell() {
  const [overview, setOverview] = useState<DashboardOverview>(defaultOverview);
  const [isRefreshing, setIsRefreshing] = useState(true);
  const [status, setStatus] = useState("Connecting to Redis");
  const [statusTone, setStatusTone] = useState<"live" | "down" | "warming">("warming");
  const [feedback, setFeedback] = useState<Feedback>({
    tone: "idle",
    message: "Ready to enqueue work.",
  });
  const [formState, setFormState] = useState({
    type: "echo",
    priority: "default",
    max_retries: "3",
    timeout_seconds: "10",
    payload: '{\n  "message": "hello from the control room"\n}',
  });

  useEffect(() => {
    void loadOverview();

    const intervalID = window.setInterval(() => {
      void loadOverview({ quiet: true });
    }, 5000);

    return () => window.clearInterval(intervalID);
  }, []);

  useEffect(() => {
    if (overview.handlers.length === 0) {
      return;
    }

    setFormState((current) => {
      if (overview.handlers.includes(current.type)) {
        return current;
      }

      return {
        ...current,
        type: overview.handlers[0],
      };
    });
  }, [overview.handlers]);

  async function loadOverview(options?: { quiet?: boolean }) {
    if (!options?.quiet) {
      setIsRefreshing(true);
    }

    try {
      const data = await fetchJSON<DashboardOverview>("/dashboard/overview?limit=20");

      startTransition(() => {
        setOverview(data);
        setStatus("Redis connected");
        setStatusTone("live");
      });
    } catch (error) {
      const message = getErrorMessage(error);
      setStatus(message);
      setStatusTone("down");
    } finally {
      setIsRefreshing(false);
    }
  }

  async function submitJob(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    let payload: unknown;
    try {
      payload = JSON.parse(formState.payload);
    } catch {
      setFeedback({
        tone: "error",
        message: "Payload must be valid JSON before a job can be queued.",
      });
      return;
    }

    setFeedback({
      tone: "idle",
      message: "Queueing job...",
    });

    try {
      const response = await fetchJSON<{ status: string; job: Job }>("/jobs", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          type: formState.type,
          priority: formState.priority,
          max_retries: Number.parseInt(formState.max_retries, 10),
          timeout_seconds: Number.parseInt(formState.timeout_seconds, 10),
          payload,
        }),
      });

      setFeedback({
        tone: "success",
        message: `Queued job ${response.job.id} successfully.`,
      });
      await loadOverview();
    } catch (error) {
      setFeedback({
        tone: "error",
        message: getErrorMessage(error),
      });
    }
  }

  async function runAction(
    path: string,
    method: "POST" | "DELETE",
    successMessage: string,
    confirmationMessage?: string,
  ) {
    if (confirmationMessage && !window.confirm(confirmationMessage)) {
      return;
    }

    setFeedback({
      tone: "idle",
      message: "Applying queue action...",
    });

    try {
      await fetchJSON<QueueActionResponse>(path, { method });
      setFeedback({
        tone: "success",
        message: successMessage,
      });
      await loadOverview();
    } catch (error) {
      setFeedback({
        tone: "error",
        message: getErrorMessage(error),
      });
    }
  }

  const readyHigh = overview.stats.ready.high ?? 0;
  const readyDefault = overview.stats.ready.default ?? 0;
  const readyLow = overview.stats.ready.low ?? 0;
  const processingHigh = overview.stats.processing.high ?? 0;
  const processingDefault = overview.stats.processing.default ?? 0;
  const processingLow = overview.stats.processing.low ?? 0;
  const processingTotal = processingHigh + processingDefault + processingLow;
  const totalQueued = readyHigh + readyDefault + readyLow;

  return (
    <main className="relative mx-auto flex min-h-screen w-full max-w-[1500px] flex-col gap-6 px-4 py-5 text-ink md:px-6 md:py-6 xl:px-8">
      <section className="glass-panel bg-hero-glow overflow-hidden p-6 md:p-8">
        <div className="grid gap-8 xl:grid-cols-[1.3fr_0.7fr]">
          <div className="space-y-6">
            <div className="space-y-3">
              <p className="subtle-label">Operations Dashboard</p>
              <h1 className="max-w-[11ch] font-display text-5xl leading-[0.9] text-slatepanel md:text-7xl">
                GoFlowQ Control Room
              </h1>
              <p className="max-w-2xl text-base leading-7 text-slate-600 md:text-lg">
                A production-style queue dashboard for your Redis-backed workers, with live queue depth,
                retry visibility, dead-letter controls, and operator actions in one place.
              </p>
            </div>

            <div className="grid gap-4 md:grid-cols-3">
              <HeroStat
                label="Ready backlog"
                value={totalQueued}
                accent="from-amber-500/20 to-amber-100/40"
                helper="Queued across all priorities"
              />
              <HeroStat
                label="Jobs in flight"
                value={processingTotal}
                accent="from-emerald-500/20 to-emerald-100/30"
                helper="Currently owned by workers"
              />
              <HeroStat
                label="Scheduled retries"
                value={overview.stats.scheduled}
                accent="from-sky-500/20 to-white/10"
                helper="Waiting for next retry window"
              />
            </div>
          </div>

          <div className="glass-panel flex flex-col justify-between gap-5 bg-slatepanel p-5 text-white/90">
            <div className="space-y-3">
              <p className="subtle-label text-white/60">Connection</p>
              <div className="flex items-center gap-3">
                <span
                  className={[
                    "status-dot",
                    statusTone === "live"
                      ? "bg-emerald-400"
                      : statusTone === "down"
                        ? "bg-rose-400"
                        : "bg-amber-300",
                  ].join(" ")}
                />
                <span className="text-sm text-white/80">{status}</span>
              </div>
              <p className="text-sm leading-6 text-white/65">
                {overview.generated_at
                  ? `Last refresh ${formatDateTime(overview.generated_at)}`
                  : "Waiting for first refresh"}
              </p>
            </div>

            <div className="rounded-[24px] border border-white/10 bg-white/5 p-4">
              <p className="text-xs uppercase tracking-[0.22em] text-white/45">Quick facts</p>
              <div className="mt-4 grid gap-3 text-sm text-white/75">
                <FactRow label="Registered handlers" value={String(overview.handlers.length)} />
                <FactRow label="Dead-letter jobs" value={String(overview.stats.dead)} />
                <FactRow label="Refresh cadence" value="5 seconds" />
              </div>
            </div>

            <button
              className="surface-button-primary"
              type="button"
              onClick={() => void loadOverview()}
            >
              {isRefreshing ? "Refreshing..." : "Refresh dashboard"}
            </button>
          </div>
        </div>
      </section>

      <section className="grid gap-6 xl:grid-cols-[0.85fr_1.15fr]">
        <div className="space-y-6">
          <div className="glass-panel p-5 md:p-6">
            <div className="flex items-start justify-between gap-4">
              <div>
                <p className="subtle-label">Queue Health</p>
                <h2 className="panel-title">Priority pressure</h2>
              </div>
              <span className="rounded-full bg-slate-900/5 px-3 py-1 text-xs text-slate-600">
                Generated {overview.generated_at ? formatDateTime(overview.generated_at) : "pending"}
              </span>
            </div>

            <div className="mt-5 grid gap-4 md:grid-cols-2">
              <MetricCard label="High priority ready" value={readyHigh} tone="high" />
              <MetricCard label="Default ready" value={readyDefault} tone="default" />
              <MetricCard label="Low priority ready" value={readyLow} tone="low" />
              <MetricCard label="Dead-letter queue" value={overview.stats.dead} tone="dead" />
            </div>

            <div className="mt-5 rounded-[26px] border border-slate-900/10 bg-slate-900/[0.03] p-4">
              <div className="grid gap-3">
                <ProgressRow label="High processing" value={processingHigh} />
                <ProgressRow label="Default processing" value={processingDefault} />
                <ProgressRow label="Low processing" value={processingLow} />
              </div>
            </div>
          </div>

          <div className="glass-panel p-5 md:p-6">
            <div className="space-y-1">
              <p className="subtle-label">Dispatch</p>
              <h2 className="panel-title">Queue a job</h2>
            </div>

            <form className="mt-5 grid gap-4" onSubmit={submitJob}>
              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Job type">
                  <select
                    className="surface-input"
                    value={formState.type}
                    onChange={(event) =>
                      setFormState((current) => ({ ...current, type: event.target.value }))
                    }
                  >
                    {overview.handlers.length === 0 ? (
                      <option value="echo">echo</option>
                    ) : (
                      overview.handlers.map((handler) => (
                        <option key={handler} value={handler}>
                          {handler}
                        </option>
                      ))
                    )}
                  </select>
                </Field>

                <Field label="Priority">
                  <select
                    className="surface-input"
                    value={formState.priority}
                    onChange={(event) =>
                      setFormState((current) => ({ ...current, priority: event.target.value }))
                    }
                  >
                    <option value="high">high</option>
                    <option value="default">default</option>
                    <option value="low">low</option>
                  </select>
                </Field>
              </div>

              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Max retries">
                  <input
                    className="surface-input"
                    min="0"
                    type="number"
                    value={formState.max_retries}
                    onChange={(event) =>
                      setFormState((current) => ({ ...current, max_retries: event.target.value }))
                    }
                  />
                </Field>

                <Field label="Timeout seconds">
                  <input
                    className="surface-input"
                    min="1"
                    type="number"
                    value={formState.timeout_seconds}
                    onChange={(event) =>
                      setFormState((current) => ({
                        ...current,
                        timeout_seconds: event.target.value,
                      }))
                    }
                  />
                </Field>
              </div>

              <Field label="Payload JSON">
                <textarea
                  className="surface-input min-h-[220px] resize-y font-mono text-[13px] leading-6"
                  value={formState.payload}
                  onChange={(event) =>
                    setFormState((current) => ({ ...current, payload: event.target.value }))
                  }
                />
              </Field>

              <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
                <p
                  className={[
                    "text-sm",
                    feedback.tone === "success"
                      ? "text-emerald-700"
                      : feedback.tone === "error"
                        ? "text-rose-700"
                        : "text-slate-500",
                  ].join(" ")}
                >
                  {feedback.message}
                </p>

                <button className="surface-button-primary" type="submit">
                  Enqueue job
                </button>
              </div>
            </form>
          </div>
        </div>

        <div className="space-y-6">
          <div className="glass-panel p-5 md:p-6">
            <div className="flex flex-wrap items-center justify-between gap-4">
              <div>
                <p className="subtle-label">Capabilities</p>
                <h2 className="panel-title">Registered handlers</h2>
              </div>
              <p className="text-sm text-slate-500">What the worker pool can execute right now.</p>
            </div>

            <div className="mt-5 flex flex-wrap gap-3">
              {overview.handlers.map((handler) => (
                <span
                  key={handler}
                  className="rounded-full border border-amber-300/40 bg-amber-50 px-4 py-2 text-sm text-amber-900"
                >
                  {handler}
                </span>
              ))}
            </div>
          </div>

          <div className="glass-panel p-5 md:p-6">
            <SectionHeader
              kicker="Scheduled Retries"
              title="Jobs waiting for another attempt"
              description="Promote delayed jobs immediately when you need to override the backoff window."
            />
            <div className="mt-5 overflow-x-auto">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Type</th>
                    <th>Priority</th>
                    <th>Attempt</th>
                    <th>Next Run</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  {overview.scheduled.length === 0 ? (
                    <EmptyTableRow colSpan={6} message="No scheduled retries are waiting right now." />
                  ) : (
                    overview.scheduled.map((item) => (
                      <ScheduledRow
                        key={item.job.id}
                        item={item}
                        onPromote={() =>
                          void runAction(
                            `/queues/scheduled/${encodeURIComponent(item.job.id)}/promote`,
                            "POST",
                            `Promoted ${item.job.id} back to the ready queue.`,
                          )
                        }
                      />
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="glass-panel p-5 md:p-6">
            <SectionHeader
              kicker="Dead Letter Queue"
              title="Jobs that need operator attention"
              description="Retry exhausted jobs after inspection or remove them permanently from the DLQ."
            />
            <div className="mt-5 overflow-x-auto">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Type</th>
                    <th>Priority</th>
                    <th>Attempt</th>
                    <th>Failed At</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {overview.dead.length === 0 ? (
                    <EmptyTableRow colSpan={6} message="No dead-lettered jobs at the moment." />
                  ) : (
                    overview.dead.map((job) => (
                      <DeadRow
                        key={job.id}
                        job={job}
                        onRetry={() =>
                          void runAction(
                            `/queues/dead/${encodeURIComponent(job.id)}/retry`,
                            "POST",
                            `Retried dead-lettered job ${job.id}.`,
                          )
                        }
                        onDelete={() =>
                          void runAction(
                            `/queues/dead/${encodeURIComponent(job.id)}`,
                            "DELETE",
                            `Deleted dead-lettered job ${job.id}.`,
                            "This will remove the job from the dead-letter queue. Continue?",
                          )
                        }
                      />
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="glass-panel p-5 md:p-6">
            <SectionHeader
              kicker="Recent Activity"
              title="Worker and scheduler timeline"
              description="A short event feed showing how jobs move through queue states in real time."
            />
            <div className="mt-5 grid gap-3">
              {overview.activity.length === 0 ? (
                <div className="rounded-[22px] border border-slate-900/10 bg-slate-900/[0.03] p-5 text-sm text-slate-500">
                  No worker activity yet.
                </div>
              ) : (
                overview.activity.map((event, index) => (
                  <ActivityCard key={`${event.timestamp}-${event.kind}-${index}`} event={event} />
                ))
              )}
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}

function HeroStat({
  label,
  value,
  helper,
  accent,
}: {
  label: string;
  value: number;
  helper: string;
  accent: string;
}) {
  return (
    <div className={`glass-panel rounded-[24px] bg-gradient-to-br ${accent} p-4`}>
      <p className="text-sm text-slate-500">{label}</p>
      <p className="mt-2 font-display text-4xl text-slatepanel">{formatNumber(value)}</p>
      <p className="mt-2 text-sm text-slate-600">{helper}</p>
    </div>
  );
}

function FactRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span>{label}</span>
      <span className="rounded-full bg-white/10 px-3 py-1 text-white">{value}</span>
    </div>
  );
}

function MetricCard({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: "high" | "default" | "low" | "dead";
}) {
  const toneClasses =
    tone === "high"
      ? "border-amber-300/40 bg-amber-50/80"
      : tone === "default"
        ? "border-emerald-300/40 bg-emerald-50/70"
        : tone === "low"
          ? "border-sky-300/40 bg-sky-50/70"
          : "border-rose-300/40 bg-rose-50/80";

  return (
    <div className={`metric-card border ${toneClasses}`}>
      <p className="text-sm text-slate-500">{label}</p>
      <p className="mt-2 font-display text-4xl text-slatepanel">{formatNumber(value)}</p>
    </div>
  );
}

function ProgressRow({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-2xl border border-slate-900/10 bg-white/65 px-4 py-3">
      <span className="text-sm text-slate-600">{label}</span>
      <span className="font-mono text-sm text-slate-900">{formatNumber(value)}</span>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="grid gap-2">
      <span className="text-sm font-medium text-slate-700">{label}</span>
      {children}
    </label>
  );
}

function SectionHeader({
  kicker,
  title,
  description,
}: {
  kicker: string;
  title: string;
  description: string;
}) {
  return (
    <div className="flex flex-col gap-2 md:flex-row md:items-end md:justify-between">
      <div className="space-y-1">
        <p className="subtle-label">{kicker}</p>
        <h2 className="panel-title">{title}</h2>
      </div>
      <p className="max-w-xl text-sm leading-6 text-slate-500">{description}</p>
    </div>
  );
}

function EmptyTableRow({ colSpan, message }: { colSpan: number; message: string }) {
  return (
    <tr>
      <td className="text-slate-500" colSpan={colSpan}>
        {message}
      </td>
    </tr>
  );
}

function ScheduledRow({
  item,
  onPromote,
}: {
  item: ScheduledJob;
  onPromote: () => void;
}) {
  return (
    <tr>
      <td className="font-mono text-[13px] text-slate-900">{item.job.id}</td>
      <td>{item.job.type}</td>
      <td>
        <PriorityBadge priority={item.job.priority} />
      </td>
      <td>{item.job.attempt}</td>
      <td>
        <div className="space-y-1">
          <div>{formatDateTime(item.run_at)}</div>
          <div className="text-xs text-slate-500">{item.job.last_error || "Waiting for retry window"}</div>
        </div>
      </td>
      <td>
        <button className="surface-button-secondary" type="button" onClick={onPromote}>
          Promote now
        </button>
      </td>
    </tr>
  );
}

function DeadRow({
  job,
  onRetry,
  onDelete,
}: {
  job: Job;
  onRetry: () => void;
  onDelete: () => void;
}) {
  return (
    <tr>
      <td className="font-mono text-[13px] text-slate-900">{job.id}</td>
      <td>{job.type}</td>
      <td>
        <PriorityBadge priority={job.priority} />
      </td>
      <td>{job.attempt}</td>
      <td>
        <div className="space-y-1">
          <div>{formatDateTime(job.failed_at)}</div>
          <div className="text-xs text-slate-500">{job.last_error || "No error details"}</div>
        </div>
      </td>
      <td>
        <div className="flex flex-wrap gap-2">
          <button className="surface-button-secondary" type="button" onClick={onRetry}>
            Retry
          </button>
          <button className="surface-button-danger" type="button" onClick={onDelete}>
            Delete
          </button>
        </div>
      </td>
    </tr>
  );
}

function ActivityCard({ event }: { event: ActivityEvent }) {
  return (
    <article className="rounded-[24px] border border-slate-900/10 bg-white/70 p-4">
      <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
        <div>
          <p className="subtle-label">{event.kind.replaceAll("_", " ")}</p>
          <p className="mt-2 text-sm leading-6 text-slate-700">{event.message}</p>
        </div>
        <span className="rounded-full bg-slate-900/5 px-3 py-1 text-xs text-slate-500">
          {formatDateTime(event.timestamp)}
        </span>
      </div>

      <div className="mt-4 flex flex-wrap gap-2 text-xs text-slate-600">
        {event.job_id ? <MetaPill label={`Job ${event.job_id}`} /> : null}
        {event.job_type ? <MetaPill label={`Type ${event.job_type}`} /> : null}
        {event.priority ? <MetaPill label={`Priority ${event.priority}`} /> : null}
        {event.attempt ? <MetaPill label={`Attempt ${event.attempt}`} /> : null}
        {event.worker_id ? <MetaPill label={`Worker ${event.worker_id}`} /> : null}
      </div>
    </article>
  );
}

function MetaPill({ label }: { label: string }) {
  return <span className="rounded-full border border-slate-900/10 bg-slate-50 px-3 py-1">{label}</span>;
}

function PriorityBadge({ priority }: { priority: string }) {
  const className =
    priority === "high"
      ? "bg-amber-100 text-amber-900 border-amber-200"
      : priority === "low"
        ? "bg-sky-100 text-sky-900 border-sky-200"
        : "bg-emerald-100 text-emerald-900 border-emerald-200";

  return <span className={`rounded-full border px-3 py-1 text-xs ${className}`}>{priority}</span>;
}

async function fetchJSON<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, options);
  const body = (await response.json().catch(() => ({}))) as { error?: string };

  if (!response.ok) {
    throw new Error(body.error ?? `Request failed with status ${response.status}`);
  }

  return body as T;
}

function getErrorMessage(error: unknown) {
  if (error instanceof Error) {
    return error.message;
  }

  return "Something went wrong while talking to the queue API.";
}

function formatDateTime(value?: string) {
  if (!value) {
    return "n/a";
  }

  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(parsed);
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}
