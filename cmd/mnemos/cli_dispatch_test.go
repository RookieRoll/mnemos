package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// This file pins the CLI dispatch contract (design D1): help is answered
// before any handler runs and exits 0, usage errors exit 2, runtime failures
// exit 1, and neither outcome depends on a handler remembering the rule.

// isolatedHome gives the test its own HOME/USERPROFILE and returns it. Every
// state assertion below is "nothing appeared under this directory".
func isolatedHome(t *testing.T) string {
	t.Helper()
	withHome(t)
	return os.Getenv("HOME")
}

// assertHomeEmpty fails when the command under test created state where the
// config and the store live. That is the observable form of "no store was
// opened": opening one creates the SQLite file, and loading a config that does
// not exist creates the TOML file.
func assertHomeEmpty(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("command wrote user state: %v", entries)
	}
}

func TestDispatchHelp(t *testing.T) {
	type testCase struct {
		name         string
		args         []string
		wantContains string
	}
	cases := []testCase{
		{name: "top level short", args: []string{"-h"}},
		{name: "top level long", args: []string{"--help"}},
	}
	// Every registered command in both spellings. serve would block and dream
	// would run a pass if the request reached the handler, which is the point
	// of intercepting it at dispatch.
	for _, cmd := range []string{
		"serve", "search", "stats", "digest", "efficacy", "sessions", "export",
		"import", "prune", "dream", "maintenance", "vault", "embed", "skill",
		"replay", "prewarm", "continuity", "hook", "version", "config", "init",
		"update", "doctor", "verify",
	} {
		for _, help := range []string{"-h", "--help"} {
			want := cmd
			if cmd == "hook" {
				// hook is adapter plumbing and is not advertised in the usage
				// text; only the exit status and the usage text matter here.
				want = ""
			}
			cases = append(cases, testCase{
				name: cmd + " " + help, args: []string{cmd, help}, wantContains: want,
			})
		}
	}
	// Help is intercepted wherever it appears, including below a subcommand
	// tree that would otherwise select a branch first.
	for _, args := range [][]string{
		{"embed", "status", "--help"},
		{"embed", "backfill", "-h"},
		{"skill", "list", "--help"},
		{"skill", "export", "-h"},
		{"vault", "export", "--help"},
		{"maintenance", "list", "-h"},
	} {
		cases = append(cases, testCase{name: strings.Join(args, " "), args: args})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			var stdout, stderr bytes.Buffer
			code := dispatch(context.Background(), tc.args, &stdout, &stderr)
			if code != exitSuccess {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitSuccess, stderr.String())
			}
			var want bytes.Buffer
			printUsage(&want)
			if stdout.String() != want.String() {
				t.Fatalf("help output is not the usage text:\n got %q\nwant %q",
					stdout.String(), want.String())
			}
			if tc.wantContains != "" && !strings.Contains(stdout.String(), tc.wantContains) {
				t.Errorf("usage text does not mention %q", tc.wantContains)
			}
			assertHomeEmpty(t, home)
		})
	}
}

func TestDispatchUsageError(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantStderr []string
	}{
		{"unknown command", []string{"nope"}, []string{"unknown command", "Usage:"}},
		{"unknown sessions flag", []string{"sessions", "--nope"}, []string{"flag provided but not defined", "usage: mnemos sessions [flags]"}},
		{"unknown prune flag", []string{"prune", "--nope"}, []string{"flag provided but not defined", "usage: mnemos prune [flags]"}},
		{"unknown stats flag", []string{"stats", "--nope"}, []string{"usage: mnemos stats [flags]"}},
		{"unknown export flag", []string{"export", "--nope"}, []string{"usage: mnemos export [flags]"}},
		{"unknown config flag", []string{"config", "--nope"}, []string{"usage: mnemos config [flags]"}},
		{"unknown dream flag", []string{"dream", "--nope"}, []string{"usage: mnemos dream [flags]"}},
		{"unknown prewarm flag", []string{"prewarm", "--nope"}, []string{"usage: mnemos prewarm [flags]"}},
		{"unknown skill export flag", []string{"skill", "export", "--nope"}, []string{"usage: mnemos skill export [flags]"}},
		{"unknown embed backfill flag", []string{"embed", "backfill", "--nope"}, []string{"usage: mnemos embed backfill [flags]"}},
		{"unknown vault export flag", []string{"vault", "export", "--nope"}, []string{"usage: mnemos vault export [flags]"}},
		{"unknown skill subcommand", []string{"skill", "teleport"}, []string{"unknown skill subcommand"}},
		{"unknown embed subcommand", []string{"embed", "teleport"}, []string{"unknown embed subcommand"}},
		{"unknown vault subcommand", []string{"vault", "teleport"}, []string{"unknown vault subcommand"}},
		{"skill import without source", []string{"skill", "import"}, []string{"usage: mnemos skill import <file-or-url>"}},
		{"search without query", []string{"search"}, []string{"usage: mnemos search <query>"}},
		{"import without file", []string{"import"}, []string{"usage: mnemos import <file>"}},
		{"export with two files", []string{"export", "a.json", "b.json"}, []string{"expected at most one output file"}},
		{"sessions with positional", []string{"sessions", "junk"}, []string{"unexpected positional arguments"}},
		{"prune with positional", []string{"prune", "junk"}, []string{"unexpected positional arguments"}},
		{"config with unknown client", []string{"config", "--client", "bogus"}, []string{"expected --client pi"}},
		{"prewarm with invalid mode", []string{"prewarm", "--mode", "wat"}, []string{"invalid mode"}},
		{"prewarm with invalid format", []string{"prewarm", "--format", "xml"}, []string{"invalid format"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			var stdout, stderr bytes.Buffer
			code := dispatch(context.Background(), tc.args, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q: %q", want, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "help requested") {
				t.Errorf("usage error rendered the help sentinel: %q", stderr.String())
			}
			assertHomeEmpty(t, home)
		})
	}
}

func TestDispatchRuntimeError(t *testing.T) {
	home := isolatedHome(t)
	// A store path that exists but cannot be opened: SQLite refuses a
	// directory, so the failure is injected below the dispatch layer while the
	// invocation itself stays valid.
	blocked := filepath.Join(home, "store-as-directory")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".mnemos")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[storage]\npath = \"" + filepath.ToSlash(blocked) + "\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := dispatch(context.Background(), []string{"stats"}, &stdout, &stderr)
	if code != exitFailure {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, exitFailure, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("a runtime failure must explain itself on stderr")
	}
	if strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("runtime failure was misclassified as a usage error: %q", stderr.String())
	}
}

// TestMaintenanceHelpPerformsNoWrites covers task 1.7: --help on the commands
// that just became flag-parsing must leave the store byte-identical, not
// merely "not prune anything".
func TestMaintenanceHelpPerformsNoWrites(t *testing.T) {
	for _, cmd := range []string{"prune", "sessions"} {
		t.Run(cmd, func(t *testing.T) {
			home := isolatedHome(t)
			ctx := context.Background()

			d, err := loadDeps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.mem.Save(ctx, memory.SaveInput{
				Title:   "survives help",
				Content: "a help request must neither prune nor close anything",
				Type:    memory.TypeContext,
				Project: "mnemos",
			}); err != nil {
				t.Fatal(err)
			}
			d.close()

			before := initTree(t, home)
			var stdout, stderr bytes.Buffer
			if code := dispatch(ctx, []string{cmd, "--help"}, &stdout, &stderr); code != exitSuccess {
				t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr.String())
			}
			if !reflect.DeepEqual(before, initTree(t, home)) {
				t.Fatal("--help changed user state on disk")
			}

			d, err = loadDeps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			st, err := d.mem.Stats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.Observations != 1 {
				t.Fatalf("observations = %d after --help, want 1", st.Observations)
			}
		})
	}
}

// TestHandlerHelpIsSideEffectFree drives the handlers directly, which is the
// path a second caller (or a test) takes when it skips dispatch: a handler's
// own flag set must answer help before it loads dependencies.
func TestHandlerHelpIsSideEffectFree(t *testing.T) {
	withSub := func(sub string) func(context.Context, []string) error {
		return func(ctx context.Context, args []string) error {
			return runSkill(ctx, append([]string{sub}, args...))
		}
	}
	cases := []struct {
		name string
		run  func(context.Context, []string) error
		args []string
	}{
		{"prune short", runPrune, []string{"-h"}},
		{"prune long", runPrune, []string{"--help"}},
		{"sessions short", runSessions, []string{"-h"}},
		{"sessions long", runSessions, []string{"--help"}},
		{"stats", runStats, []string{"--help"}},
		{"export", runExport, []string{"--help"}},
		{"prewarm", runPrewarm, []string{"--help"}},
		{"dream", runDream, []string{"--help"}},
		{"skill list", withSub("list"), []string{"--help"}},
		{"embed backfill", func(ctx context.Context, args []string) error {
			return runEmbed(ctx, append([]string{"backfill"}, args...))
		}, []string{"-h"}},
		{"vault export", func(ctx context.Context, args []string) error {
			return runVault(ctx, append([]string{"export"}, args...))
		}, []string{"--help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			var err error
			out := captureStdout(t, func() { err = tc.run(context.Background(), tc.args) })
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("err = %v, want flag.ErrHelp", err)
			}
			if !strings.Contains(out, "usage: mnemos ") {
				t.Fatalf("usage text missing from help output: %q", out)
			}
			assertHomeEmpty(t, home)
		})
	}
}
