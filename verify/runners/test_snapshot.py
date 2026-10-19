"""No-model regression checks for the behavior/capture runner snapshot."""
import importlib.util
from contextlib import closing
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("snapshot", Path(__file__).with_name("snapshot.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SnapshotTest(unittest.TestCase):
    def test_mcp_binds_test_binary_and_rejects_old_global_hook(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            claude = root / "claude"
            claude.mkdir()
            (claude / "settings.json").write_text(json.dumps({"hooks": {"SessionStart": [{"hooks": [
                {"command": '"old-mnemos" prewarm'}
            ]}]}}), encoding="utf-8")
            tested = str(root / "new-mnemos")
            old = str(root / "old-mnemos")
            expected = str(root / "mnemos.db")

            def run(args, **kwargs):
                self.assertEqual(kwargs["env"]["MNEMOS_CONFIG"], str(root / "config.toml"))
                path = expected if args[0] == tested else str(root / "live.db")
                return mock.Mock(stdout=json.dumps({"Storage": {"Path": path}}))

            with mock.patch.dict(os.environ, {"CLAUDE_CONFIG_DIR": str(claude)}), \
                    mock.patch.object(module.shutil, "which", side_effect=lambda name: tested if name == "under-test" else old), \
                    mock.patch.object(module.subprocess, "run", side_effect=run):
                with self.assertRaisesRegex(RuntimeError, "ignores MNEMOS_CONFIG"):
                    module.prepare_runner(root, "under-test")
                self.assertFalse((root / "mcp_on.json").exists())
                default_claude = root / ".claude"
                default_claude.mkdir()
                (default_claude / "settings.json").write_bytes((claude / "settings.json").read_bytes())
                with mock.patch.dict(os.environ, {"CLAUDE_CONFIG_DIR": ""}), \
                        mock.patch.object(module.Path, "home", return_value=root):
                    with self.assertRaisesRegex(RuntimeError, "ignores MNEMOS_CONFIG"):
                        module.prepare_runner(root, "under-test")
                (claude / "settings.json").write_text("{}", encoding="utf-8")
                module.prepare_runner(root, "under-test")
                mcp = json.loads((root / "mcp_on.json").read_text())
                self.assertEqual(mcp["mcpServers"]["mnemos"]["command"], tested)
                for command in ['"new-mnemos" hook user-prompt; "old-mnemos" prewarm',
                                '"new-mnemos" hook user-prompt --config live.toml']:
                    with self.subTest(command=command):
                        (claude / "settings.json").write_text(json.dumps({"hooks": {"UserPromptSubmit": [{"hooks": [
                            {"command": command}
                        ]}]}}), encoding="utf-8")
                        with self.assertRaisesRegex(RuntimeError, "Cannot validate"):
                            module.prepare_runner(root, "under-test")

    def test_wal_snapshot_and_fixture_writes_are_isolated(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "live ' quoted.db"
            with closing(sqlite3.connect(source)) as live:
                live.execute("PRAGMA journal_mode=WAL")
                live.execute("CREATE TABLE memories (value TEXT)")
                live.execute("INSERT INTO memories VALUES ('real')")
                live.commit()
                target = root / "scratch"
                module.snapshot({"Storage": {"Path": str(source)}}, target)
                with closing(sqlite3.connect(target / "mnemos.db")) as copy:
                    self.assertEqual(copy.execute("SELECT value FROM memories").fetchall(), [("real",)])
                    copy.execute("INSERT INTO memories VALUES ('fixture')")
                    copy.commit()
                self.assertEqual(live.execute("SELECT value FROM memories").fetchall(), [("real",)])
                self.assertIn("[storage]", (target / "config.toml").read_text())

    def test_absent_store_is_not_created_at_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "not-yet-created.db"
            target = root / "scratch"
            module.snapshot({"Storage": {"Path": str(source)}}, target)
            self.assertFalse(source.exists())
            self.assertTrue((target / "config.toml").is_file())


if __name__ == "__main__":
    unittest.main()
