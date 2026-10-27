import { test } from "node:test";
import assert from "node:assert/strict";

import mnemosPi from "./index.ts";
import type { PiApi } from "./hook-transport.ts";
import { isMnemosWriteTool } from "./tool-match.ts";

function harness() {
  const handlers = new Map<string, (...args: any[]) => any>();
  const calls: string[][] = [];
  let session = 0;
  const pi: PiApi = {
    on(event, handler) { handlers.set(event, handler); },
    async exec(command, args) {
      if (args[0] === "config") return { stdout: '{"protocol_version":1,"disabled":false,"continuity":{"enabled":false}}', stderr: "", code: 0, killed: false };
      calls.push([command, ...args]);
      const stdout = args[0] === "prewarm"
        ? JSON.stringify({ session_id: `S${++session}`, text: `PREWARM-${session}` })
        : '{"block":false}';
      return { stdout, stderr: "", code: 0, killed: false };
    },
  };
  mnemosPi(pi);
  return {
    calls,
    fire(event: string, input: any) {
      return handlers.get(event)?.(input, { cwd: "D:/sandbox/mnemos" });
    },
  };
}

test("versioned save metadata reaches the prefixed write guardrail unchanged", async () => {
  const h = harness();
  await h.fire("session_start", { reason: "startup" });
  h.calls.length = 0;
  const input = {
    title: "修订数据库规则", content: "New rule with an explicit predecessor", type: "convention",
    project: "mnemos", topic_key: "architecture/database", replaces_id: "expected-live-ID",
    unpin_replacement: true,
    evidence: [{ locator: "repo/db.go", relation: "supports", revision: "commit", selector: "lines:10-20", fingerprint: "ab".repeat(32) }],
  };
  for (const toolName of ["mnemos_save", "mcp__mnemos_mnemos_save", "mcp__kls-mnemos-pi__mnemos_mnemos_save"]) {
    assert.equal(isMnemosWriteTool(toolName), true);
    assert.equal(await h.fire("tool_call", { toolName, input }), undefined);
    const invocation = h.calls.at(-1)!;
    const payload = JSON.parse(invocation[invocation.indexOf("--payload") + 1]);
    assert.equal(payload.tool_name, toolName);
    assert.deepEqual(payload.tool_input, input);
    assert.equal(invocation.includes("--format"), true);
  }
});

test("new detail, timeline and evidence-marker tools do not create extra hook pushes", async () => {
  const h = harness();
  for (const tool of ["mnemos_get_many", "mnemos_neighborhood", "mnemos_evidence_check"]) {
    for (const prefix of ["", "mcp__mnemos_", "mcp__kls-mnemos-pi__mnemos_"]) {
      const toolName = prefix + tool;
      assert.equal(isMnemosWriteTool(toolName), false);
      assert.equal(await h.fire("tool_call", { toolName, input: {} }), undefined);
      assert.equal(await h.fire("tool_result", { toolName, input: {}, isError: false }), undefined);
    }
  }
  assert.equal(h.calls.length, 0);
});

test("every pi session-start reason gets one fresh non-accumulating prewarm", async () => {
  const h = harness();
  let number = 0;
  for (const reason of ["startup", "reload", "new", "resume", "fork"]) {
    number++;
    await h.fire("session_start", { reason });
    const first = await h.fire("before_agent_start", { prompt: "", systemPrompt: "BASE" });
    assert.ok(first.systemPrompt.startsWith(`BASE\nPREWARM-${number}\nmnemos_session_id: S${number}\nmnemos_host_session_id: `));
    assert.equal(await h.fire("before_agent_start", { prompt: "", systemPrompt: "BASE" }), undefined);
  }
});
