"use client";

import { useEffect, useRef, useState } from "react";
import { isFailed, isTerminal, LogLine, logsStreamURL, PIPELINE } from "@/lib/api";

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

export function formatBytes(n: number): string {
  if (!n || n < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
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

// Bounded in-memory tail + fixed row height keep the DOM small no matter how
// noisy a build is; ROW_H must match the row's lineHeight below for the windowed
// list to line up.
const MAX_LINES = 5000;
const ROW_H = 18;
const OVERSCAN = 12;

// LogViewer streams a deployment's logs over SSE (SPEC.md §10). The browser's
// EventSource replays the backlog, then live lines arrive as `log` events; on a
// hard refresh it resumes via Last-Event-ID. Rows render through a windowed
// (virtualized) list so a multi-thousand-line build stays smooth.
export function LogViewer({
  deploymentId,
  status,
}: {
  deploymentId: string;
  status: string;
}) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [gap, setGap] = useState(false);
  const [autoscroll, setAutoscroll] = useState(true);

  const boxRef = useRef<HTMLDivElement>(null);
  const esRef = useRef<EventSource | null>(null);
  const maxSeqRef = useRef(0);

  const [scrollTop, setScrollTop] = useState(0);
  const [viewH, setViewH] = useState(0);

  const terminal = isTerminal(status);

  // One EventSource for the component's lifetime. It reconnects on its own and
  // sends Last-Event-ID; we still guard against duplicate seqs defensively.
  useEffect(() => {
    const es = new EventSource(logsStreamURL(deploymentId));
    esRef.current = es;

    es.addEventListener("log", (e) => {
      const ll = JSON.parse((e as MessageEvent).data) as LogLine;
      if (ll.seq <= maxSeqRef.current) return;
      maxSeqRef.current = ll.seq;
      setLines((prev) => {
        const trimmed =
          prev.length >= MAX_LINES ? prev.slice(prev.length - MAX_LINES + 1) : prev;
        return [...trimmed, ll];
      });
    });
    es.addEventListener("gap", () => setGap(true));

    return () => {
      es.close();
      esRef.current = null;
    };
  }, [deploymentId]);

  // Close the stream a beat after the deployment settles — the backlog has fully
  // arrived by then, and there is nothing more to receive.
  useEffect(() => {
    if (!terminal) return;
    const t = setTimeout(() => esRef.current?.close(), 3000);
    return () => clearTimeout(t);
  }, [terminal]);

  // Track the viewport height for windowing.
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    setViewH(el.clientHeight);
    const ro = new ResizeObserver(() => setViewH(el.clientHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Keep pinned to the bottom while autoscroll is on.
  useEffect(() => {
    if (autoscroll && boxRef.current) {
      boxRef.current.scrollTop = boxRef.current.scrollHeight;
    }
  }, [lines, autoscroll]);

  const onScroll = () => {
    const el = boxRef.current;
    if (!el) return;
    setScrollTop(el.scrollTop);
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setAutoscroll(atBottom);
  };

  const total = lines.length;
  const first = Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN);
  const last = Math.min(total, first + Math.ceil(viewH / ROW_H) + OVERSCAN * 2);
  const slice = lines.slice(first, last);

  return (
    <div className="rounded-lg border border-edge bg-black/40">
      <div className="flex items-center justify-between border-b border-edge px-3 py-2 text-xs text-[#8b949e]">
        <span>
          {total} lines{total >= MAX_LINES && ` (showing last ${MAX_LINES})`}
        </span>
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={autoscroll}
            onChange={(e) => setAutoscroll(e.target.checked)}
          />
          autoscroll
        </label>
      </div>

      {gap && (
        <div className="border-b border-yellow-500/30 bg-yellow-500/10 px-3 py-1.5 text-xs text-yellow-400">
          stream fell behind — some lines were skipped. Reload to replay the full log.
        </div>
      )}

      <div
        ref={boxRef}
        onScroll={onScroll}
        className="h-[28rem] overflow-auto px-3 py-2 font-mono text-xs"
      >
        {total === 0 ? (
          <div className="text-[#5b636e]">waiting for logs…</div>
        ) : (
          <div style={{ height: total * ROW_H, position: "relative" }}>
            {slice.map((l, i) => {
              const idx = first + i;
              return (
                <div
                  key={l.seq}
                  style={{
                    position: "absolute",
                    top: idx * ROW_H,
                    height: ROW_H,
                    lineHeight: `${ROW_H}px`,
                  }}
                  className="flex whitespace-pre"
                >
                  <span className="mr-3 select-none text-[#3b414d]">
                    {String(l.seq).padStart(4, " ")}
                  </span>
                  <span className={streamColor[l.stream] || "text-[#e6edf3]"}>
                    {l.line || " "}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
