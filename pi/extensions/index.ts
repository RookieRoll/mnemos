// mnemos for pi.
//
// Two things have to be true for mnemos to matter in a session. The model
// must be able to call the tools (the MCP server + direct tool registration),
// and the memory must reach the model before it decides whether to look.
// The second half is what produces mnemos' measured effect, and it only
// happens if the harness pushes. That is what this extension does.
//
// Injection goes through `before_agent_start` returning `systemPrompt`
// because that is pi's only channel that reaches the model every turn
// without accumulating a copy of the block in the session's own history.
// The pushes it delivers:
//
//   session_start       prewarm  (conventions, corrections, skills, hot files)
//   before_agent_start  per-prompt memory + the capture directive
//   tool_call           file-relevant memory, and the write-boundary guardrail
//   tool_result         passive file touch, so the heat map fills without
//                       the model calling mnemos_touch
//   session_shutdown    session close with a derived summary
//
// Every call is best-effort and bounded by a timeout. The one deliberate
// failure is the guardrail: a refused write is reported as a blocked tool
// call, because that is the decision the model needs to see.

import type { HookInvoker, HookPayload, PiApi } from "./hook-transport.ts";
import { EMPTY, createSpawnInvoker } from "./hook-transport.ts";
import { filePathFromToolInput, isFileEditTool, isMnemosWriteTool } from "./tool-match.ts";

/**
 * Per-call timeouts, in milliseconds. Kept in the same range as the Claude
 * Code hook timeouts mnemos already ships, because the cost being bounded
 * is the same one: process startup plus a database open.
 */
export const TIMEOUTS = {
  prewarm: 10_000,
  prompt: 5_000,
  preTool: 5_000,
  touch: 5_000,
  sessionEnd: 5_000,
} as const;

/** Session-scoped state. Rebuilt on every `session_start`, by design. */
interface SessionState {  /** The mnemos session id prewarm established or adopted. */
  mnemosSessionId: string;
  /** The project the session is scoped to, derived once from cwd. */
  project: string;
  /** The prewarm block, pushed on the first turn. */
  prewarm: string;
  /** Whether the prewarm block has already been delivered this session. */
  prewarmDelivered: boolean;
}

function emptyState(): SessionState {
  return { mnemosSessionId: "", project: "", prewarm: "", prewarmDelivered: false };
}

/**
 * argv paths for each mnemos entry point. prewarm is a top-level
 * subcommand, so it goes through the invoker's own `prewarm` method; the
 * hooks below share the `hook <event>` dispatcher.
 */
const HOOK = {
  userPrompt: ["hook", "user-prompt"],
  preTool: ["hook", "pre-tool"],
  postTool: ["hook", "post-tool"],
  sessionEnd: ["hook", "session-end"],
} as const;

/** Derives the project name the way mnemos does: the cwd's basename. */
export function projectFromCwd(cwd: string): string {
  const normalized = (cwd ?? "").replace(/[\\/]+$/, "");
  const parts = normalized.split(/[\\/]/);
  const last = parts[parts.length - 1] ?? "";
  return last === "" ? "" : last;
}

/** Joins context fragments, dropping empties and keeping order. */
export function joinContext(...parts: Array<string | undefined>): string {
  return parts
    .map((p) => (p ?? "").trim())
    .filter((p) => p !== "")
    .join("\n");
}

export default function mnemosPi(pi: PiApi): void {
  const invoker: HookInvoker = createSpawnInvoker(pi);
  let state = emptyState();

  // ---------------------------------------------------------------- session

  pi.on("session_start", async (event: any, ctx: any) => {
    // session_start fires for startup, reload, new, resume, and fork. Each
    // is a different session, so the previous session's id must not carry
    // over: reusing it would scope this session's writes to the wrong row.
    state = emptyState();
    state.project = projectFromCwd(ctx?.cwd ?? process.cwd());
    if (invoker.disabled()) return;

    const payload: HookPayload = { hook_event_name: "SessionStart", cwd: ctx?.cwd };
    const result = await invoker.prewarm(payload, TIMEOUTS.prewarm);
    state.mnemosSessionId = result.session_id ?? "";
    state.prewarm = result.context ?? "";
  });
  // ------------------------------------------------------------- per prompt

  pi.on("before_agent_start", async (event: any, ctx: any) => {
    if (invoker.disabled()) return;

    const prompt: string = event?.prompt ?? "";
    let promptContext = "";

    if (prompt !== "") {
      const payload: HookPayload = {
        hook_event_name: "UserPromptSubmit",
        cwd: ctx?.cwd,
        prompt,
        session_id: state.mnemosSessionId,
      };
      const result = await invoker.run(HOOK.userPrompt, payload, TIMEOUTS.prompt).catch(() => EMPTY);
      promptContext = (result.context ?? "").trim();
    }

    // Prewarm rides along on whichever turn comes first rather than being
    // stashed in session history at startup, so it is present when the
    // model is actually about to act. It is delivered exactly once, which
    // is what keeps the push from accumulating across turns.
    const push = planPromptPush(state.prewarm, state.prewarmDelivered, promptContext);
    state.prewarmDelivered = push.prewarmDelivered;
    const fragments = push.fragments;

    const injected = joinContext(...fragments);
    if (injected === "") return;
    return { systemPrompt: joinContext(event?.systemPrompt, injected) };
  });

  // ---------------------------------------------------------- pre-tool call

  //
  // The pre-tool push has no channel here. `tool_call` accepts only `block`,
  // `reason`, and `terminate` — it cannot alter the system prompt, and the
  // session's message list is already fixed for the turn. That is a pi
  // constraint, not a mnemos one: the memory is retrieved and, when the
  // caller's envelope carries it, logged as surfaced, but it cannot be
  // handed to the model from this event. The pushes that do reach the model
  // are prewarm and the per-prompt context, both via `before_agent_start`.
  //
  // The guardrail is the half that does work here, and it is deliberately
  // the only failing path.
  pi.on("tool_call", async (event: any, ctx: any) => {
    const toolName: string = event?.toolName ?? "";
    const toolInput = event?.input ?? {};

    // The guardrail and the memory push are one decision inside mnemos, so
    // one invocation covers both. The guardrail is reached first on the Go
    // side: a refused call returns a block and no context.
    const relevant = isMnemosWriteTool(toolName) || isFileEditTool(toolName);
    if (!relevant || invoker.disabled()) return;

    const payload: HookPayload = {
      hook_event_name: "PreToolUse",
      cwd: ctx?.cwd,
      tool_name: toolName,
      tool_input: toolInput as Record<string, unknown>,
      session_id: state.mnemosSessionId,
    };
    const result = await invoker.run(HOOK.preTool, payload, TIMEOUTS.preTool).catch(() => EMPTY);

    if (result.block) {
      return {
        block: true,
        reason: result.block_reason ?? "mnemos: blocked by the write-boundary guardrail.",
      };
    }
  });

  // -------------------------------------------------------- post-tool touch

  pi.on("tool_result", async (event: any, ctx: any) => {
    if (invoker.disabled()) return;
    if (!isFileEditTool(event?.toolName ?? "")) return;

    // Only a call that succeeded touched anything. The harness reports
    // error-vs-success, not change-detection: pi's write tool returns no
    // change indicator, so "succeeded but changed nothing" is not
    // separately observable and is not claimed here.
    if (event?.isError) return;

    const path = filePathFromToolInput(event?.input);
    if (path === "") return;

    const payload: HookPayload = {
      hook_event_name: "PostToolUse",
      cwd: ctx?.cwd,
      tool_name: event?.toolName,
      tool_input: { file_path: path },
      session_id: state.mnemosSessionId,
    };
    // Side-effect only: stdout is intentionally ignored, and the
    // subcommand's neutral envelope carries nothing.
    await invoker.run(HOOK.postTool, payload, TIMEOUTS.touch).catch(() => EMPTY);
  });

  // ----------------------------------------------------------- session end

  pi.on("session_shutdown", async (_event: any, ctx: any) => {
    if (invoker.disabled()) return;
    const payload: HookPayload = {
      hook_event_name: "SessionEnd",
      cwd: ctx?.cwd,
      reason: "session_shutdown",
      session_id: state.mnemosSessionId,
    };
    await invoker.run(HOOK.sessionEnd, payload, TIMEOUTS.sessionEnd).catch(() => EMPTY);
    // Release session-scoped state. The handler is idempotent: a second
    // call with an already-empty state is a no-op, which matters because
    // session replacement can fire this more than once.
    state = emptyState();
  });
}
/**
 * Decides what a single turn's system-prompt push carries.
 *
 * Pure, and named, because two invariants live here and both are easy to
 * break silently: prewarm goes out on exactly one turn (it must not be
 * re-sent every turn, and it must not be skipped entirely on a turn that
 * happens to have no prompt context), and the per-prompt context is
 * dropped when empty rather than joined as a blank line.
 *
 * `prewarmDelivered` in the result is true only when this turn actually
 * carried the block — marking it delivered on the strength of some other
 * fragment would lose prewarm for the whole session.
 */
export function planPromptPush(
  prewarm: string,
  alreadyDelivered: boolean,
  promptContext: string,
): { fragments: string[]; prewarmDelivered: boolean } {
  const fragments: string[] = [];
  let prewarmDelivered = alreadyDelivered;
  if (!alreadyDelivered && prewarm !== "") {
    fragments.push(prewarm);
    prewarmDelivered = true;
  }
  if (promptContext !== "") fragments.push(promptContext);
  return { fragments, prewarmDelivered };
}
