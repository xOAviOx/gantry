"use client";
//
//
import { useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { createProject } from "@/lib/api";

export default function NewProject() {
  const router = useRouter();
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({
    name: "",
    slug: "",
    repo_url: "",
    branch: "main",
    dockerfile_path: "Dockerfile",
    port: 3000,
    health_path: "/",
  });

  function set<K extends keyof typeof form>(k: K, v: (typeof form)[K]) {
    setForm((f) => ({ ...f, [k]: v }));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const p = await createProject({ ...form, port: Number(form.port) });
      router.push(`/projects/${p.id}`);
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  }

  const field = "rounded border border-edge bg-black/40 px-3 py-2 text-sm outline-none focus:border-accent";
  const label = "text-xs uppercase tracking-widest text-[#8b949e]";

  return (
    <main className="mx-auto max-w-xl px-6 py-10">
      <Link href="/" className="text-xs text-[#8b949e] hover:underline">
        ← projects
      </Link>
      <h1 className="mb-6 mt-2 text-lg font-semibold">New project</h1>

      <form
        onSubmit={submit}
        className="flex flex-col gap-4 rounded-lg border border-edge bg-panel p-5"
      >
        <div className="flex flex-col gap-1.5">
          <label className={label}>name</label>
          <input
            className={field}
            value={form.name}
            onChange={(e) => set("name", e.target.value)}
            placeholder="hello-node"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <label className={label}>slug (subdomain)</label>
          <input
            className={field}
            value={form.slug}
            onChange={(e) => set("slug", e.target.value)}
            placeholder="hello"
          />
          <span className="text-xs text-[#5b636e]">
            → {form.slug || "slug"}.apps.localhost
          </span>
        </div>
        <div className="flex flex-col gap-1.5">
          <label className={label}>repo url / local path</label>
          <input
            className={field}
            value={form.repo_url}
            onChange={(e) => set("repo_url", e.target.value)}
            placeholder="https://github.com/... or D:/paas/examples/hello-node"
          />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <label className={label}>branch</label>
            <input
              className={field}
              value={form.branch}
              onChange={(e) => set("branch", e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className={label}>port</label>
            <input
              type="number"
              className={field}
              value={form.port}
              onChange={(e) => set("port", Number(e.target.value))}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className={label}>dockerfile path</label>
            <input
              className={field}
              value={form.dockerfile_path}
              onChange={(e) => set("dockerfile_path", e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className={label}>health path</label>
            <input
              className={field}
              value={form.health_path}
              onChange={(e) => set("health_path", e.target.value)}
            />
          </div>
        </div>

        {err && <div className="text-xs text-red-400">{err}</div>}
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-black disabled:opacity-40"
        >
          {busy ? "creating…" : "Create project"}
        </button>
      </form>
    </main>
  );
}
