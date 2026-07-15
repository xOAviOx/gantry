"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "@/lib/api";
import { StatusBadge, relTime } from "./components";

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

      {data && data.length === 0 && (
        <div className="rounded-lg border border-dashed border-edge p-10 text-center text-sm text-[#8b949e]">
          No projects yet. Create one to deploy a repo.
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        {data?.map((p) => (
          <Link
            key={p.id}
            href={`/projects/${p.id}`}
            className="rounded-lg border border-edge bg-panel p-4 transition-colors hover:border-[#3b414d]"
          >
            <div className="mb-2 flex items-center justify-between">
              <span className="font-medium">{p.name}</span>
              <StatusBadge status={p.live_status} />
            </div>
            <div className="mb-3 text-xs text-[#8b949e]">{p.repo_url}</div>
            <div className="flex items-center justify-between text-xs">
              <a
                href={`http://${p.slug}.apps.localhost/`}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => e.stopPropagation()}
                className="text-sky-400 hover:underline"
              >
                {p.slug}.apps.localhost ↗
              </a>
              <span className="text-[#8b949e]">
                deployed {relTime(p.last_deploy_at)}
              </span>
            </div>
          </Link>
        ))}
      </div>
    </main>
  );
}
