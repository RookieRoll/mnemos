// Explicit, model-free integration gate. Not registered with CI or node --test.
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';

const flags = new Map();
for (let index = 2; index < process.argv.length; index += 2) {
  assert.ok(process.argv[index].startsWith('--') && process.argv[index + 1], 'arguments must be --name value pairs');
  flags.set(process.argv[index].slice(2), process.argv[index + 1]);
}
const option = (name, env) => {
  const value = flags.get(name) ?? process.env[env];
  assert.ok(value, `supply --${name} or ${env}`);
  assert.ok(!/^[a-z][a-z0-9+.-]*:\/\//i.test(value) && !/^(?:npx|npm:|jsr:)/i.test(value), `${name} must be an existing local filesystem path`);
  assert.ok(!/^(?:\\\\|\/\/)/.test(value), `${name} must not use a network share`);
  return resolve(value);
};
const sdkPath = option('sdk', 'PI_VERIFY_SDK');
const packagePath = option('package', 'PI_VERIFY_PACKAGE');
const adapterPath = option('adapter', 'PI_VERIFY_ADAPTER');
const binary = option('binary', 'PI_VERIFY_BINARY');
for (const path of [sdkPath, packagePath, adapterPath]) assert.ok(statSync(path).isDirectory(), 'SDK/package/adapter must be local directories');
assert.ok(statSync(binary).isFile(), 'candidate binary must be a local file');
const tempParent = resolve(flags.get('temp-parent') ?? tmpdir());
const fixture = mkdtempSync(join(tempParent, 'mnemos-pi-schema-'));
const home = join(fixture, 'home'), agentDir = join(fixture, 'agent');
const cwd = join(fixture, 'workspace'), isolatedPackage = join(fixture, 'mnemos-package');
for (const path of [home, agentDir, cwd]) mkdirSync(path, { recursive: true });
// Copy only package runtime files, never user settings, models, credentials, or DBs.
mkdirSync(isolatedPackage);
for (const name of ['package.json', 'extensions', 'skills']) cpSync(join(packagePath, name), join(isolatedPackage, name), { recursive: true });
const sha256 = path => createHash('sha256').update(readFileSync(path)).digest('hex');
const copiedTypeScript = [];
function compareTypeScript(subdirectory) {
  for (const entry of readdirSync(join(packagePath, subdirectory), { withFileTypes: true })) {
    const name = join(subdirectory, entry.name);
    if (entry.isDirectory()) compareTypeScript(name);
    else if (entry.isFile() && name.endsWith('.ts')) {
      const sourceSHA256 = sha256(join(packagePath, name)), copySHA256 = sha256(join(isolatedPackage, name));
      assert.equal(copySHA256, sourceSHA256, `copied TypeScript differs: ${name}`);
      copiedTypeScript.push({ file: name, sourceSHA256, copySHA256 });
    }
  }
}
compareTypeScript('extensions');
assert.ok(copiedTypeScript.length > 0, 'package must contain actual extension TypeScript');
writeFileSync(join(isolatedPackage, 'mcp.json'), JSON.stringify({ mcpServers: {
  mnemos: { command: binary, args: ['serve'], directTools: true, toolPrefix: 'mcp' },
} }, null, 2));
const manifest = JSON.parse(readFileSync(join(isolatedPackage, 'package.json'), 'utf8'));
assert.equal(manifest.pi.mcp, './mcp.json', 'package must use its isolated MCP manifest');
assert.deepEqual(manifest.pi.extensions, ['./extensions'], 'package extensions must remain local to its copy');
assert.deepEqual(manifest.pi.skills, ['./skills'], 'package skills must remain local to its copy');
const serverName = `${manifest.name}__mnemos`;
const preservedEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
  ['PATH', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT'].includes(key.toUpperCase())));
for (const key of Object.keys(process.env)) delete process.env[key];
Object.assign(process.env, preservedEnv, {
  HOME: home, USERPROFILE: home, LOCALAPPDATA: join(home, 'LocalAppData'), TMP: fixture, TEMP: fixture,
  PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: '1', MNEMOS_CONTINUITY: '1', MNEMOS_BIN: binary,
  MNEMOS_CONFIG: join(fixture, 'mnemos.toml'), MCP_DIRECT_TOOLS: serverName,
  PI_MCP_ADAPTER_TEST_AUTH_STORE: 'memory', PI_MCP_ADAPTER_DISABLE_AUTH_CACHE: '1',
});
writeFileSync(process.env.MNEMOS_CONFIG, `[storage]\npath = ${JSON.stringify(join(fixture, 'mnemos.db').replaceAll('\\', '/'))}\n[embedding]\nprovider = "none"\n`);
const candidateSHA256 = sha256(binary);
const candidateVersion = execFileSync(binary, ['--version'], { encoding: 'utf8', windowsHide: true, timeout: 15000 }).trim();
const parsedMCP = JSON.parse(readFileSync(join(isolatedPackage, 'mcp.json'), 'utf8')).mcpServers.mnemos;
assert.equal(parsedMCP.command, binary);
const provenance = { candidateBinary: binary, candidateSHA256, candidateVersion, packageVersion: manifest.version,
  packageSource: packagePath, isolatedPackage, copiedTypeScript, mcpCommand: parsedMCP.command, mcpArgs: parsedMCP.args };
writeFileSync(join(fixture, 'provenance.json'), JSON.stringify(provenance, null, 2));
const git = args => execFileSync('git', ['-C', cwd, ...args], { windowsHide: true, timeout: 30000 });
git(['init', '--quiet']);
writeFileSync(join(cwd, 'fixture.txt'), 'Synthetic checkpoint schema verification.\n');
git(['add', 'fixture.txt']);
git(['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false', '-c', `core.hooksPath=${fixture}`, 'commit', '--quiet', '-m', 'Synthetic fixture']);
process.chdir(cwd);
const settings = { packages: [adapterPath, isolatedPackage], extensions: ['-builtin:mcp'] };
writeFileSync(join(agentDir, 'settings.json'), JSON.stringify(settings));
globalThis.fetch = async () => { throw new Error('network/model access prohibited in checkpoint schema verification'); };
const sdk = await import(pathToFileURL(join(sdkPath, 'dist/index.js')).href);
const { emitSessionShutdownEvent } = await import(pathToFileURL(join(sdkPath, 'dist/core/extensions/runner.js')).href);
const credentials = { read: async () => undefined, list: async () => [],
  modify: async () => { throw new Error('credential writes prohibited'); }, delete: async () => { throw new Error('credential writes prohibited'); } };
const modelRuntime = await sdk.ModelRuntime.create({ credentials, modelsPath: null,
  modelsStorePath: join(fixture, 'empty-model-cache.json'), allowModelNetwork: false, refreshOnCreate: false });
const settingsManager = sdk.SettingsManager.inMemory(settings);
let modelRequests = 0, adapterDirectExecutions = 0;
const loader = new sdk.DefaultResourceLoader({ cwd, agentDir, settingsManager, disabledBuiltinExtensions: ['mcp'],
  noPromptTemplates: true, noThemes: true, noContextFiles: true,
  extensionFactories: [pi => { pi.on('before_provider_request', () => { modelRequests++; throw new Error('model requests prohibited'); }); }] });
const model = { id: 'never-invoked', name: 'No model', provider: 'fixture', api: 'openai-completions', baseUrl: 'http://127.0.0.1:1',
  reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 8192, maxTokens: 128 };
let session;
const transcript = [];
const timeout = setTimeout(() => { console.error(`Verification timed out; fixture: ${fixture}`); process.exit(1); }, 120000);

function unwrap(result) {
  const candidates = [result.structuredContent, result.details];
  for (const content of result.content ?? []) if (content.type === 'text') {
    // Classic direct tools render MCP isError and structuredContent using these
    // explicit adapter prefixes; deferred tools retain structuredContent above.
    const text = content.text.replace(/^(?:Error:\s*|structuredContent:\s*)/, '');
    try { candidates.push(JSON.parse(text)); } catch { /* adapter diagnostics are not protocol results */ }
  }
  for (const candidate of candidates) if (candidate?.protocol_version === 1) return candidate;
  throw new Error('actual adapter tool result lacked a continuity v1 envelope');
}
function dereference(schema, root) {
  if (!schema?.$ref) return schema;
  assert.ok(schema.$ref.startsWith('#/'), 'only local schema references are supported');
  return dereference(schema.$ref.slice(2).split('/').reduce((value, key) => value[key.replaceAll('~1', '/').replaceAll('~0', '~')], root), root);
}
try {
  await loader.reload();
  assert.deepEqual(loader.getExtensions().errors, []);
  const loadedPaths = loader.getExtensions().extensions.map(extension => resolve(extension.resolvedPath));
  const isWithin = (path, directory) => path.startsWith(resolve(directory) + (process.platform === 'win32' ? '\\' : '/'));
  assert.ok(loadedPaths.some(path => isWithin(path, isolatedPackage)), 'isolated mnemos extension was not loaded');
  assert.ok(loadedPaths.some(path => isWithin(path, adapterPath)), 'requested adapter was not loaded');
  ({ session } = await sdk.createAgentSession({ cwd, agentDir, modelRuntime, model, settingsManager,
    resourceLoader: loader, sessionManager: sdk.SessionManager.inMemory(cwd), noTools: 'builtin' }));
  const errors = [];
  await session.bindExtensions({ mode: 'rpc', onError: error => errors.push(error) });
  const required = ['mnemos_session_start', 'mnemos_checkpoint', 'mnemos_resume_context'];
  const deadline = Date.now() + 30000;
  while (!required.every(suffix => session.getAllTools().some(tool => tool.name.endsWith(suffix)))) {
    assert.ok(Date.now() < deadline, 'actual adapter tool discovery timed out'); await delay(100);
  }
  assert.deepEqual(errors, []);
  const definitions = required.map(suffix => {
    const tool = session.getAllTools().find(tool => tool.name.endsWith(suffix));
    assert.ok(session.getActiveToolNames().includes(tool.name), `${suffix} is not visible to the agent`);
    return [suffix, tool];
  });
  const tools = new Map(definitions);
  const schema = tools.get('mnemos_checkpoint').parameters;
  writeFileSync(join(fixture, 'agent-checkpoint-schema.json'), JSON.stringify(schema, null, 2));
  const payloadSchema = dereference(schema.properties.payload, schema);
  assert.ok(payloadSchema?.properties?.summary, 'expected checkpoint payload object schema');
  assert.ok(!('workspace' in payloadSchema.properties), 'server workspace leaked into agent checkpoint input properties');
  assert.ok(!(payloadSchema.required ?? []).includes('workspace'), 'server workspace is required in agent checkpoint input');
  const call = async (suffix, input) => {
    const name = tools.get(suffix).name;
    // Execute the adapter's registered direct tool: real stdio MCP round-trip,
    // not a fake model stream or a replacement memory tool.
    adapterDirectExecutions++;
    const result = await session.getToolDefinition(name).execute(randomUUID(), input, new AbortController().signal);
    const body = unwrap(result); transcript.push({ tool: name, input, result: body }); return body;
  };
  const scope = { protocol_version: 1, cwd, agent_id: 'schema-fixture' };
  const open = async key => {
    const result = await call('mnemos_session_start', { agent_id: scope.agent_id, continuity: {
      ...scope, harness_kind: 'pi', start_key: key,
    } });
    assert.equal(result.status, 'applied'); return result.session_id;
  };
  const a = await open('schema-owner'), b = await open('schema-reader');
  assert.notEqual(a, b);
  const payload = { task: 'schema-fixture', state: 'active', summary: 'Synthetic work is ready for continuation.', next_steps: ['Run synthetic verification.'] };
  const save = { ...scope, session_id: a, action: 'save', expected_revision: 0, request_id: randomUUID(), visibility: 'workspace', payload };
  const first = await call('mnemos_checkpoint', save);
  assert.equal(first.status, 'applied'); assert.equal(first.revision, 1);
  const resume = () => call('mnemos_resume_context', { ...scope, session_id: b, source_session_id: a, resume_intent: true });
  const original = await resume();
  assert.equal(original.status, 'selected'); assert.equal(original.source.revision, 1);
  assert.equal(original.source.payload.workspace.known, true);
  assert.ok(original.source.payload.workspace.head, 'server-sampled workspace was lost from output');
  const forged = await call('mnemos_checkpoint', { ...save, expected_revision: 1, request_id: randomUUID(),
    payload: { ...payload, workspace: { known: true, branch: 'forged', head: 'forged' } } });
  assert.equal(forged.code, 'validation');
  const unchanged = await resume();
  assert.deepEqual(unchanged.source, original.source, 'rejected metadata changed existing checkpoint/revision');
  const replay = await call('mnemos_checkpoint', save);
  assert.equal(replay.status, 'replayed'); assert.equal(replay.revision, 1); assert.equal(replay.updated_at, first.updated_at);
  const second = await call('mnemos_checkpoint', { ...save, expected_revision: 1, request_id: randomUUID(), payload: { ...payload, summary: 'Synthetic normal update.' } });
  assert.equal(second.status, 'applied'); assert.equal(second.revision, 2);
  assert.equal((await resume()).source.payload.summary, 'Synthetic normal update.');
  const clear = { ...scope, session_id: a, action: 'clear', expected_revision: 2, request_id: randomUUID() };
  const cleared = await call('mnemos_checkpoint', clear);
  assert.equal(cleared.status, 'applied'); assert.equal(cleared.revision, 3);
  const clearedContext = await resume();
  assert.equal(clearedContext.status, 'cleared');
  assert.ok(!clearedContext.source?.payload, 'clear still exposes the former checkpoint body');
  const clearReplay = await call('mnemos_checkpoint', clear);
  assert.equal(clearReplay.status, 'replayed'); assert.equal(clearReplay.revision, 3); assert.equal(clearReplay.updated_at, cleared.updated_at);
  assert.equal(session.messages.length, 0, 'verification unexpectedly created model conversation messages');
  assert.equal(modelRequests, 0);
  writeFileSync(join(fixture, 'transcript.json'), JSON.stringify(transcript, null, 2));
  console.log(JSON.stringify({ ok: true, fixture, ...provenance, adapter: adapterPath,
    modelRequests, adapterDirectExecutions, usedAuth: false, schemaWithoutWorkspace: true,
    forgedMetadataRejectedWithoutMutation: true, saveReplayClear: true }, null, 2));
} catch (error) {
  console.error(`Verification failed; isolated fixture retained at ${fixture}`);
  throw error;
} finally {
  writeFileSync(join(fixture, 'transcript.json'), JSON.stringify(transcript, null, 2));
  if (session) { await emitSessionShutdownEvent(session.extensionRunner, { type: 'session_shutdown', reason: 'quit' }); session.dispose(); }
  clearTimeout(timeout);
}
