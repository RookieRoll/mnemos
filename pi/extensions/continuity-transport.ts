// Versioned continuity transport deliberately does not use the legacy hook
// parser: an empty/silent hook response cannot prove a transaction committed.
import type { PiApi } from "./hook-transport.ts";
import { mnemosCommand } from "./hook-transport.ts";
import { isAbsolute } from "node:path";

/** Explicit startup input. Its key and complete payload survive an exact retry. */
export interface ContinuityStart {
  protocol_version: 1;
  start_key: string;
  harness_kind: "pi";
  cwd: string;
}

/** A validated committed startup identity; context failure preserves the ID. */
export interface ContinuityOpened {
  protocol_version: 1;
  status: "applied" | "replayed";
  session_id: string;
  started_at: string;
  project: string;
  workspace_root: string;
  context_status: "available" | "unavailable";
  prewarm?: string;
}

/** Only a positive unsupported result permits the legacy startup path. */
export type ContinuitySupport = "supported" | "unsupported" | "unknown";

function object(stdout: string): Record<string, unknown> | undefined {
  if (Buffer.byteLength(stdout, "utf8") > 65_536) return;
  try {
    const value = JSON.parse(stdout);
    if (value && typeof value === "object" && !Array.isArray(value)) return value;
  } catch { /* An unreadable response is unknown, never unsupported. */ }
}

/** Checks support without opening a session or inferring it from silent output. */
export async function continuitySupport(pi: PiApi, timeout: number): Promise<ContinuitySupport> {
  try {
    const result = await pi.exec(mnemosCommand(), ["continuity", "capabilities"], { timeout });
    if (result.killed) return "unknown";
    // Old mnemos dispatches an unknown top-level command before loading config
    // or a database. Match that exact diagnostic, not an arbitrary substring.
    if (result.code !== 0 && /^unknown command: continuity\r?\n(?:\r?\n|$)/.test(result.stderr)) return "unsupported";
    if (result.code !== 0) return "unknown";
    const value = object(result.stdout);
    if (value?.protocol_version !== 1) return "unknown";
    if (value.supported === true) return "supported";
    if (value.supported === false) return "unsupported";
  } catch { /* Missing binaries and timeouts do not establish unsupported. */ }
  return "unknown";
}

/** Performs one bounded attempt. Undefined means the commit outcome is unknown. */
export async function continuityOpen(pi: PiApi, payload: ContinuityStart, timeout: number): Promise<ContinuityOpened | undefined> {
  try {
    const result = await pi.exec(mnemosCommand(), ["continuity", "open", "--payload", JSON.stringify(payload)], { timeout });
    if (result.killed || result.code !== 0) return;
    const value = object(result.stdout);
    if (value?.protocol_version !== 1 || (value.status !== "applied" && value.status !== "replayed")) return;
    if (typeof value.session_id !== "string" || value.session_id.trim() === "" || Buffer.byteLength(value.session_id) > 256 || Buffer.from(value.session_id).toString("utf8") !== value.session_id) return;
    if (typeof value.started_at !== "string" || !/^\d{4}-\d\d-\d\dT/.test(value.started_at) || !Number.isFinite(Date.parse(value.started_at))) return;
    for (const field of ["project", "workspace_root"]) {
      const text = value[field];
      if (typeof text !== "string" || text.trim() === "" || text.includes("\0") || Buffer.byteLength(text) > 4096 || Buffer.from(text).toString("utf8") !== text) return;
    }
    if (!isAbsolute(value.workspace_root as string)) return;
    if (value.context_status !== "available" && value.context_status !== "unavailable") return;
    if (value.context_status === "available" && typeof value.prewarm !== "string") return;
    if (value.prewarm !== undefined && typeof value.prewarm !== "string") return;
    return value as unknown as ContinuityOpened;
  } catch { return; }
}
