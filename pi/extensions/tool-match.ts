// Tool-name matching for the pi harness.
//
// pi reaches mnemos through an MCP adapter that decides tool names from
// three inputs: the configured server key, the adapter's toolPrefix mode,
// and whether the server arrives through a package manifest (which
// prepends the package name). The combinations produce:
//
//   mnemos   server  direct   mnemos_mnemos_save
//   mnemos   mcp     direct   mcp__mnemos_mnemos_save
//   mnemos   none    direct   mnemos_save
//   mnemos   mcp     package  mcp__mnemos__mnemos_mnemos_save
//   mnemos_  mcp     direct   mcp__mnemos__mnemos_save   <- the only route to
//                                                            the double underscore
//
// The Go side's `isMnemosWriteTool` compares against the exact Claude Code
// names, which is correct there because the installed matcher already
// narrowed the dispatch. In pi there is no matcher, so this module matches
// on the suffix instead: correct under every row above, including the
// package route this repository ships.
//
// Matching on a fixed prefix would be correct for exactly one row and
// silently drop write-boundary coverage for the rest.

/** mnemos' write tools, without any prefix. */
export const MNEMOS_WRITE_TOOLS = ["mnemos_save", "mnemos_correct", "mnemos_convention"] as const;

/**
 * pi's file-editing tools. pi registers these lowercase; Claude Code's
 * matcher uses `Edit` and `Write`. Both spellings are accepted because the
 * same extension logic is reasoned about against both harnesses, and a
 * case-sensitive compare against only one of them would silently stop
 * injecting file-relevant memory on the other.
 */
export const FILE_EDIT_TOOLS = ["edit", "write", "multiedit", "notebookedit"] as const;

/**
 * True when a tool name identifies one of mnemos' write tools, whatever
 * prefix the adapter applied.
 *
 * The suffix must be preceded by a separator or start the name, so a
 * hypothetical `other_mnemos_save_backup` does not match while
 * `mcp__mnemos__mnemos_save` does.
 */
export function isMnemosWriteTool(toolName: string): boolean {
  return matchesSuffix(toolName, MNEMOS_WRITE_TOOLS);
}

/** True when a tool name identifies one of pi's or Claude Code's editing tools. */
export function isFileEditTool(toolName: string): boolean {
  return matchesSuffix(toolName, FILE_EDIT_TOOLS);
}

/**
 * Case-insensitive suffix match on a separator boundary.
 *
 * A bare `endsWith` would let `not_mnemos_save` through. Requiring the
 * separator (or the start of the string) keeps the match to names that are
 * genuinely `<something>_<tool>` or `<tool>`.
 */
function matchesSuffix(toolName: string, suffixes: readonly string[]): boolean {
  const name = (toolName ?? "").toLowerCase();
  if (name === "") return false;
  for (const suffix of suffixes) {
    if (name === suffix) return true;
    if (name.endsWith(`_${suffix}`)) return true;
    // Claude Code's MCP form separates with a double underscore, so the
    // single-underscore check above already covers it; this also accepts a
    // name whose prefix ends in a dash or dot, which adapters may produce.
    if (name.endsWith(`-${suffix}`) || name.endsWith(`.${suffix}`)) return true;
  }
  return false;
}

/**
 * Extracts the file path an editing tool is about to touch. Mirrors the Go
 * side's `filePathFromToolInput`, which reads `file_path`; pi's own edit
 * and write tools use `path`, so both are accepted.
 */
export function filePathFromToolInput(input: unknown): string {
  if (input === null || typeof input !== "object") return "";
  const record = input as Record<string, unknown>;
  for (const key of ["file_path", "path", "notebook_path"]) {
    const value = record[key];
    if (typeof value === "string" && value !== "") return value;
  }
  return "";
}
