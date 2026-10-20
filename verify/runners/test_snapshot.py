"""No-model checks for explicit routes and isolated Claude runner preparation."""
from contextlib import closing
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

import snapshot as module


class RunnerTest(unittest.TestCase):
    def make_runtime(self, root: Path) -> tuple[dict[str, Path], Path]:
        binary = root / "candidate mnemos"
        host = root / "fake claude"
        config = root / "source config.toml"
        for path in (binary, host):
            path.write_text("fixture executable", encoding="utf-8")
            if os.name != "nt":
                path.chmod(0o755)
        config.write_text("[storage]\npath = \"unused.db\"\n", encoding="utf-8")
        return {"binary": binary.resolve(), "config": config.resolve(), "host": host.resolve()}, binary

    def test_runtime_paths_are_explicit_and_old_product_environment_is_ignored(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, _ = self.make_runtime(root)
            path = root / "runtime.json"
            path.write_text(json.dumps({
                "mnemos_binary": str(runtime["binary"]),
                "mnemos_config": str(runtime["config"]),
                "claude_binary": str(runtime["host"]),
            }), encoding="utf-8")
            with mock.patch.dict(os.environ, {
                "MNEMOS_BIN": "ignored", "MNEMOS_CONFIG": "ignored.toml",
                "MNEMOS_DISABLED": "1", "MNEMOS_CONTINUITY": "1",
            }, clear=False):
                selected = module.load_runtime(path)
            self.assertEqual(selected, runtime)
            path.write_text(json.dumps({"mnemos_binary": "mnemos", "mnemos_config": "x", "claude_binary": "claude"}), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "absolute local path"):
                module.load_runtime(path)

    @unittest.skipIf(os.name == "nt", "Windows does not use Unix executable permission bits")
    def test_runtime_rejects_non_executable_binaries(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, _ = self.make_runtime(root)
            runtime["host"].chmod(0o644)
            path = root / "runtime.json"
            path.write_text(json.dumps({
                "mnemos_binary": str(runtime["binary"]),
                "mnemos_config": str(runtime["config"]),
                "claude_binary": str(runtime["host"]),
            }), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "is not executable"):
                module.load_runtime(path)

    def test_source_config_keeps_real_home_for_tilde_and_defaults_but_strips_legacy_routes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            real_home = str(root / "selected-user-home")
            with mock.patch.dict(os.environ, {
                "HOME": real_home, "USERPROFILE": real_home,
                "MNEMOS_CONFIG": "ignored.toml", "MNEMOS_BIN": "ignored.exe",
            }, clear=False):
                env = module.source_config_env(root / "scratch-home")
            self.assertEqual(env["HOME"], real_home)
            self.assertEqual(env["USERPROFILE"], real_home)
            self.assertNotIn("MNEMOS_CONFIG", env)
            self.assertNotIn("MNEMOS_BIN", env)
            self.assertNotEqual(env["HOME"], str(root / "scratch-home"))

    def test_scratch_settings_and_mcp_routes_disable_all_global_hooks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, _ = self.make_runtime(root)
            config, settings_path, mcp_path, project = module.write_scratch_files(root / "on", runtime["binary"], "on")
            settings = json.loads(settings_path.read_text(encoding="utf-8"))
            mcp = json.loads(mcp_path.read_text(encoding="utf-8"))
            self.assertEqual(settings, {"disableAllHooks": True})
            self.assertEqual(mcp["mcpServers"]["mnemos"]["command"], str(runtime["binary"]))
            self.assertEqual(mcp["mcpServers"]["mnemos"]["args"], ["--config", str(config), "serve"])
            self.assertTrue(project.is_dir())

            _, off_settings_path, off_mcp_path, _ = module.write_scratch_files(root / "off", runtime["binary"], "off")
            self.assertEqual(json.loads(off_settings_path.read_text(encoding="utf-8")), {"disableAllHooks": True})
            self.assertEqual(json.loads(off_mcp_path.read_text(encoding="utf-8")), {"mcpServers": {}})

    def test_wal_snapshot_keeps_fixture_writes_out_of_live_store(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "live quoted.db"
            with closing(sqlite3.connect(source)) as live:
                live.execute("PRAGMA journal_mode=WAL")
                live.execute("CREATE TABLE memories (value TEXT)")
                live.execute("INSERT INTO memories VALUES ('real')")
                live.commit()
                target = module.snapshot(source, root / "scratch")
                with closing(sqlite3.connect(target)) as copy:
                    self.assertEqual(copy.execute("SELECT value FROM memories").fetchall(), [("real",)])
                    copy.execute("INSERT INTO memories VALUES ('fixture')")
                    copy.commit()
                self.assertEqual(live.execute("SELECT value FROM memories").fetchall(), [("real",)])

    def test_missing_live_store_stays_absent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "missing.db"
            target = module.snapshot(source, root / "scratch")
            self.assertFalse(source.exists())
            self.assertFalse(target.exists())

    def test_old_host_fails_before_any_model_launch_or_store_read(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, _ = self.make_runtime(root)
            calls = []

            def fake_run(command, **kwargs):
                calls.append(command)
                return SimpleNamespace(returncode=0, stdout="Claude Code 2.1.285\n", stderr="")

            with self.assertRaisesRegex(RuntimeError, "2.1.286 or newer"):
                module.launch(runtime, "on", "fixture prompt", runner=fake_run, temp_parent=root)
            self.assertEqual(calls, [[str(runtime["host"]), "--version"]])
            self.assertFalse(any("-p" in command for command in calls))

    def test_on_launch_uses_explicit_scratch_route_before_host_and_never_reads_global_settings(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, binary = self.make_runtime(root)
            live_db = root / "live.db"
            with closing(sqlite3.connect(live_db)) as db:
                db.execute("CREATE TABLE memories (value TEXT)")
                db.execute("INSERT INTO memories VALUES ('source')")
                db.commit()
            runtime["config"].write_text(
                "[storage]\npath = " + json.dumps(str(live_db)) + "\n", encoding="utf-8")
            calls = []
            hook_payloads = {}
            host_version = "Claude Code 2.1.286\n"
            host_help = " ".join(module.REQUIRED_HOST_FLAGS)

            def fake_run(command, **kwargs):
                calls.append((command, kwargs))
                if command[0] == str(runtime["host"]) and command[1:] == ["--version"]:
                    return SimpleNamespace(returncode=0, stdout=host_version, stderr="")
                if command[0] == str(runtime["host"]) and command[1:] == ["--help"]:
                    return SimpleNamespace(returncode=0, stdout=host_help, stderr="")
                if command[0] == str(binary) and command[1:] == ["--config", str(runtime["config"]), "config"]:
                    return SimpleNamespace(returncode=0, stdout=json.dumps({"Storage": {"Path": str(live_db)}}), stderr="")
                if command[0] == str(binary) and "prewarm" in command:
                    self.assertIn("--config", command)
                    payload = json.loads(command[command.index("--payload") + 1])
                    hook_payloads["prewarm"] = payload
                    return SimpleNamespace(returncode=0, stdout=json.dumps({"text": "PREWARM", "session_id": "session-fixture"}), stderr="")
                if command[0] == str(binary) and "user-prompt" in command:
                    self.assertIn("--config", command)
                    hook_payloads["user-prompt"] = json.loads(command[command.index("--payload") + 1])
                    return SimpleNamespace(returncode=0, stdout=json.dumps({"context": "PROMPT"}), stderr="")
                if command[0] == str(runtime["host"]):
                    self.assertIn("--bare", command)
                    self.assertIn("--strict-mcp-config", command)
                    self.assertIn("--append-system-prompt", command)
                    settings = Path(command[command.index("--settings") + 1])
                    mcp = Path(command[command.index("--mcp-config") + 1])
                    self.assertEqual(json.loads(settings.read_text(encoding="utf-8")), {"disableAllHooks": True})
                    route = json.loads(mcp.read_text(encoding="utf-8"))["mcpServers"]["mnemos"]
                    self.assertEqual(route["args"][0:2], ["--config", str(mcp.parent / "config.toml")])
                    self.assertEqual(route["command"], str(binary))
                    env = kwargs["env"]
                    self.assertFalse(any(key.upper().startswith("MNEMOS_") for key in env))
                    self.assertNotIn("CLAUDE_CONFIG_DIR", env)
                    self.assertEqual(command[command.index("--append-system-prompt") + 1], "PREWARM\nPROMPT")
                    return SimpleNamespace(returncode=0, stdout="", stderr="")
                self.fail(f"unexpected subprocess: {command}")

            with mock.patch.object(module.Path, "home", side_effect=AssertionError("global home lookup")):
                self.assertEqual(module.launch(runtime, "on", "test trigger", runner=fake_run, temp_parent=root), 0)
            commands = [item[0] for item in calls]
            self.assertRegex(hook_payloads["prewarm"]["session_id"], r"^mnemos-verify-[0-9a-f-]{36}$")
            self.assertEqual(hook_payloads["user-prompt"]["session_id"], "session-fixture")
            self.assertLess(commands.index([str(binary), "--config", str(runtime["config"]), "config"]),
                            next(i for i, command in enumerate(commands) if command[0] == str(runtime["host"]) and "-p" in command))
            self.assertEqual([command[1] for command in commands if command[0] == str(runtime["host"])],
                             ["--version", "--help", "-p"])
            with closing(sqlite3.connect(live_db)) as db:
                self.assertEqual(db.execute("SELECT value FROM memories").fetchall(), [("source",)])

    def test_off_arm_never_starts_mnemos(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runtime, binary = self.make_runtime(root)
            calls = []

            def fake_run(command, **kwargs):
                calls.append(command)
                if command[1:] == ["--version"]:
                    return SimpleNamespace(returncode=0, stdout="Claude Code 2.2.0", stderr="")
                if command[1:] == ["--help"]:
                    return SimpleNamespace(returncode=0, stdout=" ".join(module.REQUIRED_HOST_FLAGS), stderr="")
                self.assertEqual(command[0], str(runtime["host"]))
                self.assertIn("--bare", command)
                self.assertIn("--strict-mcp-config", command)
                self.assertNotIn("--append-system-prompt", command)
                mcp = Path(command[command.index("--mcp-config") + 1])
                self.assertEqual(json.loads(mcp.read_text(encoding="utf-8")), {"mcpServers": {}})
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            self.assertEqual(module.launch(runtime, "off", "baseline", runner=fake_run, temp_parent=root), 0)
            self.assertFalse(any(command[0] == str(binary) for command in calls))


if __name__ == "__main__":
    unittest.main()
