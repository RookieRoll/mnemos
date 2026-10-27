// Mnemos CLI. Subcommands are thin wrappers over the domain services.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/polyxmedia/mnemos/internal/version"
)

// Process exit statuses. They are part of the CLI contract: scripts and hooks
// need to tell "the invocation is wrong, do not retry" (2) from "the store
// failed, a retry may help" (1).
const (
	exitSuccess = 0 // success, or an explicit help request
	exitFailure = 1 // runtime or domain failure
	exitUsage   = 2 // usage error: unknown command/flag, missing or invalid input
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

// dispatch routes one argv slice onto a handler and maps its outcome to an
// exit status. It is separate from main so tests can drive the whole command
// surface without os.Exit.
//
// Help is answered here, before a handler runs (design D1): "mnemos prune -h"
// must print usage and exit 0 without opening a store or pruning anything, and
// that guarantee cannot depend on every handler remembering it.
//
// The scan stops at the "--" terminator, so an operand that happens to be
// spelled "--help" reaches its handler instead of being answered with usage
// (CR c4#4): "mnemos import -- --help" imports a file named --help.
//
// One boundary is deliberately left unpoliced: "--" only ends flag parsing, it
// does not make this layer forget which flags take a value, so "mnemos prewarm
// --goal --help" is still read as a help request even though the handler would
// have consumed --help as the value of --goal. Telling those two shapes apart
// needs per-command grammar, which dispatch does not have and must not grow;
// the design D1 rule (help wins wherever it appears) is the answer instead.
func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	for _, arg := range args {
		if arg == "--" {
			// Everything after -- is an operand, not a flag.
			break
		}
		if arg == "-h" || arg == "--help" {
			printUsage(stdout)
			return exitSuccess
		}
	}
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	ctx, cmd, rest, err := parseInvocation(ctx, args)
	if err != nil {
		fmt.Fprintln(stderr, "mnemos:", err)
		return exitUsage
	}
	handler, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(stderr, "unknown command: %s\n\n", cmd)
		printUsage(stderr)
		return exitUsage
	}
	if err := handler(ctx, rest); err != nil {
		return commandFailure(cmd, err, stderr)
	}
	return exitSuccess
}

// commandFailure maps a handler error onto the exit taxonomy: help is success,
// a usage error is 2, everything else — a domain or runtime failure — is 1.
// An error that already wrote its machine-readable payload
// (maintenanceJSONError) adds no human-oriented text to stderr.
func commandFailure(cmd string, err error, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitSuccess
	}
	var jsonErr maintenanceJSONError
	if errors.As(err, &jsonErr) {
		return exitFailure
	}
	var usageErr *usageError
	if errors.As(err, &usageErr) {
		if !usageErr.quiet {
			fmt.Fprintf(stderr, "mnemos: %s: %v\n", cmd, err)
			if usageErr.usage != "" {
				fmt.Fprint(stderr, usageErr.usage)
			}
		}
		return exitUsage
	}
	fmt.Fprintf(stderr, "mnemos: %s: %v\n", cmd, err)
	return exitFailure
}

// usageError marks a failure caused by the invocation itself — an unknown
// subcommand, an unknown flag, a missing required flag, an invalid value —
// rather than by the work. dispatch exits 2 for it so a script can tell a
// malformed invocation from a transient one.
type usageError struct {
	cause error
	usage string // usage text to print after the message, when the source has one
	// quiet suppresses dispatch's stderr message because the handler already
	// wrote a machine-readable payload. continuity's v1 error envelope is the
	// one such surface: it answers in JSON and still classifies the malformed
	// request as a usage error.
	quiet bool
}

func (e *usageError) Error() string { return e.cause.Error() }

func (e *usageError) Unwrap() error { return e.cause }

// usageErrorf builds a usage error. Handlers use it for anything that is the
// caller's mistake rather than the store's.
func usageErrorf(format string, args ...any) error {
	return &usageError{cause: fmt.Errorf(format, args...)}
}

// quietUsageError marks a usage error whose machine-readable payload the
// handler already wrote. dispatch still exits 2, but adds no prose to stderr,
// mirroring how maintenanceJSONError keeps a written JSON response clean.
func quietUsageError(err error) error {
	return &usageError{cause: err, quiet: true}
}

// newCommandFlagSet builds the flag set every handler parses through.
// ContinueOnError is deliberate: flag.ExitOnError would call os.Exit inside a
// handler and skip its deferred store closes.
func newCommandFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet("mnemos "+name, flag.ContinueOnError)
}

// parseFlags parses args for a command's flag set and classifies the outcome:
// nil on success, flag.ErrHelp when help was requested (dispatch exits 0), and
// a usage error otherwise (dispatch exits 2). flag's own output is suppressed
// so this one place decides where text goes — stdout for help, stderr for
// errors — and a help request never reaches a handler's storage work.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var rendered bytes.Buffer
	fs.SetOutput(io.Discard)
	fs.Usage = func() { writeFlagUsage(&rendered, fs) }
	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(os.Stdout, rendered.String())
		return flag.ErrHelp
	default:
		return &usageError{cause: err, usage: rendered.String()}
	}
}

// splitFlagArgs separates positional arguments from flags so a command whose
// grammar is `name [flags]` can still parse its flags. flag.Parse stops at the
// first positional argument, which would otherwise turn
// `skill delete NAME --dry-run` into a usage error. A flag that takes a value
// keeps its value attached, whether it arrived as `--flag value` or
// `--flag=value`; boolean flags never consume the next argument.
func splitFlagArgs(fs *flag.FlagSet, args []string) (flagArgs, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			// An unknown flag stays in flagArgs so parseFlags reports it.
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positionals
}

// writeFlagUsage renders "usage: mnemos <command> [flags]" plus the flag
// defaults, so every subcommand help request has one shape.
func writeFlagUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, "usage: %s [flags]\n", fs.Name())
	out := fs.Output()
	fs.SetOutput(w)
	fs.PrintDefaults()
	fs.SetOutput(out)
}

// treeHelp answers a help request for a subcommand tree (embed/skill/vault),
// which selects a branch before any flag set exists. It prints the tree usage
// and returns flag.ErrHelp when the request was a help one, and nil otherwise.
func treeHelp(args []string, usage string) error {
	if len(args) == 0 || (args[0] != "-h" && args[0] != "--help") {
		return nil
	}
	fmt.Fprintln(os.Stdout, usage)
	return flag.ErrHelp
}

type handler func(ctx context.Context, args []string) error

var commands = map[string]handler{
	"serve":       runServe,
	"search":      runSearch,
	"stats":       runStats,
	"digest":      runDigest,
	"efficacy":    runEfficacy,
	"sessions":    runSessions,
	"export":      runExport,
	"import":      runImport,
	"prune":       runPrune,
	"retire":      runRetire,
	"backup":      runBackup,
	"dream":       runDream,
	"maintenance": runMaintenance,
	"vault":       runVault,
	"embed":       runEmbed,
	"skill":       runSkill,
	"replay":      runReplay,
	"prewarm":     runPrewarm,
	"continuity":  runContinuity,
	"hook":        runHook,
	"version":     runVersion,
	"config":      runConfig,
	"init":        runInit,
	"update":      runUpdate,
	"doctor":      runDoctor,
	"verify":      runVerify,
	"-v":          runVersion,
	"--version":   runVersion,
	"-h":          runHelp,
	"--help":      runHelp,
	"help":        runHelp,
}

func runVersion(_ context.Context, _ []string) error {
	fmt.Println("mnemos", version.Version)
	return nil
}

func runHelp(_ context.Context, _ []string) error {
	printUsage(os.Stdout)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `mnemos — persistent memory and skills for AI coding agents

Usage:
  mnemos [--config FILE] <command> [flags]

Global:
  --config FILE          Select a TOML file (default: ~/.mnemos/config.toml)
  -h, --help             Print this usage text and exit

Commands:
  serve [--http ADDR]    Start the MCP stdio server (or HTTP if --http given)
  search <query>         Search observations from the terminal
  stats                  Show memory statistics
  digest [--since 24h]   Activity summary: saves, surfacing, sessions, dreams
  efficacy [--since]     Surfaced-vs-outcome: does surfacing memory go with sessions that end ok
  sessions [--status S] [--limit N] [--offset N] [--json]
                         List recent sessions
  sessions close --stale 24h [--project P] [--status abandoned] [--summary S] [--dry-run]
                         Close open sessions older than a duration
  export [file]          Export all data as JSON (see also: backup)
  import <file>          Import data from JSON
  backup <path>          Snapshot the whole database to a new file (never overwrites)
  prune                  Remove expired observations
  retire [flags]         Set valid_until on observations matching --project,
                         --importance-max and/or --no-access (--dry-run to preview)
  dream [--watch]        Run a consolidation pass (or daemon with --watch)
  maintenance list      List pending semantic maintenance candidates as JSON
  maintenance evidence --id ID
                         Print candidate evidence and review context as JSON
  maintenance resolve --payload JSON
                         Resolve a candidate with an agent-authored prediction
  maintenance dismiss --payload JSON
                         Dismiss a candidate with an agent-authored reason
  vault export           Export memory to an Obsidian-compatible vault
  vault watch            Daemon: keep the vault in sync on a timer
  vault status           Show vault sync status
  embed status           Show provider status, embedded N/M coverage and model mix
  embed backfill [--rebuild-incompatible]
                         Fill missing vectors; reports skipped reasons and remaining
  skill list             Show installed skills
  skill export [names]   Build a shareable pack (stdout or --out file)
  skill import <src>     Import a pack from a file path or https URL
  skill delete <name>    Remove matching skills (--agent/--project/--dry-run)
  skill merge <name>     Fold duplicate skills into one keeper (--keep-version/--dry-run)
  replay <session_id>    Markdown recap of a prior session + what you've learned since
  prewarm [flags]        Print the session_start prewarm block (used by the Claude Code hook)
  continuity capabilities  Query continuity v1 support without opening storage
  continuity <open|checkpoint|context> --payload JSON  Explicit scoped progress
  init                   Register mnemos with Claude Code / Cursor / Windsurf
  update [--yes]         Download the latest release and replace this binary
  doctor [--workspaces]  Check installation health or report recorded workspace bindings
  verify retrieval       Probe the store with fixture queries (precision@K)
  verify behavior        A/B replay scenarios with mnemos on vs off (lift)
  verify capture         Single-arm: do agents record corrections handed to them?
  verify calibrate       Sweep gate parameters on the score contract scale
  verify all             Run all three
  config [--client pi]   Print configuration, or a nonsecret adapter projection
  version                Print the binary version

First run:
  mnemos init            # auto-wires to Claude Code, Cursor, Windsurf
  Restart your agent.    # new tools will appear under mnemos_*
`)
}
