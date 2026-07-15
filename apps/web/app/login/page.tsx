"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { login } from "@/lib/api";

export default function LoginPage() {
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const router = useRouter();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      await login(token);
      router.push("/");
    } catch {
      setErr("Invalid token");
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center px-6">
      <div className="mb-6 flex items-center gap-2">
        <span className="text-accent">▲</span>
        <h1 className="text-xl font-semibold tracking-tight">gantry</h1>
      </div>
      <form
        onSubmit={submit}
        className="flex flex-col gap-3 rounded-lg border border-edge bg-panel p-5"
      >
        <label className="text-xs uppercase tracking-widest text-[#8b949e]">
          admin token
        </label>
        <input
          type="password"
          autoFocus
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="rounded border border-edge bg-black/40 px-3 py-2 text-sm outline-none focus:border-accent"
          placeholder="ADMIN_TOKEN"
        />
        {err && <div className="text-xs text-red-400">{err}</div>}
        <button
          type="submit"
          disabled={busy || !token}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-black disabled:opacity-40"
        >
          {busy ? "signing in…" : "sign in"}
        </button>
      </form>
    </main>
  );
}
