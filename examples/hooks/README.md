# Claude Code hook examples

These are opt-in hooks that auto-save signal to Mnemos without the agent
having to call tools manually. Drop any of them into `.claude/settings.json`
under a `hooks` key.

Each hook is a shell command receiving JSON on stdin. If you don't want a
particular automation, skip it — none of these are required.

## post-commit: save the commit as an observation

When `git commit` succeeds from inside Claude Code, save the commit
message + diff summary as an observation.

```json
{
  "hooks": {
    "PostBashToolUse": [
      {
        "matcher": "git commit",
        "command": "/usr/local/bin/mnemos-hook-commit.sh"
      }
    ]
  }
}
```

See `post-commit.sh` in this directory.

## on-edit: touch the file so it enters the heat map

Fires on every Edit tool call. Records that the file was touched in the
current session.

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "command": "/usr/local/bin/mnemos-hook-touch.sh"
      }
    ]
  }
}
```

See `on-edit-touch.sh`.

## Compaction recovery

`mnemos init` already wires this, so no example script is needed. A
`SessionStart` hook with the `compact` matcher runs
`mnemos prewarm --mode compaction_recovery` once the compacted
conversation resumes, and its output is added to the model's context.
An earlier example here used a `PreCompact` hook instead; its output never
reached the model, so it was removed.

## Notes

- Hooks are **push-from-harness**. Mnemos itself never initiates a push;
  these shell scripts just call the MCP server (or HTTP) from your agent
  client's hook runner.
- Keep hooks idempotent. Mnemos dedups on save, so repeat calls are free.
- Test hooks manually first: run the script with a sample JSON payload
  and verify `mnemos search` surfaces the result.
