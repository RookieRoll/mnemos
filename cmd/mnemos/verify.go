package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/verify"
)

// runVerify dispatches the verify subcommand tree:
//
//	mnemos verify retrieval [fixture]   — cheap, runs against live store
//	mnemos verify behavior  [fixture]   — expensive, claude A/B (read side)
//	mnemos verify capture   [fixture]   — expensive, single-arm capture rate (write side)
//	mnemos verify calibrate [fixture]   — cheap, sweeps gate params (score contract)
//	mnemos verify all                   — retrieval + behavior + capture
func runVerify(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos verify <retrieval|behavior|capture|calibrate|all> [fixture]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "retrieval":
		return runVerifyRetrieval(ctx, rest)
	case "calibrate":
		return runVerifyCalibrate(ctx, rest)
	case "behavior":
		return runVerifyBehavior(ctx, rest)
	case "capture":
		return runVerifyCapture(ctx, rest)
	case "all":
		if err := runVerifyRetrieval(ctx, rest); err != nil {
			return err
		}
		fmt.Println()
		if err := runVerifyBehavior(ctx, rest); err != nil {
			return err
		}
		fmt.Println()
		return runVerifyCapture(ctx, rest)
	default:
		return fmt.Errorf("unknown verify subcommand: %s", sub)
	}
}

func runVerifyRetrieval(ctx context.Context, args []string) error {
	path := "verify/retrieval.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadRetrievalFixture(path)
	if err != nil {
		return err
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	now := time.Now()
	rep, err := verify.RunRetrievalWithDelivery(ctx, d.mem, fix, func(ctx context.Context, c verify.RetrievalCase, q string) ([]injection.Ref, error) {
		if c.IncludeRaw || c.IncludeStale || c.AgentID != "" {
			return nil, fmt.Errorf("prompt consumer does not support raw, stale or agent-filtered delivery evaluation")
		}
		pm := collectPromptMemoryAt(ctx, d, q, "", c.Project, "", now, false)
		refs := make([]injection.Ref, 0, len(pm.Hits))
		for _, h := range pm.Hits {
			refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
		}
		return refs, nil
	})
	if err != nil {
		return fmt.Errorf("retrieval: %w", err)
	}
	printRetrievalReport(os.Stdout, rep)
	if rep.Passed < rep.Total {
		return fmt.Errorf("retrieval: %d/%d probes failed", rep.Total-rep.Passed, rep.Total)
	}
	return nil
}

// runVerifyCalibrate sweeps the injection floor and the cross-project
// penalty over the retrieval fixture plus injection-log usage history and
// prints the parameter report. Read-only: it searches and reads the
// injection log, nothing else. The report's values are what the gate
// constants should carry; record the basis in docs/ARCHITECTURE.md.
func runVerifyCalibrate(ctx context.Context, args []string) error {
	path := "verify/retrieval.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadRetrievalFixture(path)
	if err != nil {
		return err
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	// Surfacing counts come from the injection log; a memory accessed more
	// often than it was surfaced carries a legacy usage proxy, which the
	// compatibility sweep weighs double. This does not establish agent use
	// or a successful outcome.
	surfaced := func(ctx context.Context, id string) int {
		evts, err := d.db.Injections().ListByRef(ctx, injection.KindObservation, id, 10000)
		if err != nil {
			return 0
		}
		return len(evts)
	}

	if len(fix.Cases) > 0 {
		labelled, err := verify.CalibrateLabelledGate(ctx, d.mem, fix)
		if err != nil {
			return fmt.Errorf("calibrate labelled: %w", err)
		}
		fmt.Fprintf(os.Stdout, "Calibration basis: %s\nCoverage: positive=%d negative=%d unlabelled=%d queries=%d separation_measured=%t\n", labelled.Basis, labelled.Coverage.Positive, labelled.Coverage.Negative, labelled.Coverage.Unlabelled, labelled.Coverage.Queries, labelled.Coverage.SeparationMeasured)
		for _, q := range labelled.Queries {
			fmt.Fprintf(os.Stdout, "  %s %q positive=%d negative=%d unlabelled=%d\n", q.Case, q.Query, q.Positive, q.Negative, q.Unlabelled)
		}
		if labelled.Coverage.SeparationMeasured {
			printCalibrationReport(os.Stdout, labelled.Sweep)
		} else {
			fmt.Fprintln(os.Stdout, "Separation was not measured: empty or single-class labelled coverage.")
		}
		if len(fix.Probes) == 0 {
			return nil
		}
	}
	fmt.Fprintln(os.Stdout, "Calibration basis: legacy single-target proxy; access/surfacing counts are usage proxies, not verified success.")
	legacy := &verify.RetrievalFixture{Probes: fix.Probes}
	rep, _, err := verify.CalibrateGate(ctx, d.mem, legacy, surfaced)
	if err != nil {
		return fmt.Errorf("calibrate: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Legacy coverage: positive=%d negative=%d separation_measured=%t\n", rep.OnTopic, rep.Samples-rep.OnTopic, rep.OnTopic > 0 && rep.OnTopic < rep.Samples)
	if rep.OnTopic == 0 || rep.OnTopic == rep.Samples {
		fmt.Fprintln(os.Stdout, "Separation was not measured: empty or single-class legacy proxy coverage; the following sweep values are compatibility diagnostics, not validated recommendations.")
	}
	printCalibrationReport(os.Stdout, rep)
	return nil
}

// printCalibrationReport renders the gate parameter report on the
// composite score contract scale.
func printCalibrationReport(w io.Writer, rep memory.SweepReport) {
	fmt.Fprintf(w, "Gate calibration — composite score contract scale (ceiling %.1f)\n", memory.ScoreCeiling)
	fmt.Fprintf(w, "  samples:       %d (on-topic %d, used %d)\n", rep.Samples, rep.OnTopic, rep.Used)
	fmt.Fprintf(w, "  floor:         %.2f\n", rep.Floor)
	fmt.Fprintf(w, "  penalty:       %.2f  (cross-project multiplier)\n", rep.Penalty)
	fmt.Fprintf(w, "  balanced acc:  %.3f\n", rep.BalancedAcc)
}

func runVerifyBehavior(ctx context.Context, args []string) error {
	path := "verify/behavior.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadBehaviorFixture(path)
	if err != nil {
		return err
	}
	rep, err := verify.RunBehavior(ctx, verify.ExecExecutor{}, fix)
	if err != nil {
		return fmt.Errorf("behavior: %w", err)
	}
	printBehaviorReport(os.Stdout, rep)
	return nil
}

func runVerifyCapture(ctx context.Context, args []string) error {
	path := "verify/capture.yaml"
	if len(args) > 0 {
		path = args[0]
	}
	fix, err := verify.LoadCaptureFixture(path)
	if err != nil {
		return err
	}
	rep, err := verify.RunCapture(ctx, verify.ExecExecutor{}, fix)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	printCaptureReport(os.Stdout, rep)
	return nil
}

// printRetrievalReport renders one row per probe plus a summary line.
// Failed probes show every query rank so it's obvious whether the target
// was missing entirely (rank 0) or just buried below the threshold.
func printRetrievalReport(w *os.File, rep *verify.RetrievalReport) {
	fmt.Fprintln(w, "retrieval report")
	fmt.Fprintln(w, "================")
	for _, p := range rep.Probes {
		status := "PASS"
		if !p.Pass {
			status = "FAIL"
		}
		title := p.Probe.Title
		if title == "" {
			title = p.Probe.ID
		}
		fmt.Fprintf(w, "  [%s] %s  (best rank %d, want ≤ %d)\n",
			status, title, p.BestRank, p.Probe.ExpectInTop)
		if !p.Pass {
			for _, q := range p.Queries {
				rank := fmt.Sprintf("%d", q.Rank)
				if q.Rank == 0 {
					rank = "miss"
				}
				fmt.Fprintf(w, "      %-6s  %q\n", rank, q.Query)
			}
		}
	}
	for _, c := range rep.Cases {
		fmt.Fprintf(w, "  case %s pass=%t mode=%s\n", c.Case.Name, c.Pass, c.Case.QueryMode)
		for _, q := range c.Queries {
			fmt.Fprintf(w, "    %q pass=%t labelled_recall=%.3f relevant=%v forbidden=%v unlabelled=%v delivery_measured=%t selected=%v\n", q.Query, q.Pass, q.Recall, q.RelevantIDs, q.ForbiddenIDs, q.UnlabelledIDs, q.DeliveryMeasured, q.Selected)
		}
	}
	fmt.Fprintf(w, "\n  %d/%d probes/cases passed (legacy any-query; explicit labelled cases)\n",
		rep.Passed, rep.Total)
}

// printCaptureReport shows per-scenario capture rate plus aggregate.
// Single-arm so there's no "lift" — just whether the agent recorded what
// the user said when they said it.
func printCaptureReport(w *os.File, rep *verify.CaptureReport) {
	fmt.Fprintln(w, "capture report")
	fmt.Fprintln(w, "==============")
	fmt.Fprintf(w, "  %-32s  %-10s  %s\n", "scenario", "captured", "rate")
	totalCap, totalRuns := 0, 0
	for _, s := range rep.Scenarios {
		fmt.Fprintf(w, "  %-32s  %d/%-8d  %3.0f%%\n",
			truncate(s.Scenario.Name, 32),
			s.Captured, s.Runs, s.Rate()*100)
		if len(s.Errors) > 0 {
			fmt.Fprintf(w, "      %d run errors\n", len(s.Errors))
		}
		totalCap += s.Captured
		totalRuns += s.Runs
	}
	rate := 0.0
	if totalRuns > 0 {
		rate = float64(totalCap) / float64(totalRuns)
	}
	fmt.Fprintf(w, "\n  overall capture rate: %d/%d (%.0f%%)\n", totalCap, totalRuns, rate*100)
}

// printBehaviorReport shows on/off pass counts and lift per scenario.
func printBehaviorReport(w *os.File, rep *verify.BehaviorReport) {
	fmt.Fprintln(w, "behavior report")
	fmt.Fprintln(w, "===============")
	fmt.Fprintf(w, "  %-30s  %-7s  %-7s  %s\n", "scenario", "on", "off", "lift")
	for _, s := range rep.Scenarios {
		fmt.Fprintf(w, "  %-30s  %d/%-5d  %d/%-5d  %+.0f%%\n",
			truncate(s.Scenario.Name, 30),
			s.OnPass, s.Runs, s.OffPass, s.Runs, s.Lift()*100)
		if len(s.Errors) > 0 {
			fmt.Fprintf(w, "      %d run errors\n", len(s.Errors))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
