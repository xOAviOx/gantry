"use client";

import { useEffect, useRef, useState } from "react";
import { getLogs, isFailed, isTerminal, LogLine, PIPELINE } from "@/lib/api";

export function statusClass(s: string): string {
  if (s === "live") return "border-accent/50 text-accent";
  if (isFailed(s)) return "border-red-500/50 text-red-400";
  if (["retired", "superseded", "canceled"].includes(s))
    return "border-edge text-[#8b949e]";
  if (s === "queued" || s === "") return "border-edge text-[#8b949e]";
  return "border-yellow-500/50 text-yellow-400"; // in-progress
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs ${statusClass(status)}`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {status || "—"}
    </span>
  );
}

export function relTime(iso: string | null): string {
  if (!iso) return "never";
  const d = new Date(iso).getTime();
  const s = Math.round((Date.now() - d) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

// Pipeline stepper for the deployment page header.
export function PipelineSteps({ status }: { status: string }) {
  const failed = isFailed(status);
  const idx = PIPELINE.indexOf(status);
  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs">
      {PIPELINE.map((step, i) => {
        const done = idx >= 0 && i < idx;
        const current = step === status;
        const cls = current
          ? failed
            ? "border-red-500/60 text-red-400"
            : "border-yellow-500/60 text-yellow-400"
          : done
            ? "border-accent/50 text-accent"
            : "border-edge text-[#5b636e]";
        return (
          <span key={step} className="flex items-center gap-1.5">
            <span className={`rounded border px-1.5 py-0.5 ${cls}`}>{step}</span>
            {i < PIPELINE.length - 1 && <span className="text-[#3b414d]">→</span>}
          </span>
        );
      })}
      {failed && (
        <span className="ml-2 rounded border border-red-500/60 px-1.5 py-0.5 text-red-400">
          {status}
        </span>
      )}
    </div>
  );
}

const streamColor: Record<string, string> = {
  system: "text-accent",
  stderr: "text-red-400",
  git: "text-sky-400",
  build: "text-[#8b949e]",
  stdout: "text-[#e6edf3]",
};

// LogViewer polls the logs endpoint with an after-cursor and appends lines.
// (M2 replaces the polling with SSE + Last-Event-ID.)
export function LogViewer({
  deploymentId,
  status,
}: {
  deploymentId: string;
  status: string;
}) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [autoscroll, setAutoscroll] = useState(true);
  const afterRef = useRef(0);
  const boxRef = useRef<HTMLDivElement>(null);
  const terminal = isTerminal(status);

  useEffect(() => {
    let alive = true;
    let stopped = false;

    async function tick() {
      try {
        const res = await getLogs(deploymentId, afterRef.current);
        if (!alive) return;
        if (res.lines.length) {
          afterRef.current = res.next;
          setLines((prev) => [...prev, ...res.lines]);
        }
      } catch {
        /* ignore transient errors */
      }
      // Once the deployment is terminal, fetch one last time then stop.
      if (isTerminal(status) && !stopped) {
        stopped = true;
        setTimeout(() => {
          if (alive) tick();
        }, 1200);
      }
    }

    tick();
    const iv = setInterval(() => {
      if (!terminal) tick();
    }, 1000);
    return () => {
      alive = false;
      clearInterval(iv);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deploymentId, status]);

  useEffect(() => {
    if (autoscroll && boxRef.current) {
      boxRef.current.scrollTop = boxRef.current.scrollHeight;
    }
  }, [lines, autoscroll]);

  return (
    <div className="rounded-lg border border-edge bg-black/40">
      <div className="flex items-center justify-between border-b border-edge px-3 py-2 text-xs text-[#8b949e]">
        <span>{lines.length} lines</span>
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={autoscroll}
            onChange={(e) => setAutoscroll(e.target.checked)}
          />
          autoscroll
        </label>
      </div>
      <div
        ref={boxRef}
        className="h-[28rem] overflow-auto px-3 py-2 text-xs leading-relaxed"
      >
        {lines.length === 0 && (
          <div className="text-[#5b636e]">waiting for logs…</div>
        )}
        {lines.map((l) => (
          <div key={l.seq} className="whitespace-pre-wrap break-all">
            <span className="mr-2 select-none text-[#3b414d]">
              {String(l.seq).padStart(4, " ")}
            </span>
            <span className={streamColor[l.stream] || "text-[#e6edf3]"}>
              {l.line || " "}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
