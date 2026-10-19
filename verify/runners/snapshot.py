"""Seed the verify on-arm from a coherent read-only SQLite snapshot."""
import json
from contextlib import closing
import os
from pathlib import Path
import re
import shutil
import sqlite3
import subprocess
import sys


def snapshot(config, destination):
    """Copy the live store without creating or writing it, then emit config."""
    target = Path(destination)
    target.mkdir(parents=True, exist_ok=True)
    source = Path(config["Storage"]["Path"])
    if source.is_file():
        # mode=ro prevents a typo from creating a new live database. The
        # SQLite backup API includes committed WAL pages under concurrent use.
        with closing(sqlite3.connect(source.resolve().as_uri() + "?mode=ro", uri=True)) as live:
            with closing(sqlite3.connect(target / "mnemos.db")) as copy:
                live.backup(copy)
    # JSON strings are also valid TOML basic strings, including Windows paths.
    (target / "config.toml").write_text(
        "[storage]\npath = " + json.dumps(str(target / "mnemos.db")) + "\n",
        encoding="utf-8",
    )


def prepare_runner(destination, binary):
    """Bind MCP and reject old hook binaries that ignore MNEMOS_CONFIG."""
    target = Path(destination).resolve()
    expected = os.path.normcase(str(target / "mnemos.db"))
    env = dict(os.environ, MNEMOS_CONFIG=str(target / "config.toml"))
    executable = shutil.which(binary)
    if executable is None:
        raise RuntimeError("Cannot resolve MNEMOS_BIN before starting verify")
    executable = str(Path(executable).resolve())
    candidates = {executable}
    claude_dir = Path(os.environ.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude")
    settings_path = claude_dir / "settings.json"
    if settings_path.is_file():
        settings = json.loads(settings_path.read_text(encoding="utf-8-sig"))
        for matchers in settings.get("hooks", {}).values():
            for matcher in matchers:
                for hook in matcher.get("hooks", []):
                    command = hook.get("command", "")
                    if not isinstance(command, str) or "mnemos" not in command.lower():
                        continue
                    # Our installer emits a directly invoked binary. Shell wrappers
                    # cannot be proven safe and must be reviewed before a verify run.
                    if re.search(r"[\r\n;&|<>`$%^!]", command):
                        raise RuntimeError("Cannot validate a registered mnemos hook wrapper; verify stopped")
                    match = re.fullmatch(
                        r'''\s*(?:"([^"]+)"|'([^']+)'|(.+?))\s+'''
                        r'''(?:prewarm(?:\s+--mode\s+(?:session_start|compaction_recovery))?|'''
                        r'''hook\s+(?:user-prompt|post-tool|session-end|pre-tool|pre-tool-memory|pre-compact|post-compact))\s*''',
                        command,
                    )
                    if not match:
                        raise RuntimeError("Cannot validate a registered mnemos hook; verify stopped")
                    hook_binary = next(group for group in match.groups() if group is not None)
                    resolved = shutil.which(hook_binary)
                    if resolved is None:
                        raise RuntimeError("Cannot resolve a registered mnemos hook; verify stopped")
                    candidates.add(str(Path(resolved).resolve()))
    for candidate in candidates:
        result = subprocess.run([candidate, "config"], env=env, capture_output=True,
                                text=True, encoding="utf-8", timeout=15, check=True)
        actual = json.loads(result.stdout)["Storage"]["Path"]
        if os.path.normcase(str(Path(actual).resolve())) != expected:
            raise RuntimeError("A mnemos binary ignores MNEMOS_CONFIG; upgrade its installation before verify")
    (target / "mcp_on.json").write_text(json.dumps({"mcpServers": {"mnemos": {
        "type": "stdio", "command": executable, "args": ["serve"],
    }}}), encoding="utf-8")


if __name__ == "__main__":
    snapshot(json.load(sys.stdin), sys.argv[1])
    if len(sys.argv) > 2:
        prepare_runner(sys.argv[1], sys.argv[2])
