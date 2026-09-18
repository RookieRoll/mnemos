// Shared helpers for the mnemos pi extension: how mnemos is located, how a
// hook is invoked, and how its result is read.
//
// The transport is behind `HookInvoker` so the event mappings never learn
// whether a result came from a spawned process or, later, from an HTTP
// daemon. That seam is the reason this file exists separately from the
// event wiring.

/** The neutral result envelope every hook emits under `--format json`. */
export interface HookResult {
  session_id?: string;
  context?: string;
  block?: boolean;
  block_reason?: string;
}

/** What one hook invocation needs. Mirrors the four fields harnesses consume. */
export interface HookPayload {
  /** Hook event name, matching the Claude Code payload shape mnemos parses. */
  hook_event_name?: string;
  /** Project scoping; mnemos falls back to the process cwd when absent. */
  cwd?: string;
  /** UserPromptSubmit text. */
  prompt?: string;
  /** PreToolUse tool identity and arguments. */
  tool_name?: string;
  tool_input?: Record<string, unknown>;
  /** SessionEnd reason. */
  reason?: string;
  /** mnemos session id, when the caller already knows it. */
  session_id?: string;
}

/** The subset of the pi ExtensionAPI this extension uses. */
export interface PiApi {
  on(event: string, handler: (...args: any[]) => any): void;
  exec(
    command: string,
    args: string[],
    options?: { signal?: AbortSignal; timeout?: number; cwd?: string },
  ): Promise<{ stdout: string; stderr: string; code: number; killed: boolean }>;
}

/** One hook invocation's outcome, normalised for the event mappers. */
export interface HookInvoker {
  /**
   * Runs a mnemos subcommand and returns its neutral result. `subcommand`
   * is the argv path, so the hook dispatcher's own sub-word is passed
   * separately: `["hook", "user-prompt"]`.
   */
  run(subcommand: string[], payload: HookPayload, timeoutMs: number): Promise<HookResult>;
  /**
   * Runs prewarm and returns its block in the same envelope as the hooks.
   *
   * prewarm is a top-level subcommand with its own output shape — it
   * reports `text`, not `context` — so the mapping lives here rather than
   * in the event wiring. Keeping it in the transport is what lets the
   * event mappers treat every push identically.
   */
  prewarm(payload: HookPayload, timeoutMs: number): Promise<HookResult>;
  /** True when every hook should no-op. */
  disabled(): boolean;
}

/** The empty result. Returning this instead of throwing is the contract. */
export const EMPTY: HookResult = Object.freeze({ block: false });

/**
 * The global kill switch, matching the Go side's `MNEMOS_DISABLED`.
 * Semantics differ slightly and deliberately: mnemos honours any non-empty
 * value, while pi's disable is only meaningful for the truthy spellings a
 * user would actually type, so a stray `MNEMOS_DISABLED=0` does not
 * silently turn memory off.
 */
export function isDisabled(env: NodeJS.ProcessEnv = process.env): boolean {
  const raw = (env.MNEMOS_DISABLED ?? "").trim().toLowerCase();
  return raw !== "" && raw !== "0" && raw !== "false" && raw !== "no";
}

/** Where the mnemos binary is expected to be. */
export function mnemosCommand(env: NodeJS.ProcessEnv = process.env): string {
  const override = (env.MNEMOS_BIN ?? "").trim();
  return override !== "" ? override : "mnemos";
}

/**
 * Spawn-backed invoker: the same shape mnemos already uses with Claude
 * Code — a short-lived process per event.
 *
 * Payloads travel as `--payload` because pi's `exec` has no stdin. That
 * flag is the only reason this transport is expressible at all, and it is
 * why the Go side grew it.
 */
export function createSpawnInvoker(pi: PiApi, env: NodeJS.ProcessEnv = process.env): HookInvoker {
  const command = mnemosCommand(env);
  return {
    disabled: () => isDisabled(env),
    async prewarm(payload, timeoutMs) {
      const raw = await runCapture(pi, command, ["prewarm"], payload, timeoutMs);
      const text = (raw as { text?: unknown }).text;
      return typeof text === "string" ? { ...raw, context: text } : raw;
    },
    async run(subcommand, payload, timeoutMs) {
      return runCapture(pi, command, subcommand, payload, timeoutMs);
    },
  };
}

/**
 * One spawn, parsed. Extracted so `run` and `prewarm` cannot drift apart
 * on argv order, the `--format` flag, or the degraded-silent contract.
 */
async function runCapture(
  pi: PiApi,
  command: string,
  subcommand: string[],
  payload: HookPayload,
  timeoutMs: number,
): Promise<HookResult> {
  try {
    const result = await pi.exec(
      command,
      [...subcommand, "--payload", JSON.stringify(payload), "--format", "json"],
      { timeout: timeoutMs },
    );
    return parseHookResult(result.stdout);
  } catch {
    // Degraded-silent is the whole contract: an unreachable binary, a
    // timeout, or a non-zero exit must not obstruct the session.
    return EMPTY;
  }
}

/**
 * Parses a neutral envelope. Malformed output, an empty string, and a
 * non-object all degrade to EMPTY — never to an error, because a hook that
 * throws would surface as a failed turn.
 */
export function parseHookResult(stdout: string): HookResult {
  const text = (stdout ?? "").trim();
  if (text === "") return EMPTY;
  try {
    const parsed = JSON.parse(text);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return EMPTY;
    return parsed as HookResult;
  } catch {
    return EMPTY;
  }
}

/**
 * Reads the session id out of a prewarm result. `mnemos prewarm` reports
 * it at the top level; the hook envelope nests nothing, so one lookup
 * suffices, but keeping the extraction in one place means a shape change
 * is a one-line fix rather than a hunt.
 */
export function sessionIdOf(result: HookResult): string {
  const id = result.session_id;
  return typeof id === "string" && id !== "" ? id : "";
}
