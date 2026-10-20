import { test } from "node:test";
import assert from "node:assert/strict";

import {
  EMPTY,
  createSpawnInvoker,
  parseHookResult,
  sessionIdOf,
  type PiApi,
} from "./hook-transport.ts";

/** A PiApi stub that records calls and returns whatever it was told to. */
function stubPi(outcome: { stdout?: string; throw?: Error } = {}): PiApi & { calls: string[][] } {
  const calls: string[][] = [];
  return {
    calls,
    on() {},
    async exec(command, args) {
      calls.push([command, ...args]);
      if (outcome.throw) throw outcome.throw;
      return { stdout: outcome.stdout ?? "", stderr: "", code: 0, killed: false };
    },
  };
}

test("a canned envelope is parsed", () => {
  assert.deepEqual(parseHookResult('{"context":"x","block":false}'), {
    context: "x",
    block: false,
  });
});

test("malformed, empty, and non-object output all degrade to the empty result", () => {
  // None of these may throw: a hook that throws surfaces as a failed turn.
  for (const bad of ["", "   ", "not json", "[1,2]", "null", '"a string"', "42"]) {
    assert.deepEqual(parseHookResult(bad), EMPTY, `input ${JSON.stringify(bad)}`);
  }
});

test("the payload travels as an argument because pi.exec has no stdin", async () => {
  const pi = stubPi({ stdout: '{"block":false}' });
  const invoker = createSpawnInvoker(pi, { command: "mnemos", configArgs: [] });
  await invoker.run(["hook", "pre-tool"], { hook_event_name: "PreToolUse", tool_name: "edit" }, 5000);

  assert.equal(pi.calls.length, 1);
  const argv = pi.calls[0];
  assert.equal(argv[0], "mnemos");
  assert.equal(argv[1], "hook");
  assert.equal(argv[2], "pre-tool");
  assert.equal(argv[3], "--payload");
  assert.deepEqual(JSON.parse(argv[4]), { hook_event_name: "PreToolUse", tool_name: "edit" });
  assert.equal(argv[5], "--format");
  assert.equal(argv[6], "json");
});

test("a spawn failure degrades to the empty result rather than throwing", async () => {
  const invoker = createSpawnInvoker(stubPi({ throw: new Error("ENOENT") }), { command: "mnemos", configArgs: [] });
  assert.deepEqual(await invoker.run(["hook", "pre-tool"], {}, 1000), EMPTY);
});

test("a non-JSON response degrades to the empty result", async () => {
  const invoker = createSpawnInvoker(stubPi({ stdout: "mnemos: something broke\n" }), { command: "mnemos", configArgs: [] });
  assert.deepEqual(await invoker.run(["hook", "pre-tool"], {}, 1000), EMPTY);
});

test("the explicit file route carries an absolute selector to hooks", async () => {
  const pi = stubPi({ stdout: "{}" });
  const invoker = createSpawnInvoker(pi, { command: "/opt/mnemos", configArgs: ["--config", "/config with spaces/mnemos.toml"] });
  await invoker.run(["hook", "pre-tool"], {}, 1000);
  assert.deepEqual(pi.calls[0].slice(0, 5), ["/opt/mnemos", "--config", "/config with spaces/mnemos.toml", "hook", "pre-tool"]);
});

test("a file-disabled invoker suppresses both hook and prewarm", async () => {
  const pi = stubPi();
  const invoker = createSpawnInvoker(pi, { command: "mnemos", configArgs: [] }, true);
  assert.equal(invoker.disabled(), true);
  assert.deepEqual(await invoker.run(["hook", "pre-tool"], {}, 1000), EMPTY);
  assert.deepEqual(await invoker.prewarm({}, 1000), EMPTY);
  assert.equal(pi.calls.length, 0);
});

test("session id extraction tolerates every non-string shape", () => {
  assert.equal(sessionIdOf({ session_id: "abc" }), "abc");
  assert.equal(sessionIdOf({ session_id: "" }), "");
  assert.equal(sessionIdOf({}), "");
  assert.equal(sessionIdOf({ session_id: 42 as unknown as string }), "");
});
