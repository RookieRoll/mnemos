"""Isolated behavior/capture runner using only the Python standard library."""
from __future__ import annotations

import argparse
from contextlib import closing
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from typing import Any, Callable
import uuid


MIN_HOST_VERSION = (2, 1, 286)
REQUIRED_HOST_FLAGS = (
    "--print",
    "--bare",
    "--settings",
    "--mcp-config",
    "--strict-mcp-config",
    "--append-system-prompt",
    "--dangerously-skip-permissions",
    "--output-format",
    "--verbose",
)


def isolated_env(home: Path) -> dict[str, str]:
    """Keep host/provider credentials while removing Mnemos routing overrides."""
    env = {key: value for key, value in os.environ.items()
           if not key.upper().startswith("MNEMOS_") and key.upper() != "CLAUDE_CONFIG_DIR"}
    env["HOME"] = str(home)
    env["USERPROFILE"] = str(home)
    env["APPDATA"] = str(home / "AppData" / "Roaming")
    env["LOCALAPPDATA"] = str(home / "AppData" / "Local")
    return env


def source_config_env(scratch_home: Path) -> dict[str, str]:
    """Resolve '~' and home defaults from the caller for explicit source TOML."""
    env = isolated_env(scratch_home)
    for key in ("HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH"):
        if key in os.environ:
            env[key] = os.environ[key]
    return env


def _required_path(value: Any, field: str, *, executable: bool = False) -> Path:
    if not isinstance(value, str) or not value or not Path(value).is_absolute():
        raise ValueError(f"runner config {field} must be an absolute local path")
    if value.startswith(("\\\\", "//")):
        raise ValueError(f"runner config {field} cannot use a network share")
    path = Path(value)
    if not path.is_file():
        raise ValueError(f"runner config {field} does not name an existing file")
    if executable and os.name != "nt" and not os.access(path, os.X_OK):
        raise ValueError(f"runner config {field} is not executable")
    return path.resolve()


def load_runtime(path: str | Path) -> dict[str, Path]:
    """Read explicit local executable/config paths without environment fallback."""
    config_path = Path(path).resolve()
    try:
        value = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read runner config {config_path}: {error}") from error
    required = {"mnemos_binary", "mnemos_config", "claude_binary"}
    if not isinstance(value, dict) or set(value) != required:
        raise ValueError("runner config must contain exactly mnemos_binary, mnemos_config, and claude_binary")
    return {
        "binary": _required_path(value["mnemos_binary"], "mnemos_binary", executable=True),
        "config": _required_path(value["mnemos_config"], "mnemos_config"),
        "host": _required_path(value["claude_binary"], "claude_binary", executable=True),
    }


def _run(command: list[str], *, env: dict[str, str], cwd: Path | None = None,
         capture: bool = True, runner: Callable[..., Any] = subprocess.run) -> Any:
    return runner(command, env=env, cwd=cwd, capture_output=capture, text=True,
                  encoding="utf-8", errors="replace", check=False, timeout=30)


def check_host(host: Path, env: dict[str, str], runner: Callable[..., Any] = subprocess.run) -> str:
    """Fail before model launch unless bare mode and its isolation flags exist."""
    version = _run([str(host), "--version"], env=env, runner=runner)
    if version.returncode != 0:
        raise RuntimeError("cannot read Claude Code version; verify stopped before model launch")
    match = re.search(r"\b(\d+)\.(\d+)\.(\d+)\b", version.stdout + "\n" + version.stderr)
    if not match or tuple(map(int, match.groups())) < MIN_HOST_VERSION:
        raise RuntimeError("Claude Code 2.1.286 or newer is required for isolated bare-mode verification")
    help_result = _run([str(host), "--help"], env=env, runner=runner)
    if help_result.returncode != 0:
        raise RuntimeError("cannot inspect Claude Code flags; verify stopped before model launch")
    missing = [flag for flag in REQUIRED_HOST_FLAGS if flag not in help_result.stdout]
    if missing:
        raise RuntimeError("Claude Code lacks required isolation flags: " + ", ".join(missing))
    return version.stdout.strip() or version.stderr.strip()


def read_source_store(binary: Path, config: Path, env: dict[str, str],
                      runner: Callable[..., Any] = subprocess.run) -> Path:
    """Ask the selected candidate for its resolved store using explicit argv."""
    result = _run([str(binary), "--config", str(config), "config"], env=env, runner=runner)
    if result.returncode != 0:
        raise RuntimeError("candidate could not read the explicitly selected source config")
    try:
        storage = json.loads(result.stdout)["Storage"]
        source = Path(storage["Path"]).resolve()
    except (json.JSONDecodeError, KeyError, TypeError) as error:
        raise RuntimeError("candidate config output did not contain Storage.Path") from error
    return source


def snapshot(source: Path, destination: Path) -> Path:
    """Copy an existing SQLite store read-only, including committed WAL pages."""
    destination.mkdir(parents=True, exist_ok=True)
    target = destination / "mnemos.db"
    if source.is_file():
        uri = source.resolve().as_uri() + "?mode=ro"
        with closing(sqlite3.connect(uri, uri=True)) as live, closing(sqlite3.connect(target)) as copy:
            live.backup(copy)
    return target


def write_scratch_files(root: Path, binary: Path, arm: str) -> tuple[Path, Path, Path, Path]:
    """Create the isolated settings, MCP route, and scratch config for one arm."""
    root.mkdir(parents=True, exist_ok=True)
    scratch_config = root / "config.toml"
    scratch_config.write_text(
        "[storage]\npath = " + json.dumps(str((root / "mnemos.db").resolve())) +
        "\n[embedding]\nprovider = \"none\"\n[integration]\ndisabled = false\n"
        "[continuity]\nenabled = false\n",
        encoding="utf-8",
    )
    settings = root / "settings.json"
    settings.write_text(json.dumps({"disableAllHooks": True}), encoding="utf-8")
    mcp = root / ("mcp_on.json" if arm == "on" else "mcp_off.json")
    servers = {}
    if arm == "on":
        servers["mnemos"] = {"type": "stdio", "command": str(binary),
                             "args": ["--config", str(scratch_config), "serve"]}
    mcp.write_text(json.dumps({"mcpServers": servers}), encoding="utf-8")
    project = root / "mnemos"
    project.mkdir(exist_ok=True)
    return scratch_config, settings, mcp, project


def _mnemos_json(binary: Path, config: Path, command: list[str], payload: dict[str, Any],
                 cwd: Path, env: dict[str, str], runner: Callable[..., Any]) -> dict[str, Any]:
    argv = [str(binary), "--config", str(config), *command, "--payload", json.dumps(payload, ensure_ascii=False)]
    result = _run(argv, env=env, cwd=cwd, runner=runner)
    if result.returncode != 0:
        raise RuntimeError("explicit candidate hook failed; model launch was skipped")
    if result.stdout.strip() == "":
        return {}
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("candidate hook returned invalid JSON; model launch was skipped") from error
    if not isinstance(value, dict):
        raise RuntimeError("candidate hook returned a non-object; model launch was skipped")
    return value


def build_host_command(host: Path, arm: str, trigger: str, settings: Path,
                       mcp: Path, context: str) -> list[str]:
    command = [str(host), "-p", trigger, "--bare", "--settings", str(settings),
               "--mcp-config", str(mcp), "--strict-mcp-config",
               "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"]
    if arm == "on" and context:
        command.extend(["--append-system-prompt", context])
    return command


def launch(runtime: dict[str, Path], arm: str, trigger: str,
           runner: Callable[..., Any] = subprocess.run,
           temp_parent: Path | None = None) -> int:
    """Prepare one isolated arm, then launch the host only after all gates pass."""
    with tempfile.TemporaryDirectory(prefix=f"mnemos-verify-{arm}-", dir=temp_parent) as temporary:
        root = Path(temporary).resolve()
        env = isolated_env(root / "home")
        (root / "home").mkdir()
        host_version = check_host(runtime["host"], env, runner)
        source_store = None
        if arm == "on":
            source_env = source_config_env(root / "source-home")
            source_store = read_source_store(runtime["binary"], runtime["config"], source_env, runner)
            snapshot(source_store, root / "store")
        scratch_config, settings, mcp, project = write_scratch_files(
            root / ("store" if arm == "on" else "off"), runtime["binary"], arm)
        context = ""
        if arm == "on":
            hook_env = isolated_env(root / "home")
            host_session_id = f"mnemos-verify-{uuid.uuid4()}"
            prewarm = _mnemos_json(runtime["binary"], scratch_config,
                                   ["prewarm", "--mode", "session_start", "--format", "json"],
                                   {"hook_event_name": "SessionStart", "cwd": str(project), "session_id": host_session_id},
                                   project, hook_env, runner)
            mnemos_session_id = prewarm.get("session_id")
            if not isinstance(mnemos_session_id, str) or not mnemos_session_id:
                raise RuntimeError("candidate prewarm did not return a session ID; model launch was skipped")
            prompt = _mnemos_json(runtime["binary"], scratch_config,
                                  ["hook", "user-prompt", "--format", "json"],
                                  {"hook_event_name": "UserPromptSubmit", "cwd": str(project),
                                   "session_id": mnemos_session_id, "prompt": trigger},
                                  project, hook_env, runner)
            context = "\n".join(text.strip() for text in
                                  (prewarm.get("text", ""), prompt.get("context", ""))
                                  if isinstance(text, str) and text.strip())
        command = build_host_command(runtime["host"], arm, trigger, settings, mcp, context)
        if runner is subprocess.run:
            print(f"verify runner: Claude Code {host_version}; arm={arm}", file=sys.stderr)
            completed = subprocess.run(command, env=env, cwd=project, check=False)
            return completed.returncode
        completed = runner(command, env=env, cwd=project, capture_output=False,
                           text=True, encoding="utf-8", errors="replace", check=False, timeout=30)
        return completed.returncode


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run an isolated Mnemos behavior/capture arm")
    parser.add_argument("--config", required=True, help="explicit local runner JSON file")
    parser.add_argument("--arm", required=True, choices=("on", "off"))
    parser.add_argument("--trigger", required=True)
    parser.add_argument("--temp-parent", help="existing local directory for disposable state")
    args = parser.parse_args(argv)
    try:
        runtime = load_runtime(args.config)
        temp_parent = Path(args.temp_parent).resolve() if args.temp_parent else None
        if temp_parent is not None and not temp_parent.is_dir():
            raise ValueError("--temp-parent must be an existing directory")
        return launch(runtime, args.arm, args.trigger, temp_parent=temp_parent)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"verify runner: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
