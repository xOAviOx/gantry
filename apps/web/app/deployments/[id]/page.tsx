"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { getDeployment, isTerminal } from "@/lib/api";
import { LogViewer, PipelineSteps, StatusBadge, relTime } from "../../components";

export default function DeploymentPage() {
  const { id } = useParams<{ id: string }>();

  const { data, isLoading, error } = useQuery({
    queryKey: ["deployment", id],
    queryFn: () => getDeployment(id),
    refetchInterval: (q) => (isTerminal(q.state.data?.status ?? "") ? false : 1500),
  });

  return (
    <main className="mx-auto max-w-4xl px-6 py-10">
      <Link
        href={data ? `/projects/${data.project_id}` : "/"}
        className="text-xs text-[#8b949e] hover:underline"
      >
        ← project
      </Link>

      {isLoading && <div className="mt-4 text-sm text-[#8b949e]">loading…</div>}
      {error && (
        <div className="mt-4 text-sm text-red-400">{(error as Error).message}</div>
      )}

      {data && (
        <>
          <div className="mb-4 mt-2 flex items-center justify-between">
            <div className="flex items-center gap-3">
              <h1 className="text-lg font-semibold">
                deploy {data.id.slice(0, 8)}
              </h1>
              <StatusBadge status={data.status} />
            </div>
            <span className="text-xs text-[#8b949e]">
              {data.trigger} · {relTime(data.created_at)}
            </span>
          </div>

          <div className="mb-4 rounded-lg border border-edge bg-panel p-4">
            <PipelineSteps status={data.status} />
            {data.error && (
              <div className="mt-3 rounded border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-400">
                {data.error}
              </div>
            )}
            <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-xs text-[#8b949e] sm:grid-cols-3">
              {data.image_tag && <Meta k="image" v={data.image_tag} />}
              {data.container_name && <Meta k="container" v={data.container_name} />}
              {data.commit_sha && <Meta k="commit" v={data.commit_sha.slice(0, 12)} />}
              {data.host_port && <Meta k="health port" v={String(data.host_port)} />}
            </div>
          </div>

          <LogViewer deploymentId={id} status={data.status} />
        </>
      )}
    </main>
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-[#5b636e]">{k}</span>
      <span className="break-all text-[#e6edf3]">{v}</span>
    </div>
  );
}
