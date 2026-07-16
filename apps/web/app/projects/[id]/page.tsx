"use client";

import { useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Deployment,
  deployProject,
  EnvVarMeta,
  envRestart,
  getProject,
  listEnv,
  putEnv,
  revealEnv,
  rollbackTo,
} from "@/lib/api";
import { StatusBadge, relTime } from "../../components";

export default function ProjectPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();

  const { data, isLoading, error } = useQuery({
    queryKey: ["project", id],
    queryFn: () => getProject(id),
    refetchInterval: 3000,
  });

  const deploy = useMutation({
    mutationFn: () => deployProject(id),
    onSuccess: (dep) => {
      qc.invalidateQueries({ queryKey: ["project", id] });
      router.push(`/deployments/${dep.id}`);
    },
  });

  const rollback = useMutation({
    mutationFn: (deploymentId: string) => rollbackTo(id, deploymentId),
    onSuccess: (dep) => {
      qc.invalidateQueries({ queryKey: ["project", id] });
      router.push(`/deployments/${dep.id}`);
    },
  });

  if (isLoading) return <Shell><div className="text-sm text-[#8b949e]">loading…</div></Shell>;
  if (error) return <Shell><div className="text-sm text-red-400">{(error as Error).message}</div></Shell>;
  if (!data) return null;

  const { project, deployments } = data;

  return (
    <Shell>
      <div className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-lg font-semibold">{project.name}</h1>
          <a
            href={`http://${project.slug}.apps.localhost/`}
            target="_blank"
            rel="noreferrer"
            className="text-sm text-sky-400 hover:underline"
          >
            {project.slug}.apps.localhost ↗
          </a>
        </div>
        <button
          onClick={() => deploy.mutate()}
          disabled={deploy.isPending}
          className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-black disabled:opacity-40"
        >
          {deploy.isPending ? "queuing…" : "Deploy now"}
        </button>
      </div>

      <dl className="mb-8 grid grid-cols-2 gap-x-6 gap-y-2 rounded-lg border border-edge bg-panel p-4 text-xs sm:grid-cols-3">
        <Meta k="repo" v={project.repo_url} />
        <Meta k="branch" v={project.branch} />
        <Meta k="port" v={String(project.port)} />
        <Meta k="dockerfile" v={project.dockerfile_path} />
        <Meta k="health" v={project.health_path} />
      </dl>

      <EnvEditor projectId={id} />

      <h2 className="mb-2 mt-8 text-xs uppercase tracking-widest text-[#8b949e]">
        Deployments
      </h2>
      <div className="overflow-hidden rounded-lg border border-edge">
        {deployments.length === 0 && (
          <div className="p-6 text-center text-sm text-[#8b949e]">
            No deployments yet — hit “Deploy now”.
          </div>
        )}
        {deployments.map((d) => (
          <div
            key={d.id}
            className="flex items-center justify-between border-b border-edge px-4 py-3 text-sm last:border-0 hover:bg-panel"
          >
            <Link href={`/deployments/${d.id}`} className="flex flex-1 items-center gap-3">
              <StatusBadge status={d.status} />
              <span className="text-[#8b949e]">{d.trigger}</span>
              <span className="font-mono text-xs text-[#5b636e]">
                {d.commit_sha ? d.commit_sha.slice(0, 8) : "—"}
              </span>
            </Link>
            <div className="flex items-center gap-3">
              {d.image_tag && d.status !== "live" && (
                <button
                  onClick={() => rollback.mutate(d.id)}
                  disabled={rollback.isPending}
                  className="rounded border border-edge px-2 py-0.5 text-xs text-[#8b949e] hover:border-accent hover:text-accent disabled:opacity-40"
                  title="Redeploy this image (skip build)"
                >
                  rollback
                </button>
              )}
              <span className="text-xs text-[#8b949e]">{relTime(d.created_at)}</span>
            </div>
          </div>
        ))}
      </div>
    </Shell>
  );
}

// EnvEditor is write-only: it lists keys (never values) and lets you set/delete
// them and reveal a single value on demand. Saved changes apply on the next
// deploy or via "Restart with new env".
function EnvEditor({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const router = useRouter();
  const [newKey, setNewKey] = useState("");
  const [newVal, setNewVal] = useState("");
  const [revealed, setRevealed] = useState<Record<string, string>>({});

  const { data } = useQuery({
    queryKey: ["env", projectId],
    queryFn: () => listEnv(projectId),
  });
  const keys: EnvVarMeta[] = data?.keys ?? [];

  const save = useMutation({
    mutationFn: (vars: Record<string, string>) => putEnv(projectId, vars),
    onSuccess: () => {
      setNewKey("");
      setNewVal("");
      qc.invalidateQueries({ queryKey: ["env", projectId] });
    },
  });
  const remove = useMutation({
    mutationFn: (key: string) => putEnv(projectId, {}, [key]),
    onSuccess: (_res, key) => {
      setRevealed((r) => {
        const n = { ...r };
        delete n[key];
        return n;
      });
      qc.invalidateQueries({ queryKey: ["env", projectId] });
    },
  });
  const restart = useMutation({
    mutationFn: () => envRestart(projectId),
    onSuccess: (dep: Deployment) => router.push(`/deployments/${dep.id}`),
  });

  async function toggleReveal(key: string) {
    if (revealed[key] !== undefined) {
      setRevealed((r) => {
        const n = { ...r };
        delete n[key];
        return n;
      });
      return;
    }
    const { value } = await revealEnv(projectId, key);
    setRevealed((r) => ({ ...r, [key]: value }));
  }

  return (
    <section>
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-xs uppercase tracking-widest text-[#8b949e]">
          Environment
        </h2>
        <button
          onClick={() => restart.mutate()}
          disabled={restart.isPending}
          className="rounded border border-edge px-2 py-0.5 text-xs text-[#8b949e] hover:border-accent hover:text-accent disabled:opacity-40"
          title="Redeploy the current image with the saved env (skip build)"
        >
          {restart.isPending ? "restarting…" : "Restart with new env"}
        </button>
      </div>

      <div className="rounded-lg border border-edge bg-panel">
        {keys.length === 0 && (
          <div className="px-4 py-3 text-xs text-[#5b636e]">
            No env vars. Values are encrypted at rest and write-only.
          </div>
        )}
        {keys.map((k) => (
          <div
            key={k.key}
            className="flex items-center justify-between border-b border-edge px-4 py-2 text-sm last:border-0"
          >
            <div className="flex min-w-0 items-center gap-3">
              <span className="font-mono text-xs text-[#e6edf3]">{k.key}</span>
              <span className="font-mono text-xs text-[#5b636e]">
                {revealed[k.key] !== undefined ? revealed[k.key] : "••••••••"}
              </span>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <button
                onClick={() => toggleReveal(k.key)}
                className="text-[#8b949e] hover:text-accent"
              >
                {revealed[k.key] !== undefined ? "hide" : "reveal"}
              </button>
              <button
                onClick={() => remove.mutate(k.key)}
                className="text-[#8b949e] hover:text-red-400"
              >
                delete
              </button>
            </div>
          </div>
        ))}

        <div className="flex items-center gap-2 px-4 py-2">
          <input
            value={newKey}
            onChange={(e) => setNewKey(e.target.value.toUpperCase())}
            placeholder="KEY"
            className="w-40 rounded border border-edge bg-black/40 px-2 py-1 font-mono text-xs outline-none focus:border-accent"
          />
          <input
            value={newVal}
            onChange={(e) => setNewVal(e.target.value)}
            placeholder="value"
            className="flex-1 rounded border border-edge bg-black/40 px-2 py-1 font-mono text-xs outline-none focus:border-accent"
          />
          <button
            onClick={() => newKey && save.mutate({ [newKey]: newVal })}
            disabled={!newKey || save.isPending}
            className="rounded bg-accent px-3 py-1 text-xs font-medium text-black disabled:opacity-40"
          >
            save
          </button>
        </div>
      </div>
      <p className="mt-1.5 text-xs text-[#5b636e]">
        Saved env vars take effect on the next deploy, or immediately via “Restart
        with new env”.
      </p>
    </section>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="mx-auto max-w-3xl px-6 py-10">
      <Link href="/" className="text-xs text-[#8b949e] hover:underline">
        ← projects
      </Link>
      <div className="mt-2">{children}</div>
    </main>
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-[#5b636e]">{k}</span>
      <span className="break-all">{v}</span>
    </div>
  );
}
