// Typed client for the controld API. All calls are same-origin (served under
// paas.localhost), so the httpOnly session cookie is sent automatically.

export type Project = {
  id: string;
  name: string;
  slug: string;
  repo_url: string;
  branch: string;
  dockerfile_path: string;
  port: number;
  health_path: string;
  created_at: string;
};

export type ProjectWithStatus = Project & {
  live_status: string;
  live_deployment_id: string | null;
  last_deploy_at: string | null;
};

export type Deployment = {
  id: string;
  project_id: string;
  commit_sha: string;
  commit_message: string;
  trigger: string;
  status: string;
  image_tag: string;
  container_name: string;
  host_port: number | null;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};

export type LogLine = { seq: number; stream: string; line: string; ts: string };

async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    credentials: "same-origin",
    cache: "no-store",
  });
  if (res.status === 401) {
    if (
      typeof window !== "undefined" &&
      !window.location.pathname.startsWith("/login")
    ) {
      window.location.href = "/login";
    }
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const j = await res.json();
      msg = j.error || msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const login = (token: string) =>
  api<{ ok: boolean }>("/login", { method: "POST", body: JSON.stringify({ token }) });

export const listProjects = () => api<ProjectWithStatus[]>("/projects");

export const getProject = (id: string) =>
  api<{ project: Project; deployments: Deployment[] }>(`/projects/${id}`);

export const createProject = (body: Record<string, unknown>) =>
  api<Project>("/projects", { method: "POST", body: JSON.stringify(body) });

export const deployProject = (id: string) =>
  api<Deployment>(`/projects/${id}/deploy`, { method: "POST" });

export const getDeployment = (id: string) => api<Deployment>(`/deployments/${id}`);

export type EnvVarMeta = { key: string; updated_at: string };

export const listEnv = (id: string) =>
  api<{ keys: EnvVarMeta[] }>(`/projects/${id}/env`);

export const putEnv = (id: string, set: Record<string, string>, del: string[] = []) =>
  api<{ keys: EnvVarMeta[] }>(`/projects/${id}/env`, {
    method: "PUT",
    body: JSON.stringify({ set, delete: del }),
  });

export const revealEnv = (id: string, key: string) =>
  api<{ key: string; value: string }>(`/projects/${id}/env/reveal`, {
    method: "POST",
    body: JSON.stringify({ key }),
  });

export const rollbackTo = (id: string, deploymentId: string) =>
  api<Deployment>(`/projects/${id}/rollback`, {
    method: "POST",
    body: JSON.stringify({ deployment_id: deploymentId }),
  });

export const envRestart = (id: string) =>
  api<Deployment>(`/projects/${id}/env/restart`, { method: "POST" });

export type DiskCategory = { count: number; size: number; reclaimable: number };
export type DiskReport = {
  images: DiskCategory;
  containers: DiskCategory;
  build_cache: DiskCategory;
  total_reclaimable: number;
};
export type GCReport = {
  images_removed: number;
  containers_removed: number;
  dangling_reclaimed: number;
  cache_reclaimed: number;
  log_lines_purged: number;
  duration_ms: number;
};

export const getDisk = () => api<DiskReport>("/system/disk");
export const runGC = () => api<GCReport>("/system/gc", { method: "POST" });

// Live streams (SSE). Consumed via the browser's native EventSource, which sends
// the session cookie same-origin and resumes from Last-Event-ID on reconnect.
export const logsStreamURL = (id: string) => `/api/deployments/${id}/logs`;
export const eventsStreamURL = (id: string) => `/api/deployments/${id}/events`;

export const PIPELINE = [
  "queued",
  "cloning",
  "building",
  "starting",
  "checking",
  "routing",
  "live",
];

export const TERMINAL = [
  "live",
  "retired",
  "build_failed",
  "deploy_failed",
  "superseded",
  "canceled",
];

export const isTerminal = (s: string) => TERMINAL.includes(s);
export const isFailed = (s: string) =>
  ["build_failed", "deploy_failed"].includes(s);
