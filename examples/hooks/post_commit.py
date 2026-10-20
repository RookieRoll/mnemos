#!/usr/bin/env python3
"""Save a successful Git commit through the configured Mnemos HTTP API."""

import argparse
import subprocess
import sys

from mnemos_http import post, project_name


def git(*args: str) -> str:
    result = subprocess.run(["git", *args], check=True, capture_output=True, text=True, timeout=3)
    return result.stdout.strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", help="TOML config file (defaults to ~/.mnemos/config.toml)")
    parser.add_argument("--project", help="explicit project label")
    args = parser.parse_args()
    try:
        git("rev-parse", "--show-toplevel")
        sha = git("rev-parse", "--short", "HEAD")
        subject = git("log", "-1", "--pretty=%s")
        body = git("log", "-1", "--pretty=%B")
        files = git("show", "--stat", "--name-only", "--oneline", "HEAD").splitlines()[1:21]
        content = f"{body}\n\nFiles:\n" + "\n".join(files)
        payload = {
            "Title": f"commit {sha}: {subject}",
            "Content": content,
            "Type": "episodic",
            "Tags": ["commit", "git"],
            "Project": project_name(args.project),
            "Importance": 6,
        }
        post(args.config, "/v1/observations", payload)
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        print(f"mnemos post-commit hook: {exc}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
