"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { deployProject, getProject } from "@/lib/api";
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

      <h2 className="mb-2 text-xs uppercase tracking-widest text-[#8b949e]">
        Deployments
      </h2>
      <div className="overflow-hidden rounded-lg border border-edge">
        {deployments.length === 0 && (
          <div className="p-6 text-center text-sm text-[#8b949e]">
            No deployments yet — hit “Deploy now”.
          </div>
        )}
        {deployments.map((d) => (
          <Link
            key={d.id}
            href={`/deployments/${d.id}`}
            className="flex items-center justify-between border-b border-edge px-4 py-3 text-sm last:border-0 hover:bg-panel"
          >
            <div className="flex items-center gap-3">
              <StatusBadge status={d.status} />
              <span className="text-[#8b949e]">{d.trigger}</span>
              <span className="font-mono text-xs text-[#5b636e]">
                {d.commit_sha ? d.commit_sha.slice(0, 8) : "—"}
              </span>
            </div>
            <span className="text-xs text-[#8b949e]">{relTime(d.created_at)}</span>
          </Link>
        ))}
      </div>
    </Shell>
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
