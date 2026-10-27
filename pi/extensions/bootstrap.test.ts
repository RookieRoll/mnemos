import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { readClientSettings, readPackageRoute } from "./bootstrap.ts";
import mnemosPi from "./index.ts";
import type { PiApi } from "./hook-transport.ts";

const ok = (value: unknown) => ({ stdout: JSON.stringify(value), stderr: "", code: 0, killed: false });
const settings = (disabled = false, enabled = false) => ({ protocol_version: 1, disabled, continuity: { enabled } });

function fixture(t: any) {
  const root = mkdtempSync(join(tmpdir(), "mnemos-pi-bootstrap-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const manifest = join(root, "mcp.json"), config = join(root, "config with spaces.toml");
  const entry = (command = "mnemos", args: unknown = ["--config", config, "serve"]) =>
    writeFileSync(manifest, JSON.stringify({ mcpServers: { mnemos: { command, args, directTools: true, toolPrefix: "mcp" } } }));
  entry();
  return { root, manifest, config, entry };
}

test("bootstrap accepts one direct file route, including paths with spaces", (t) => {
  const f = fixture(t), command = resolve(f.root, "binary with spaces", "mnemos.exe");
  for (const args of [["serve"], ["--config", f.config, "serve"], ["serve", "--config", f.config], [`--config=${f.config}`, "serve"]]) {
    f.entry(command, args);
    assert.deepEqual(readPackageRoute(f.manifest), { command, configArgs: args.length === 1 ? [] : ["--config", f.config] });
  }
});

test("bootstrap rejects cwd-dependent routes, duplicate selectors and shell wrappers", (t) => {
  const f = fixture(t);
  for (const command of ["./mnemos", "../bin/mnemos", "bin\\mnemos.exe", "mnemos --config bad", "npx", "bash", "-mnemos", "", "x\0y"]) {
    f.entry(command); assert.throws(() => readPackageRoute(f.manifest), Error, command);
  }
  for (const args of [null, "serve", [], ["serve", "--http", ":8080"], ["hook"], ["--config", "relative.toml", "serve"],
    ["serve", "--config"], ["--config=", "serve"], ["--config", f.config, "serve", "--config", f.config], ["serve", 1]]) {
    f.entry("mnemos", args); assert.throws(() => readPackageRoute(f.manifest), Error, JSON.stringify(args));
  }
  writeFileSync(f.manifest, "not json"); assert.throws(() => readPackageRoute(f.manifest));
});

test("bootstrap rejects adapter environment overrides that split hook and MCP routing", (t) => {
  const f = fixture(t);
  for (const key of ["HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "PATH", "Path"]) {
    f.entry("mnemos", ["serve"]);
    const value = JSON.parse(readFileSync(f.manifest, "utf8"));
    value.mcpServers.mnemos.env = { [key]: "different-route" };
    writeFileSync(f.manifest, JSON.stringify(value));
    assert.throws(() => readPackageRoute(f.manifest), /cannot override home or executable search paths/, key);
  }
});

test("projection is strict, bounded, nonsecret and uses the exact selected route", async () => {
  const route = { command: "chosen-mnemos", configArgs: ["--config", resolve("config with spaces.toml")] };
  const calls: string[][] = [];
  const pi: PiApi = { on() {}, async exec(command, args) { calls.push([command, ...args]); return ok(settings()); } };
  assert.deepEqual(await readClientSettings(pi, route, 1000), settings());
  assert.deepEqual(calls, [[route.command, ...route.configArgs, "config", "--client", "pi"]]);
  for (const bad of [null, [], {}, settings("false" as any), { ...settings(), protocol_version: 2 },
    { ...settings(), continuity: { enabled: 1 } }, { ...settings(), api_key: "must never be projected" },
    { ...settings(), continuity: { enabled: false, extra: true } }]) {
    const stub: PiApi = { on() {}, async exec() { return ok(bad); } };
    await assert.rejects(readClientSettings(stub, route, 1000));
  }
  for (const result of [{ ...ok(settings()), code: 1 }, { ...ok(settings()), killed: true },
    { ...ok(settings()), stdout: "malformed" }, { ...ok(settings()), stdout: " ".repeat(4097) }]) {
    await assert.rejects(readClientSettings({ on() {}, async exec() { return result; } }, route, 1000));
  }
});

test("former product variables cannot change file switches or the executable", async (t) => {
  const f = fixture(t), saved = new Map<string, string | undefined>();
  for (const [key, value] of Object.entries({ MNEMOS_BIN: "wrong-executable", MNEMOS_CONFIG: "wrong.toml", MNEMOS_DISABLED: "1", MNEMOS_CONTINUITY: "1" })) {
    saved.set(key, process.env[key]); process.env[key] = value;
  }
  t.after(() => { for (const [key, value] of saved) { if (value === undefined) delete process.env[key]; else process.env[key] = value; } });
  const handlers = new Map<string, any>(), calls: string[][] = [];
  mnemosPi({ on(name, handler) { handlers.set(name, handler); }, async exec(command, args) {
    calls.push([command, ...args]);
    return args.includes("--client") ? ok(settings()) : ok({ session_id: "FILE", text: "file prewarm" });
  } }, f.manifest);
  await handlers.get("session_start")({}, { cwd: f.root });
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0], ["mnemos", "--config", f.config, "config", "--client", "pi"]);
  assert.equal(calls[1][3], "prewarm");
  assert.equal(calls.some(call => call.includes("continuity")), false);
});

test("disabled or invalid projection never performs automatic writes or guessed startup", async (t) => {
  const f = fixture(t);
  for (const projection of [ok(settings(true, true)), { ...ok(settings()), code: 1 }, ok({ protocol_version: 2 })]) {
    const handlers = new Map<string, any>(), calls: string[][] = [];
    mnemosPi({ on(name, handler) { handlers.set(name, handler); }, async exec(_command, args) { calls.push(args); return projection; } }, f.manifest);
    for (const event of ["session_start", "before_agent_start", "tool_call", "tool_result", "session_shutdown"]) {
      await handlers.get(event)({ prompt: "progress", toolName: "edit", input: { path: "x.ts" } }, { cwd: f.root });
    }
    assert.equal(calls.length, 1, "only the read-only projection can execute");
  }
});

test("pending and late configuration cannot affect the next generation's file route", async (t) => {
  const f = fixture(t), handlers = new Map<string, any>(), calls: string[][] = [];
  let finish!: (value: ReturnType<typeof ok>) => void;
  const pending = new Promise<ReturnType<typeof ok>>(r => { finish = r; });
  let projections = 0;
  mnemosPi({ on(name, handler) { handlers.set(name, handler); }, async exec(command, args) {
    calls.push([command, ...args]);
    if (args.includes("--client")) return ++projections === 1 ? pending : ok(settings());
    return args.includes("prewarm") ? ok({ session_id: "NEW", text: "NEW-CONTEXT" }) : ok({});
  } }, f.manifest);
  const fire = (name: string, value = {}) => handlers.get(name)(value, { cwd: f.root });
  const old = fire("session_start");
  await new Promise(r => setImmediate(r));
  await fire("tool_call", { toolName: "edit", input: { path: "x.ts" } });
  assert.equal(calls.length, 1, "pending configuration suppresses automatic events");
  f.entry("new-mnemos");
  await fire("session_start");
  finish(ok(settings(true, true))); await old;
  const pushed = await fire("before_agent_start", { prompt: "inspect", systemPrompt: "BASE" });
  const prewarm = calls.find(c => c[0] === "new-mnemos" && c.includes("prewarm"))!;
  const host = JSON.parse(prewarm[prewarm.indexOf("--payload") + 1]).session_id;
  assert.equal(pushed.systemPrompt, `BASE\nNEW-CONTEXT\nmnemos_session_id: NEW\nmnemos_host_session_id: ${host}`);
  assert.equal(calls.at(-1)![0], "new-mnemos");
  assert.equal(JSON.parse(calls.at(-1)![calls.at(-1)!.indexOf("--payload") + 1]).session_id, "NEW");
});
