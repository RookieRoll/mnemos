// The package MCP entry is the single executable/config route for both the
// adapter and hooks. Feature switches remain in TOML, parsed by mnemos itself.
import { readFileSync } from "node:fs";
import { isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";
import type { PiApi } from "./hook-transport.ts";

/** An unambiguous route shared by MCP and automatic integration. */
export interface MnemosRoute {
  command: string;
  configArgs: string[];
}

/** The complete nonsecret version-1 configuration projection. */
export interface ClientSettings {
  protocol_version: 1;
  disabled: boolean;
  continuity: { enabled: boolean };
}

/** Reads this installed package's route without consulting product variables. */
export function readPackageRoute(path = fileURLToPath(new URL("../mcp.json", import.meta.url))): MnemosRoute {
  const entry = JSON.parse(readFileSync(path, "utf8"))?.mcpServers?.mnemos;
  if (!entry || typeof entry !== "object" || Array.isArray(entry)) throw new Error("mcp.json must contain mcpServers.mnemos");
  // The adapter starts its server with entry.env, while pi.exec inherits the
  // host environment. These overrides can make an implicit config or bare
  // executable resolve differently across the two launch paths.
  const routeEnvironment = new Set(["HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "PATH"]);
  if (entry.env && typeof entry.env === "object" && !Array.isArray(entry.env) &&
    Object.keys(entry.env).some((key) => routeEnvironment.has(key.toUpperCase()))) {
    throw new Error("mcp.json mnemos.env cannot override home or executable search paths; hooks and MCP must inherit the same route environment");
  }
  const command = entry.command;
  if (typeof command !== "string" || !command || command.trim() !== command || command.includes("\0") ||
    (!isAbsolute(command) && !/^[A-Za-z0-9_.-]+$/.test(command)) || command.startsWith("-")) {
    throw new Error("mcp.json mnemos.command must be an absolute executable path or bare program name; run mnemos init to normalize relative paths");
  }
  // Shell wrappers cannot share the executable/config route with direct hooks.
  if (/(?:^|[\\/])(?:cmd|powershell|pwsh|sh|bash|zsh|npx|npm)(?:\.exe)?$/i.test(command) || /\.(?:cmd|bat|ps1|sh)$/i.test(command)) {
    throw new Error("mcp.json mnemos.command must invoke mnemos directly, without a shell wrapper");
  }
  if (!Array.isArray(entry.args) || !entry.args.every((value: unknown) => typeof value === "string" && !value.includes("\0"))) {
    throw new Error("mcp.json mnemos.args must contain serve and at most one explicit --config selector");
  }
  const words: string[] = [];
  let configPath: string | undefined;
  for (let index = 0; index < entry.args.length; index++) {
    const arg: string = entry.args[index];
    if (arg === "--config" || arg.startsWith("--config=")) {
      if (configPath !== undefined) throw new Error("mcp.json has ambiguous duplicate --config selectors");
      configPath = arg === "--config" ? entry.args[++index] : arg.slice("--config=".length);
      if (!configPath || !isAbsolute(configPath) || configPath.includes("\0")) {
        throw new Error("mcp.json --config must select an absolute file; run mnemos init to normalize relative paths");
      }
    } else words.push(arg);
  }
  if (words.length !== 1 || words[0] !== "serve") {
    throw new Error("mcp.json mnemos.args supports only serve and one explicit --config selector");
  }
  return { command, configArgs: configPath === undefined ? [] : ["--config", configPath] };
}

/** Loads only validated adapter switches, without parsing TOML or opening a DB. */
export async function readClientSettings(pi: PiApi, route: MnemosRoute, timeout: number): Promise<ClientSettings> {
  const result = await pi.exec(route.command, [...route.configArgs, "config", "--client", "pi"], { timeout });
  if (result.killed || result.code !== 0 || Buffer.byteLength(result.stdout, "utf8") > 4096) {
    throw new Error("mnemos config --client pi failed; check the installed executable and selected configuration file");
  }
  let value: any;
  try { value = JSON.parse(result.stdout); } catch { throw new Error("mnemos config --client pi returned invalid JSON"); }
  if (!value || typeof value !== "object" || Array.isArray(value) ||
    Object.keys(value).sort().join(",") !== "continuity,disabled,protocol_version" ||
    value.protocol_version !== 1 || typeof value.disabled !== "boolean" ||
    !value.continuity || typeof value.continuity !== "object" || Array.isArray(value.continuity) ||
    Object.keys(value.continuity).join(",") !== "enabled" || typeof value.continuity.enabled !== "boolean") {
    throw new Error("mnemos config --client pi returned an invalid version-1 projection");
  }
  return value as ClientSettings;
}
