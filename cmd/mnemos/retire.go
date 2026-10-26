package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// retireSampleWidth caps one preview title in the retirement report. The
// service already bounds the sample to five rows; this keeps each row one
// terminal line.
const retireSampleWidth = 72

// runRetire sets valid_until on every live observation matching the supplied
// selectors:
//
//	mnemos retire --project alpha --importance-max 3 --no-access --older-than 720h [--dry-run]
//
// Retirement is fact-time ("the fact stopped being true at T"), never
// system-time supersession: created_at and invalidated_at are untouched and no
// row is deleted, so an AsOf query before the retirement instant still returns
// the observation (design D5).
//
// --older-than is a relative duration, converted here into the absolute
// instant the service compares created_at against: a row must have been created
// strictly before now minus that duration. Zero means the flag was omitted, not
// "older than zero" — which would match everything already saved.
//
// At least one selector is required. With none, every live row would match, so
// the invocation is refused as a usage error before the store is opened and
// nothing is written.
func runRetire(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("retire")
	project := fs.String("project", "", "retire observations in this project")
	importanceMax := fs.Int("importance-max", 0, "retire observations whose importance is at or below this value")
	noAccess := fs.Bool("no-access", false, "retire only observations that were never accessed")
	olderThan := fs.Duration("older-than", 0, "retire observations created more than this duration ago (e.g. 720h)")
	dryRun := fs.Bool("dry-run", false, "print the selection without writing")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	if *importanceMax < 0 {
		return usageErrorf("--importance-max must not be negative, got %d", *importanceMax)
	}
	if *olderThan < 0 {
		return usageErrorf("--older-than must not be negative, got %s", *olderThan)
	}
	in := memory.RetireInput{
		Project:       strings.TrimSpace(*project),
		ImportanceMax: *importanceMax,
		NoAccess:      *noAccess,
		OlderThan:     retireOlderThanCutoff(*olderThan),
	}
	if in.Project == "" && in.ImportanceMax <= 0 && !in.NoAccess && in.OlderThan.IsZero() {
		return usageErrorf("at least one selector is required: --project, --importance-max, --no-access or --older-than")
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	if *dryRun {
		report, err := d.mem.SelectRetire(ctx, in)
		if err != nil {
			return fmt.Errorf("retire preview: %w", err)
		}
		printRetireReport(report, true)
		return nil
	}
	report, err := d.mem.Retire(ctx, in)
	if err != nil {
		return fmt.Errorf("retire: %w", err)
	}
	printRetireReport(report, false)
	return nil
}

// retireOlderThanCutoff converts --older-than into the absolute created_at
// threshold the service selects on. The conversion happens at invocation time
// from the wall clock, which is the same source the service clock reads in
// production, so "older than 720h" means "created before now minus 720h" in one
// place only. A zero (or negative) duration yields the zero time, which the
// service reads as "no age predicate" rather than as "everything".
func retireOlderThanCutoff(olderThan time.Duration) time.Time {
	if olderThan <= 0 {
		return time.Time{}
	}
	return time.Now().UTC().Add(-olderThan)
}

// printRetireReport renders the count and the bounded sample shared by a
// --dry-run preview and a real retirement. The two paths print the same
// report by construction; only the verb differs.
func printRetireReport(report memory.RetireReport, dryRun bool) {
	verb := "retired"
	if dryRun {
		verb = "would retire"
	}
	fmt.Printf("%s %d observation(s)\n", verb, report.Count)
	for _, row := range report.Sample {
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("  %s  %s\n", row.ID, capRunes(title, retireSampleWidth))
	}
	if report.Count > len(report.Sample) {
		fmt.Printf("  ... %d more not shown\n", report.Count-len(report.Sample))
	}
	if !dryRun {
		fmt.Println("retirement set valid_until; created_at, invalidated_at and text are unchanged.")
	}
}
