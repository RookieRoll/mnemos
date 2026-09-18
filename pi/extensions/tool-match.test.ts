import { test } from "node:test";
import assert from "node:assert/strict";

import {
  filePathFromToolInput,
  isFileEditTool,
  isMnemosWriteTool,
} from "./tool-match.ts";

// The matrix below is the whole reason this module matches on suffix. Each
// row is a configuration the adapter can actually produce, derived from
// its prefix rules rather than observed on one machine.

test("mnemos write tools match under every adapter configuration", () => {
  const names = [
    // server=mnemos, prefix=server, direct
    "mnemos_mnemos_save",
    "mnemos_mnemos_correct",
    "mnemos_mnemos_convention",
    // server=mnemos, prefix=mcp, direct  (the shipped direct-route config)
    "mcp__mnemos_mnemos_save",
    "mcp__mnemos_mnemos_correct",
    "mcp__mnemos_mnemos_convention",
    // server=mnemos, prefix=none, direct
    "mnemos_save",
    "mnemos_correct",
    "mnemos_convention",
    // server=mnemos, prefix=mcp, via package manifest
    "mcp__mnemos__mnemos_mnemos_save",
    "mcp__mnemos__mnemos_mnemos_correct",
    "mcp__mnemos__mnemos_mnemos_convention",
    // server=mnemos_, prefix=mcp, direct
    "mcp__mnemos__mnemos_save",
    "mcp__mnemos__mnemos_correct",
    "mcp__mnemos__mnemos_convention",
    // Claude Code's own form
    "mcp__mnemos__mnemos_save",
  ];
  for (const name of names) {
    assert.equal(isMnemosWriteTool(name), true, `expected a match for ${name}`);
  }
});

test("tools that are not mnemos writes do not match", () => {
  const foreign = [
    "read",
    "bash",
    "edit",
    "write",
    "mcp",
    "other_server_search",
    "mnemos_save_backup",
    "notmnemos_save",
  ];
  for (const name of foreign) {
    assert.equal(isMnemosWriteTool(name), false, `expected no match for ${name}`);
  }
});

test("a foreign tool ending in a mnemos tool name DOES match, deliberately", () => {
  // This is the documented cost of suffix matching, asserted so it stays
  // visible rather than being discovered later as a bug.
  //
  // `other_mnemos_save` and the legitimate `mnemos_mnemos_save` are
  // structurally identical — `<prefix>_mnemos_save` — so no name-only rule
  // can separate them. The two failure modes are not symmetric: matching
  // too much scans and possibly blocks a foreign write, while matching too
  // little stops scanning real mnemos writes. Over-matching is the cheaper
  // side, and the scanner only blocks on high-risk injection patterns, so
  // the practical cost is a mnemos-flavoured reason on a refused foreign
  // write. See design.md, D6.
  for (const name of ["other_mnemos_save", "someone_else__mnemos_correct"]) {
    assert.equal(isMnemosWriteTool(name), true, `expected the deliberate over-match for ${name}`);
  }
});

test("file-editing tools match in both harnesses' casings", () => {
  // pi registers lowercase; Claude Code's matcher uses Edit/Write. A
  // case-sensitive compare against only one would silently stop injecting
  // file-relevant memory on the other.
  for (const name of ["edit", "write", "Edit", "Write", "MultiEdit", "NotebookEdit"]) {
    assert.equal(isFileEditTool(name), true, `expected an edit match for ${name}`);
  }
  for (const name of ["read", "bash", "find", "grep", "mnemos_save"]) {
    assert.equal(isFileEditTool(name), false, `unexpected edit match for ${name}`);
  }
});

test("file path extraction accepts both pi's and Claude Code's field names", () => {
  // pi's own edit/write tools use `path`; the MCP tools and Claude Code
  // use `file_path`. Missing this would make every pi edit query for an
  // empty path and surface nothing.
  assert.equal(filePathFromToolInput({ path: "src/a.ts" }), "src/a.ts");
  assert.equal(filePathFromToolInput({ file_path: "src/b.ts" }), "src/b.ts");
  assert.equal(filePathFromToolInput({ notebook_path: "nb.ipynb" }), "nb.ipynb");
  assert.equal(filePathFromToolInput({}), "");
  assert.equal(filePathFromToolInput(null), "");
  assert.equal(filePathFromToolInput("a string"), "");
  assert.equal(filePathFromToolInput({ path: 42 }), "");
});
