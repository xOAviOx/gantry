"use client";

import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DiskReport, GCReport, getDisk, listProjects, runGC } from "@/lib/api";
import { StatusBadge, formatBytes, relTime } from "./components";

export default function Home() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["projects"],
    queryFn: listProjects,
    refetchInterval: 3000,
  });

  return (
    <main className="mx-auto max-w-4xl px-6 py-10">
      <header className="mb-8 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-accent">▲</span>
          <h1 className="text-xl font-semibold tracking-tight">gantry</h1>
        </div>
        <Link
          href="/projects/new"
          className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-black"
        >
          + New project
        </Link>
      </header>

      {isLoading && <div className="text-sm text-[#8b949e]">loading…</div>}
      {error && (
        <div className="text-sm text-red-400">{(error as Error).message}</div>
      )}

      <DiskWidget />

      {data && data.length === 0 && (
        <div className="rounded-lg border border-dashed border-edge p-10 text-center text-sm text-[#8b949e]">
          No projects yet. Create one to deploy a repo.
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        {data?.map((p) => (
          <div
            key={p.id}
            className="rounded-lg border border-edge bg-panel p-4 transition-colors hover:border-[#3b414d]"
          >
            <Link href={`/projects/${p.id}`} className="block">
              <div className="mb-2 flex items-center justify-between">
                <span className="font-medium">{p.name}</span>
                <StatusBadge status={p.live_status} />
              </div>
              <div className="text-xs text-[#8b949e]">{p.repo_url}</div>
            </Link>
            <div className="mt-3 flex items-center justify-between text-xs">
              <a
                href={`http://${p.slug}.apps.localhost/`}
                target="_blank"
                rel="noreferrer"
                className="text-sky-400 hover:underline"
              >
                {p.slug}.apps.localhost ↗
              </a>
              <span className="text-[#8b949e]">
                deployed {relTime(p.last_deploy_at)}
              </span>
            </div>
          </div>
        ))}
      </div>
    </main>
  );
}

// DiskWidget surfaces `docker system df` and offers an on-demand GC run.
function DiskWidget() {
  const qc = useQueryClient();
  const { data: disk } = useQuery({
    queryKey: ["disk"],
    queryFn: getDisk,
    refetchInterval: 15000,
  });
  const gc = useMutation({
    mutationFn: runGC,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["disk"] }),
  });
  const rows: { label: string; c: DiskReport["images"] }[] = disk
    ? [
        { label: "Images", c: disk.images },
        { label: "Containers", c: disk.containers },
        { label: "Build cache", c: disk.build_cache },
      ]
    : [];

  return (
    <section className="mb-8 rounded-lg border border-edge bg-panel p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-xs uppercase tracking-widest text-[#8b949e]">Disk</h2>
        <button
          onClick={() => gc.mutate()}
          disabled={gc.isPending}
          className="rounded border border-edge px-2 py-0.5 text-xs text-[#8b949e] hover:border-accent hover:text-accent disabled:opacity-40"
        >
          {gc.isPending ? "running GC…" : "Run GC now"}
        </button>
      </div>

      {!disk && <div className="text-xs text-[#5b636e]">loading disk usage…</div>}

      {disk && (
        <div className="grid grid-cols-3 gap-3 text-xs">
          {rows.map((r) => (
            <div key={r.label} className="rounded border border-edge bg-black/30 p-3">
              <div className="text-[#5b636e]">{r.label}</div>
              <div className="mt-1 text-sm text-[#e6edf3]">{formatBytes(r.c.size)}</div>
              <div className="text-[#8b949e]">
                {r.c.count} · {formatBytes(r.c.reclaimable)} reclaimable
              </div>
            </div>
          ))}
        </div>
      )}

      {disk && (
        <div className="mt-2 text-xs text-[#5b636e]">
          {formatBytes(disk.total_reclaimable)} reclaimable total
        </div>
      )}

      {gc.data && <GCResult report={gc.data} />}
    </section>
  );
}

function GCResult({ report }: { report: GCReport }) {
  return (
    <div className="mt-3 rounded border border-accent/40 bg-accent/10 px-3 py-2 text-xs text-accent">
      GC done in {report.duration_ms}ms — removed {report.images_removed} image(s),{" "}
      {report.containers_removed} container(s); reclaimed{" "}
      {formatBytes(report.dangling_reclaimed + report.cache_reclaimed)} (dangling +
      cache); purged {report.log_lines_purged} log line(s).
    </div>
  );
}
