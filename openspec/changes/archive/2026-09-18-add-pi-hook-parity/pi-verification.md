# pi verification record — task 9.1 and 1.5

**This is observation, not harness output.** The five scenarios below were
prompted by hand into a live `pi -p` session. The verify harness itself was not
touched, there is no pi runner, and none of these numbers are comparable to the
README's Claude Code figures. No shipped file claims a pi effect figure.

Two caveats on the environment, neither of which changes a result below:

- The session ran against the working-tree build, with the `pi/` package
  installed project-locally (`pi install ./pi -l`). It has since been removed;
  `.pi/` is not part of this change.
- **The adapter's prefix mode did not survive.** Tool names came out as
  `mcp__mnemos-pi__mnemos_mnemos_save` — `mcp__<package>__mnemos_<tool>` — not
  the `mcp__mnemos_mnemos_save` that `pi/mcp.json`'s `toolPrefix: "mcp"` asks
  for on its own. This is precisely the case the suffix matcher exists for, and
  it is the route a user installing this repository's package actually gets. The
  configured prefix is still worth keeping (it is what a hand-written
  `~/.pi/agent/mcp.json` entry gets), but it is not load-bearing.

- pi 0.85.1, local pi build, model `litellm/deepseek-v4.1-flash`
- binary: the working-tree build at `./mnemos.exe`
- store: an isolated HOME seeded with the five memories `verify/behavior.yaml`
  names, plus two conventions
- a shim binary logged every mnemos invocation and delegated to the real one,
  which is how the "recorded a memory" column was measured

## Task 1.5 — tools are callable without a discovery step

Confirmed by listing the tool names a live session offers; no `mcp` proxy call
was needed to reach any of them. The names the adapter derives are:

```
mcp__mnemos-pi__mnemos_mnemos_save      mcp__mnemos-pi__mnemos_mnemos_search
mcp__mnemos-pi__mnemos_mnemos_correct   mcp__mnemos-pi__mnemos_mnemos_convention
... 21 mnemos tools, plus 4 mnemos resources
```

Note the shape: `mcp__<package-name>__mnemos_<tool>`. **The prefix mode
configured in `pi/mcp.json` is not what pi actually applies** — routing through
the package manifest wins, and the package name is derived from the configured
server key `mnemos`. This is exactly why both the extension's matcher and the Go
guardrail match on the tool-name *suffix*: a matcher built on the configured
prefix would be correct for one installation route and silently wrong for this
one, which is the route this repository ships.

## Task 9.1 — the five README behavior scenarios, run in pi

Memory-application scenarios (the two the README shows a full on/off gap on):

| scenario | observed | note |
| --- | --- | --- |
| `session_start_on_edit` | **did not pass** | no mnemos tool call; answers came from repo files |
| `oss_first_for_protocol` | not run separately | see below |

`session_start_on_edit` is a genuine miss, not an assertion artifact. The
prewarm block *was* delivered — it reached the model as the system prompt on the
first turn, and it carried the seeded convention `check the memory store before
orienting` plus three more. The model oriented itself by reading `AGENTS.md`,
`git status`, and `git log` instead. The `AGENTS.md` in this repository
describes the repo's state in the same detail the memory block does, so the
model had a cheaper sufficient source and used it. Whether the block would win
without that file is not something this run can distinguish.

Capture scenarios (the README publishes 8/15 = 53% *with* the directive already
in context; pi restores that precondition, so 53% is this arm's ceiling, not a
target of 100%):

| scenario | recorded a memory | README's Claude Code rate |
| --- | --- | --- |
| `explicit_save_request` | **yes** (`mnemos_convention`) | 100% |
| `inline_correction` | **yes** (`mnemos_correct`) | 67% |
| `quiet_convention_mention` | **yes** (`mnemos_convention`) | 67% |
| `architectural_decision` | **yes** (`mnemos_save`) | **0%** |
| `silent_correction_mid_work` | **yes** (`mnemos_correct`) | 33% |

Overall **5/5 recorded**. Four of the five scenarios came in at or above the
Claude Code rate. The fifth — `architectural_decision`, the one the README calls
out as stuck at 0/3 even with the directive lever — was recorded here.

**One confound, stated plainly.** `inline_correction`'s trigger text is the
fixture verbatim from `verify/capture.yaml`, and the model noticed: it replied
that the prompt "is verbatim the fixture in `verify/capture.yaml:28`" and that
the `mnemos_correct` call had already flowed through the hook pipeline. A model
that recognises the harness scenario is not a model behaving in the wild, so that
row is the weakest evidence here. The other four had no such tell.

The scenarios had a real side effect, which is worth noting for anyone
re-running this: `quiet_convention_mention` asks for a helper in `internal/api/`,
and the session wrote `internal/api/validation.go` plus a test into this
working tree. Both were removed. A hand-run of these prompts edits the
repository — the harness runs avoid this by using a scratch checkout.

**What this is not.** One run per scenario, not five. A single model. A
recognisable fixture in one case. There is no off-arm, so no lift is measured and
none is claimed — every row could in principle be a model that would have
recorded the memory anyway.
