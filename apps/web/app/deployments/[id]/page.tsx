"use client";

import { useEffect, useRef } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Deployment,
  eventsStreamURL,
  getDeployment,
  isTerminal,
} from "@/lib/api";
import { LogViewer, PipelineSteps, StatusBadge, relTime } from "../../components";

export default function DeploymentPage() {
  const { id } = useParams<{ id: string }>();
  const qc = useQueryClient();

  const { data, isLoading, error } = useQuery({
    queryKey: ["deployment", id],
    queryFn: () => getDeployment(id),
  });

  // Live status via SSE (SPEC.md §11): push each transition straight into the
  // query cache instead of polling. The stream also sends the current state on
  // connect, so this stays correct even for an already-finished deployment.
  const esRef = useRef<EventSource | null>(null);
  useEffect(() => {
    const es = new EventSource(eventsStreamURL(id));
    esRef.current = es;
    es.addEventListener("status", (e) => {
      const dep = JSON.parse((e as MessageEvent).data) as Deployment;
      qc.setQueryData(["deployment", id], dep);
      if (isTerminal(dep.status)) {
        // Nothing more will change; let the connection go after a short grace.
        setTimeout(() => esRef.current?.close(), 3000);
      }
    });
    return () => {
      es.close();
      esRef.current = null;
    };
  }, [id, qc]);

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
