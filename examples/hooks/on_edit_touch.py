#!/usr/bin/env python3
"""Record an edited file path through the configured Mnemos HTTP API."""

import argparse
import json
import sys

from mnemos_http import post, project_name


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", help="TOML config file (defaults to ~/.mnemos/config.toml)")
    parser.add_argument("--project", help="explicit project label")
    args = parser.parse_args()
    try:
        event = json.load(sys.stdin)
        tool_input = event.get("tool_input", {}) if isinstance(event, dict) else {}
        path = (tool_input.get("file_path") or tool_input.get("path", "")) if isinstance(tool_input, dict) else ""
        if not isinstance(path, str) or not path:
            return 0
        post(args.config, "/v1/touch", {"path": path, "project": project_name(args.project)})
    except (json.JSONDecodeError, OSError, ValueError) as exc:
        print(f"mnemos file-touch hook: {exc}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
