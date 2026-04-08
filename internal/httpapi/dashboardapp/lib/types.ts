export type Priority = "high" | "default" | "low";

export interface QueueStats {
  ready: Record<string, number>;
  processing: Record<string, number>;
  scheduled: number;
  dead: number;
}

export interface Job {
  id: string;
  type: string;
  priority: Priority;
  payload: unknown;
  attempt: number;
  max_retries: number;
  timeout_seconds: number;
  created_at: string;
  available_at?: string;
  last_error?: string;
  failed_at?: string;
}

export interface ScheduledJob {
  job: Job;
  run_at: string;
  run_unix: number;
}

export interface ActivityEvent {
  timestamp: string;
  kind: string;
  message: string;
  job_id?: string;
  job_type?: string;
  priority?: Priority;
  attempt?: number;
  worker_id?: number;
}

export interface DashboardOverview {
  generated_at: string;
  handlers: string[];
  stats: QueueStats;
  scheduled: ScheduledJob[];
  dead: Job[];
  activity: ActivityEvent[];
}

export interface QueueActionResponse {
  status: string;
  job: Job;
}
