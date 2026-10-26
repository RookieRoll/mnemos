package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
)

// sessionListScanCap bounds one sessions listing scan.
//
// shortcut: offset paging over session rows, upgrade to keyset paging if the
// open-session table exceeds ~10^4 rows. The session service exposes only
// "recent N" and "all open" reads — no status predicate and no offset — so a
// filtered page is cut in the CLI from one bounded scan. The cap keeps a store
// that accumulated years of orphaned sessions from turning a listing into an
// unbounded read; it is far above any plausible single-user history.
const sessionListScanCap = 10000

// sessionStatusOpen is the pseudo-status that selects sessions with no end
// time. It is not a session.Status value: the stored status of an open session
// is its eventual outcome, so "open" has to be its own filter.
const sessionStatusOpen = "open"

// openBacklogSuffix renders the oldest-open-session age for the stats backlog
// line. The count and the timestamp come from one session-service read, so the
// CLI and the MCP stats surfaces cannot disagree about either number.
func openBacklogSuffix(backlog session.OpenBacklog) string {
	if backlog.Count == 0 || backlog.OldestStartedAt.IsZero() {
		return ""
	}
	days := int(time.Since(backlog.OldestStartedAt).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return fmt.Sprintf(" (oldest %dd)", days)
}

// runSessions lists recent sessions, or reclaims stale ones under the close
// subcommand:
//
//	mnemos sessions [--status S] [--limit N] [--offset N] [--json]
//	mnemos sessions close --stale <duration> [--project P] [--status S] [--summary S] [--dry-run]
//
// Listing is read-only. Closing goes through the session service's stale
// selection and per-session close, so preview and execution share one path.
func runSessions(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "close" {
		return runSessionsClose(ctx, args[1:])
	}

	fs := newCommandFlagSet("sessions")
	status := fs.String("status", "", "list only sessions in this state: open | ok | failed | blocked | abandoned")
	limit := fs.Int("limit", 20, "maximum number of rows to list")
	offset := fs.Int("offset", 0, "skip this many rows of the filtered result")
	asJSON := fs.Bool("json", false, "print the page as a JSON array")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments (did you mean: sessions close --stale 24h?)")
	}
	if *limit < 1 {
		return usageErrorf("--limit must be positive, got %d", *limit)
	}
	if *offset < 0 {
		return usageErrorf("--offset must not be negative, got %d", *offset)
	}
	filter, err := parseSessionStatusFilter(*status)
	if err != nil {
		return err
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	rows, err := sessionListRows(ctx, d, filter)
	if err != nil {
		return err
	}
	page := pageSessions(rows, *offset, *limit)
	if *asJSON {
		return encodeSessionsJSON(page)
	}
	if len(page) == 0 {
		if filter == "" && *offset == 0 {
			fmt.Println("no sessions yet")
			return nil
		}
		fmt.Println("no sessions match the filter")
		return nil
	}
	for _, s := range page {
		fmt.Printf("  %s  %s..%s  %-9s %s  %s\n",
			s.ID, s.StartedAt.Format("2006-01-02 15:04"), sessionEndLabel(s), s.Status, s.Project, s.Goal)
	}
	return nil
}

// parseSessionStatusFilter validates --status. An empty value lists every
// session; the literal "open" selects rows with no end time; anything else
// must be a status the session service recognises.
func parseSessionStatusFilter(raw string) (string, error) {
	if raw == "" || raw == sessionStatusOpen {
		return raw, nil
	}
	if !session.Status(raw).Valid() {
		return "", usageErrorf("invalid --status %q (want open | ok | failed | blocked | abandoned)", raw)
	}
	return raw, nil
}

// sessionListRows reads the rows a listing paginates over: open sessions come
// from the open-session read, everything else from one bounded recent scan.
func sessionListRows(ctx context.Context, d *deps, filter string) ([]session.Session, error) {
	if filter == sessionStatusOpen {
		rows, err := d.sess.ListOpen(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("list open sessions: %w", err)
		}
		return rows, nil
	}
	rows, err := d.sess.Recent(ctx, "", sessionListScanCap)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if filter == "" {
		return rows, nil
	}
	kept := make([]session.Session, 0, len(rows))
	for _, s := range rows {
		if string(s.Status) == filter {
			kept = append(kept, s)
		}
	}
	return kept, nil
}

// pageSessions applies offset/limit to an ordered selection.
func pageSessions(rows []session.Session, offset, limit int) []session.Session {
	if offset >= len(rows) {
		return nil
	}
	rows = rows[offset:]
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

// sessionEndLabel renders the end column of the text listing.
func sessionEndLabel(s session.Session) string {
	if s.EndedAt == nil {
		return "open"
	}
	return s.EndedAt.Format("2006-01-02 15:04")
}

// sessionPageRow is the wire shape of one listing row. It carries exactly the
// fields the text form prints, so --json is the same page in another shape.
type sessionPageRow struct {
	ID        string     `json:"id"`
	Project   string     `json:"project"`
	Goal      string     `json:"goal"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// encodeSessionsJSON prints the page as a JSON array. An empty page encodes as
// [] rather than null so a consumer can index the result unconditionally.
func encodeSessionsJSON(rows []session.Session) error {
	out := make([]sessionPageRow, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionPageRow{
			ID: s.ID, Project: s.Project, Goal: s.Goal, Status: string(s.Status),
			StartedAt: s.StartedAt, EndedAt: s.EndedAt,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode sessions: %w", err)
	}
	return nil
}

// runSessionsClose reclaims open sessions that started before now minus a
// duration. A non-positive duration is a usage error: --stale 0 would include
// the session that just started. --dry-run runs the identical selection and
// prints the identical report without closing anything.
func runSessionsClose(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("sessions close")
	stale := fs.Duration("stale", 0, "close open sessions that started more than this duration ago (e.g. 24h)")
	project := fs.String("project", "", "limit the sweep to one project")
	status := fs.String("status", string(session.StatusAbandoned), "status recorded on each closed session")
	summary := fs.String("summary", "", "summary recorded on each closed session")
	limit := fs.Int("limit", 0, "close at most this many sessions (0 = no cap)")
	dryRun := fs.Bool("dry-run", false, "print the selection without closing anything")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	if *stale <= 0 {
		return usageErrorf("--stale <duration> is required and must be positive, got %s", *stale)
	}
	if *limit < 0 {
		return usageErrorf("--limit must not be negative, got %d", *limit)
	}
	closeStatus := session.Status(*status)
	if !closeStatus.Valid() {
		return usageErrorf("invalid --status %q (want open | ok | failed | blocked | abandoned)", *status)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	cutoff, err := d.sess.StaleCutoff(*stale)
	if err != nil {
		// StaleCutoff repeats the positivity guard for non-CLI callers; the
		// CLI already rejected the duration above, so this is defensive.
		return usageErrorf("%v", err)
	}
	targets, err := d.sess.SelectStaleOpen(ctx, session.StaleOpenInput{
		Before: cutoff, Project: *project, Limit: *limit,
	})
	if err != nil {
		return fmt.Errorf("sessions close: %w", err)
	}

	// Print the rows first. Preview and execution share the selection, so
	// the two reports name the same sessions and the same count.
	for _, s := range targets {
		fmt.Printf("  %s  %s  %s  %s\n", s.ID, s.StartedAt.Format("2006-01-02 15:04"), s.Project, s.Goal)
	}
	if *dryRun {
		fmt.Printf("would close %d session(s) (dry run, nothing written)\n", len(targets))
		return nil
	}
	closed, err := d.sess.CloseEach(ctx, targets, session.CloseInput{Status: closeStatus, Summary: *summary})
	if err != nil {
		return fmt.Errorf("sessions close: %w", err)
	}
	fmt.Printf("closed %d session(s)\n", closed)
	return nil
}
