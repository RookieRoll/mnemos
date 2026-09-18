import { test } from "node:test";
import assert from "node:assert/strict";

import {
  EMPTY,
  createSpawnInvoker,
  isDisabled,
  mnemosCommand,
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
  const invoker = createSpawnInvoker(pi, {});
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
  const invoker = createSpawnInvoker(stubPi({ throw: new Error("ENOENT") }), {});
  assert.deepEqual(await invoker.run(["hook", "pre-tool"], {}, 1000), EMPTY);
});

test("a non-JSON response degrades to the empty result", async () => {
  const invoker = createSpawnInvoker(stubPi({ stdout: "mnemos: something broke\n" }), {});
  assert.deepEqual(await invoker.run(["hook", "pre-tool"], {}, 1000), EMPTY);
});

test("MNEMOS_BIN overrides the binary without touching PATH", async () => {
  const pi = stubPi({ stdout: "{}" });
  const invoker = createSpawnInvoker(pi, { MNEMOS_BIN: "/opt/mnemos" });
  await invoker.run(["hook", "pre-tool"], {}, 1000);
  assert.equal(pi.calls[0][0], "/opt/mnemos");
});

test("the disable switch silences every invocation", async () => {
  // The stub throws if called, so a single exec would fail the test.
  const pi: PiApi = {
    on() {},
    async exec() {
      throw new Error("no subcommand may run while disabled");
    },
  };
  const invoker = createSpawnInvoker(pi, { MNEMOS_DISABLED: "1" });
  assert.equal(invoker.disabled(), true);
});

test("the disable switch accepts the spellings a user would type", () => {
  for (const value of ["1", "true", "YES", "on", " 1 "]) {
    assert.equal(isDisabled({ MNEMOS_DISABLED: value }), true, `value ${value}`);
  }
  // A stray zero must not silently turn memory off, even though the Go side
  // treats any non-empty value as disabled.
  for (const value of ["", "0", "false", "no", "  "]) {
    assert.equal(isDisabled({ MNEMOS_DISABLED: value }), false, `value ${JSON.stringify(value)}`);
  }
  assert.equal(isDisabled({}), false);
});

test("session id extraction tolerates every non-string shape", () => {
  assert.equal(sessionIdOf({ session_id: "abc" }), "abc");
  assert.equal(sessionIdOf({ session_id: "" }), "");
  assert.equal(sessionIdOf({}), "");
  assert.equal(sessionIdOf({ session_id: 42 as unknown as string }), "");
});

test("the default binary is mnemos on PATH", () => {
  assert.equal(mnemosCommand({}), "mnemos");
  assert.equal(mnemosCommand({ MNEMOS_BIN: "  " }), "mnemos");
  assert.equal(mnemosCommand({ MNEMOS_BIN: " ./mnemos " }), "./mnemos");
});
