"""Small stdlib-only HTTP client shared by the example hooks."""

from __future__ import annotations

import json
import os
import tomllib
from pathlib import Path
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


def load_server(config_path: str | None = None) -> tuple[str, str]:
    """Read server.http_addr and server.api_key from one TOML file."""
    path = Path(config_path).expanduser() if config_path else Path.home() / ".mnemos" / "config.toml"
    with path.open("rb") as config_file:
        config = tomllib.load(config_file)
    server = config.get("server", {})
    address = server.get("http_addr", ":8080")
    if not isinstance(address, str) or not address.strip():
        raise ValueError("server.http_addr must be a non-empty string")
    if address.startswith(":"):
        address = "http://localhost" + address
    elif "://" not in address:
        address = "http://" + address
    parsed = urlsplit(address)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.path not in ("", "/"):
        raise ValueError("server.http_addr must be an HTTP(S) address without a path")
    key = server.get("api_key", "")
    if not isinstance(key, str):
        raise ValueError("server.api_key must be a string")
    return address.rstrip("/"), key


def project_name(explicit: str | None = None) -> str:
    """Use the explicit project label, Git root, or current directory name."""
    if explicit:
        return explicit
    import subprocess

    try:
        result = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            check=True,
            capture_output=True,
            text=True,
            timeout=2,
        )
        root = result.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        root = os.getcwd()
    return Path(root).name or root


def request_for(config_path: str | None, endpoint: str, payload: dict) -> Request:
    """Build a safely encoded JSON request, including configured auth."""
    base, key = load_server(config_path)
    data = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Authorization"] = f"Bearer {key}"
    return Request(base + endpoint, data=data, headers=headers, method="POST")


def post(config_path: str | None, endpoint: str, payload: dict) -> None:
    """Send one bounded best-effort request to the configured local service."""
    request = request_for(config_path, endpoint, payload)
    with urlopen(request, timeout=3):
        pass
