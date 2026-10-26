package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// Observations returns the observation store backed by this DB.
func (d *DB) Observations() memory.Store { return &obsStore{db: d.sql, path: d.path} }

// FencedObservations returns the observation store's transaction-fenced
// maintenance surface for dream passes.
func (d *DB) FencedObservations() memory.FencedMaintenance {
	return &obsStore{db: d.sql, path: d.path}
}

// obsStore carries the database path next to the handle because Stats reports
// the on-disk size, which is a filesystem fact rather than a SQL one.
type obsStore struct {
	db   *sql.DB
	path string
}

const obsColumns = `
	id, session_id, agent_id, project,
	title, content, obs_type, tags, importance,
	access_count, last_accessed_at,
	created_at, valid_from, valid_until,
	invalidated_at, expires_at,
	content_hash, structured, rationale,
	embedding, embedding_model, last_exported_at,
	source_kind, trust_tier, derived_from,
	pinned, topic_key`

const evidenceProjection = `COALESCE((SELECT json_group_array(json_object('key', e.evidence_key, 'locator', e.locator, 'relation', e.relation, 'revision', e.revision, 'selector', e.selector, 'fingerprint', e.fingerprint, 'status', e.status, 'checked_at', e.checked_at)) FROM (SELECT * FROM observation_evidence ORDER BY evidence_key) e WHERE e.observation_id = %s.id), '[]')`

const selectObsSQL = `SELECT ` + obsColumns + `, COALESCE((SELECT json_group_array(json_object('key', e.evidence_key, 'locator', e.locator, 'relation', e.relation, 'revision', e.revision, 'selector', e.selector, 'fingerprint', e.fingerprint, 'status', e.status, 'checked_at', e.checked_at)) FROM (SELECT * FROM observation_evidence ORDER BY evidence_key) e WHERE e.observation_id = observations.id), '[]') FROM observations`

func observationProjection(alias string) string {
	return prefixed(alias, obsColumns) + ", " + fmt.Sprintf(evidenceProjection, alias)
}

func liveObsSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + `invalidated_at IS NULL
	   AND (` + prefix + `valid_until IS NULL OR ` + prefix + `valid_until > ?)
	   AND (` + prefix + `expires_at  IS NULL OR ` + prefix + `expires_at  > ?)`
}

func (s *obsStore) Insert(ctx context.Context, o *memory.Observation) error {
	return s.insertWithEvidence(ctx, o)
}

// Restore inserts an observation verbatim, preserving its ID, bi-temporal
// timestamps (including invalidated_at), provenance, access count, content
// hash, and embedding exactly as exported. Unlike Insert (the Save path), it
// writes every column and uses INSERT OR IGNORE, so re-importing an already
// present ID (or a skill-style uniqueness clash) is skipped rather than
// erroring. It never clamps the trust tier, so a quarantined raw observation
// comes back raw. Returns true when a row was actually written.
func (s *obsStore) Restore(ctx context.Context, o *memory.Observation) (bool, error) {
	return s.restoreWithEvidence(ctx, o)
}

// Export returns every observation, all tiers and validity states included,
// in a single drained query. Unlike Get it does not bump access counts, so an
// export never mutates the store, and unlike iterating ids + Get it never
// holds a cursor open across a second query, which would deadlock the
// single-connection pool. Used by the fidelity export path.
func (s *obsStore) Export(ctx context.Context) ([]memory.Observation, error) {
	rows, err := s.db.QueryContext(ctx, selectObsSQL+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("export observations: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) Get(ctx context.Context, id string) (*memory.Observation, error) {
	row := s.db.QueryRowContext(ctx, selectObsSQL+` WHERE id = ?`, id)
	o, err := scanObs(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, memory.ErrNotFound
		}
		return nil, err
	}
	if err := s.BumpAccess(ctx, id); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *obsStore) BumpAccess(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE observations SET access_count = access_count + 1,
		        last_accessed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("bump access: %w", err)
	}
	return nil
}

func (s *obsStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM observations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

// SetTrustTier updates an observation's trust_tier. No bi-temporal effect:
// trust is a retrieval filter, not a fact-time property. The service layer
// enforces that tier is valid and that the caller supplied a why_better
// justification before promoting.
func (s *obsStore) SetTrustTier(ctx context.Context, id string, tier memory.TrustTier) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE observations SET trust_tier = ? WHERE id = ?`,
		string(tier), id)
	if err != nil {
		return fmt.Errorf("set trust tier: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

// SetPinned sets or unsets the pinned core-memory flag. Pinning clears any
// existing expiry (so core memory cannot be pruned by TTL); unpinning leaves
// expiry unset.
func (s *obsStore) SetPinned(ctx context.Context, id string, pinned bool) error {
	var query string
	if pinned {
		query = `UPDATE observations SET pinned = 1, expires_at = NULL WHERE id = ?`
	} else {
		query = `UPDATE observations SET pinned = 0 WHERE id = ?`
	}
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("set pinned: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) Invalidate(ctx context.Context, id string, validUntil time.Time) error {
	// Explicit Go-side timestamp preserves sub-second precision — important
	// for bi-temporal queries and replay filtering.
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE observations
		   SET valid_until    = COALESCE(valid_until, ?),
		       invalidated_at = ?
		 WHERE id = ? AND invalidated_at IS NULL`,
		validUntil.UTC(), now, id)
	if err != nil {
		return fmt.Errorf("invalidate: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) Search(ctx context.Context, in memory.SearchInput) ([]memory.SearchResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	asOf := in.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("search: query is required")
	}

	args := []any{ftsEscape(in.Query)}
	var sb strings.Builder
	sb.WriteString(`SELECT ` + observationProjection("o") + `,
	       bm25(observations_fts) AS score,
	       snippet(observations_fts, 1, '', '', '…', 16) AS snip
	  FROM observations_fts
	  JOIN observations o ON o.rowid = observations_fts.rowid
	 WHERE observations_fts MATCH ?`)

	if in.AgentID != "" {
		sb.WriteString(` AND o.agent_id = ?`)
		args = append(args, in.AgentID)
	}
	if in.Project != "" {
		sb.WriteString(` AND o.project = ?`)
		args = append(args, in.Project)
	}
	if in.TopicKey != "" {
		sb.WriteString(` AND o.topic_key = ?`)
		args = append(args, in.TopicKey)
	}
	if in.Type != "" {
		sb.WriteString(` AND o.obs_type = ?`)
		args = append(args, string(in.Type))
	}
	if in.MinImportance > 0 {
		sb.WriteString(` AND o.importance >= ?`)
		args = append(args, in.MinImportance)
	}
	if !in.IncludeStale {
		// Bi-temporal: invalidated_at is system time (when WE recorded the
		// invalidation); valid_until is fact time. For a historical AsOf
		// query we want everything that was true at AsOf, even if it was
		// invalidated in our system afterwards.
		sb.WriteString(` AND (o.invalidated_at IS NULL OR o.invalidated_at > ?)`)
		args = append(args, asOf.UTC())
		sb.WriteString(` AND (o.valid_until IS NULL OR o.valid_until > ?)`)
		args = append(args, asOf.UTC())
		sb.WriteString(` AND (o.expires_at  IS NULL OR o.expires_at  > ?)`)
		args = append(args, asOf.UTC())
	}
	if !in.IncludeRaw {
		// Quarantine: raw-tier observations come from untrusted sources
		// (tool output, agent inference not yet confirmed). Excluded
		// from default search; opt in with IncludeRaw=true.
		sb.WriteString(` AND o.trust_tier != 'raw'`)
	}
	for _, tag := range in.Tags {
		sb.WriteString(` AND o.tags LIKE ?`)
		args = append(args, "%"+jsonQuote(tag)+"%")
	}

	sb.WriteString(` ORDER BY score LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	results := make([]memory.SearchResult, 0, limit)
	for rows.Next() {
		o, score, snippet, err := scanObsWithScore(rows)
		if err != nil {
			return nil, err
		}
		// FTS5 bm25() returns a negative score by convention (lower = better).
		// Flip to positive "relevance".
		bm25 := -score
		results = append(results, memory.SearchResult{
			Observation: *o,
			Score:       bm25,
			BM25:        bm25,
			Snippet:     snippet,
		})
	}
	return results, rows.Err()
}

func (s *obsStore) Link(ctx context.Context, sourceID, targetID string, linkType memory.LinkType) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO observation_links(source_id, target_id, link_type)
		VALUES (?, ?, ?)`, sourceID, targetID, string(linkType))
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	return nil
}

// ListLinks returns edges of the given link type where both endpoints are
// still live. Callers pass agentID to scope to their agent; empty means
// all agents. The source and target titles come back inline so monitors
// don't need a second round-trip per edge. Ordered by link created_at
// descending so the newest contradictions rise first.
func (s *obsStore) ListLinks(ctx context.Context, linkType memory.LinkType, agentID string, limit int) ([]memory.LinkEdge, error) {
	if limit <= 0 {
		limit = 1000
	}
	now := time.Now().UTC()
	query := `
			SELECT l.source_id, src.title, src.agent_id, src.created_at, src.trust_tier,
			       l.target_id, tgt.title, tgt.agent_id, tgt.created_at, tgt.trust_tier,
			       l.created_at
			  FROM observation_links l
			  JOIN observations src ON src.id = l.source_id
			  JOIN observations tgt ON tgt.id = l.target_id
			 WHERE l.link_type = ?
			   AND ` + liveObsSQL("src") + `
			   AND ` + liveObsSQL("tgt")
	args := []any{string(linkType), now, now, now, now}
	if agentID != "" {
		query += ` AND tgt.agent_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY l.created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()

	var out []memory.LinkEdge
	for rows.Next() {
		var e memory.LinkEdge
		var srcTier, tgtTier string
		if err := rows.Scan(
			&e.SourceID, &e.SourceTitle, &e.SourceAgent, &e.SourceCreatedAt, &srcTier,
			&e.TargetID, &e.TargetTitle, &e.TargetAgent, &e.TargetCreatedAt, &tgtTier,
			&e.LinkedAt,
		); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		e.LinkType = linkType
		e.SourceCreatedAt = e.SourceCreatedAt.UTC()
		e.TargetCreatedAt = e.TargetCreatedAt.UTC()
		e.LinkedAt = e.LinkedAt.UTC()
		e.SourceTrustTier = memory.TrustTier(srcTier)
		e.TargetTrustTier = memory.TrustTier(tgtTier)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPinned returns live, unquarantined pinned observations in scope,
// ordered by importance desc then created_at asc.
func (s *obsStore) ListPinned(ctx context.Context, agentID, project string, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().UTC()
	query := selectObsSQL + ` WHERE pinned = 1 AND trust_tier != 'raw' AND ` + liveObsSQL("")
	args := []any{now, now}
	if agentID != "" {
		query += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	if project != "" {
		query += ` AND (project = ? OR project IS NULL OR project = '')`
		args = append(args, project)
	}
	query += ` ORDER BY importance DESC, created_at ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pinned: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

// ListByTrustTier returns live observations at the given tier, oldest
// first. The StaleRawObservationMonitor uses this to surface raw-tier
// observations that have been sitting in quarantine too long without a
// promotion decision either way.
func (s *obsStore) ListByTrustTier(ctx context.Context, agentID string, tier memory.TrustTier, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().UTC()
	args := []any{string(tier), now, now}
	query := selectObsSQL + ` WHERE trust_tier = ? AND ` + liveObsSQL("")
	if agentID != "" {
		query += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY created_at ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list by trust_tier: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) Prune(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM observations WHERE pinned = 0 AND expires_at IS NOT NULL AND expires_at <= ?`,
		now.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Stats returns the aggregate counts that back mnemos_stats, plus the
// database's on-disk size. The counts are one query each; the size comes from
// the filesystem and can never fail the call, because a diagnostics field must
// not turn into an outage.
func (s *obsStore) Stats(ctx context.Context) (memory.Stats, error) {
	var st memory.Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).
		Scan(&st.Observations); err != nil {
		return st, fmt.Errorf("count observations: %w", err)
	}
	now := time.Now().UTC()
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE `+liveObsSQL(""), now, now).
		Scan(&st.LiveObservations); err != nil {
		return st, fmt.Errorf("count live: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).
		Scan(&st.Sessions); err != nil {
		return st, fmt.Errorf("count sessions: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills`).
		Scan(&st.Skills); err != nil {
		return st, fmt.Errorf("count skills: %w", err)
	}
	st.TopTags = collectTopTags(ctx, s.db)
	st.RecentSessions = collectRecentSessions(ctx, s.db, 5)
	st.StorageBytes = databaseStorageBytes(s.path)
	return st, nil
}

func (s *obsStore) ListByTitleSimilarity(ctx context.Context, agentID, title string, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+observationProjection("o")+`
		  FROM observations o
		  JOIN observations_fts fts ON fts.rowid = o.rowid
		 WHERE o.agent_id = ? AND observations_fts MATCH ?
		 ORDER BY bm25(observations_fts) LIMIT ?`,
		agentID, ftsEscape(title), limit)
	if err != nil {
		return nil, fmt.Errorf("similarity search: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

// DecayImportance lowers importance on observations idle past staleDays,
// skipping pinned rows: core memory is exempt from the dream pass's idle
// decay, part of the pinned lifecycle (docs/ARCHITECTURE.md).
func (s *obsStore) DecayImportance(ctx context.Context, staleDays int, amount int) (int64, error) {
	if staleDays <= 0 || amount <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -staleDays)
	res, err := s.db.ExecContext(ctx, `
		UPDATE observations
		   SET importance = MAX(1, importance - ?)
		 WHERE importance > 1
		   AND pinned = 0
		   AND COALESCE(last_accessed_at, created_at) < ?`,
		amount, cutoff)
	if err != nil {
		return 0, fmt.Errorf("decay: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *obsStore) FindByContentHash(ctx context.Context, agentID, project, hash string) (*memory.Observation, error) {
	if hash == "" {
		return nil, nil
	}
	args := []any{nullableStr(agentID), hash}
	query := selectObsSQL + ` WHERE agent_id = COALESCE(?, agent_id) AND content_hash = ? AND COALESCE(topic_key,'') = '' AND NOT EXISTS (SELECT 1 FROM observation_evidence e WHERE e.observation_id = observations.id)`
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	now := time.Now().UTC()
	query += ` AND ` + liveObsSQL("") + ` LIMIT 1`
	args = append(args, now, now)
	row := s.db.QueryRowContext(ctx, query, args...)
	o, err := scanObs(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return o, nil
}

func (s *obsStore) ListBySession(ctx context.Context, sessionID string) ([]memory.Observation, error) {
	// Returns ALL observations for a session, including invalidated ones.
	// Replay needs this so it can flag which session-observations were
	// superseded after the fact.
	rows, err := s.db.QueryContext(ctx,
		selectObsSQL+` WHERE session_id = ? ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list by session: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) ListByProject(ctx context.Context, agentID, project string, obsType memory.ObsType, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 20
	}
	now := time.Now().UTC()
	args := []any{now, now}
	query := selectObsSQL + ` WHERE ` + liveObsSQL("")
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	if agentID != "" {
		query += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	if obsType != "" {
		query += ` AND obs_type = ?`
		args = append(args, string(obsType))
	}
	query += ` ORDER BY importance DESC, created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list by project: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) UpdateEmbedding(ctx context.Context, id, model string, vec []float32) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE observations SET embedding = ?, embedding_model = ? WHERE id = ?`,
		encodeVector(vec), nullableStr(model), id)
	if err != nil {
		return fmt.Errorf("update embedding: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return memory.ErrNotFound
	}
	return nil
}

func (s *obsStore) ListMissingEmbeddings(ctx context.Context, limit int) ([]memory.Observation, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		selectObsSQL+` WHERE embedding IS NULL AND `+liveObsSQL("")+` ORDER BY created_at DESC LIMIT ?`,
		now, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list missing embeddings: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

// ListEmbeddings returns live observations in the query's scope that carry a
// stored embedding vector. The vector-recall path brute-forces cosine over
// this set, so there is deliberately no LIMIT: the scope filters (agent,
// project, type, importance, tags) are what bound the scan. Ordered by
// created_at descending then id so iteration is deterministic across calls.
// The Query field is ignored; this is a scoped scan, not a text search.
//
// ponytail: unbounded scoped read; the recall benchmark documents the
// measured ~10k-row ceiling. Revisit with a LIMIT or an index only if it bites.
func (s *obsStore) ListEmbeddings(ctx context.Context, in memory.SearchInput) ([]memory.Observation, error) {
	asOf := in.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	var sb strings.Builder
	sb.WriteString(selectObsSQL + ` WHERE embedding IS NOT NULL`)
	args := []any{}
	if in.AgentID != "" {
		sb.WriteString(` AND agent_id = ?`)
		args = append(args, in.AgentID)
	}
	if in.TopicKey != "" {
		sb.WriteString(` AND topic_key = ?`)
		args = append(args, in.TopicKey)
	}
	if in.Project != "" {
		sb.WriteString(` AND project = ?`)
		args = append(args, in.Project)
	}
	if in.Type != "" {
		sb.WriteString(` AND obs_type = ?`)
		args = append(args, string(in.Type))
	}
	if in.MinImportance > 0 {
		sb.WriteString(` AND importance >= ?`)
		args = append(args, in.MinImportance)
	}
	if !in.IncludeStale {
		sb.WriteString(` AND (invalidated_at IS NULL OR invalidated_at > ?)`)
		args = append(args, asOf.UTC())
		sb.WriteString(` AND (valid_until IS NULL OR valid_until > ?)`)
		args = append(args, asOf.UTC())
		sb.WriteString(` AND (expires_at  IS NULL OR expires_at  > ?)`)
		args = append(args, asOf.UTC())
	}
	if !in.IncludeRaw {
		sb.WriteString(` AND trust_tier != 'raw'`)
	}
	for _, tag := range in.Tags {
		sb.WriteString(` AND tags LIKE ?`)
		args = append(args, "%"+jsonQuote(tag)+"%")
	}
	sb.WriteString(` ORDER BY created_at DESC, id`)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list embeddings: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

func (s *obsStore) MarkExported(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE observations SET last_exported_at = ? WHERE id = ?`, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("mark exported: %w", err)
	}
	return nil
}

// ListNeighborsFor returns live observations reachable from the given
// candidate IDs along the expansion edge set: `refines` link rows and
// `derived_from` provenance references, each followed in both directions
// (so a candidate's refinements, its refiners, its derivation parents,
// and the observations derived from it all surface). Results are ordered
// deterministically (parent, neighbour created_at, neighbour id) and
// capped at perParent entries per parent (no cap when perParent <= 0), so
// graph expansion is reproducible run to run.
func (s *obsStore) ListNeighborsFor(ctx context.Context, ids []string, perParent int) ([]memory.Neighbour, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	ph := placeholders(len(ids))
	query := `
		SELECT e.parent_id, ` + observationProjection("o") + `
		  FROM (
		        SELECT l.source_id AS parent_id, l.target_id AS nb_id
		          FROM observation_links l
		         WHERE l.link_type = ? AND l.source_id IN (` + ph + `)
		        UNION
		        SELECT l.target_id, l.source_id
		          FROM observation_links l
		         WHERE l.link_type = ? AND l.target_id IN (` + ph + `)
		        UNION
		        SELECT c.id, je.value
		          FROM observations c, json_each(c.derived_from) je
		         WHERE c.id IN (` + ph + `)
		        UNION
		        SELECT je.value, d.id
		          FROM observations d, json_each(d.derived_from) je
		         WHERE je.value IN (` + ph + `)
		       ) e
		  JOIN observations o ON o.id = e.nb_id
		 WHERE ` + liveObsSQL("o") + `
		 ORDER BY e.parent_id, o.created_at, o.id`
	args := make([]any, 0, 4*len(ids)+4)
	args = append(args, string(memory.LinkRefines))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, string(memory.LinkRefines))
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, now, now)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list neighbours: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	var out []memory.Neighbour
	for rows.Next() {
		var parentID string
		o := &memory.Observation{}
		d := newObsScanDest(o)
		if err := rows.Scan(append([]any{&parentID}, d.args()...)...); err != nil {
			return nil, fmt.Errorf("scan neighbour: %w", err)
		}
		if err := d.finalise(); err != nil {
			return nil, err
		}
		if perParent > 0 && counts[parentID] >= perParent {
			continue
		}
		counts[parentID]++
		out = append(out, memory.Neighbour{
			ParentID:    parentID,
			Observation: *o,
		})
	}
	return out, rows.Err()
}

// ListContradictors returns live `contradicts` refutations of the given
// candidate IDs, following the edge in both directions: the candidate may
// sit at either endpoint. Each entry pairs the candidate with the live
// refuting observation; its trust tier carries how much weight the
// refutation deserves. Ordered by (candidate id, refuter id).
func (s *obsStore) ListContradictors(ctx context.Context, ids []string) ([]memory.Contradictor, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	ph := placeholders(len(ids))
	query := `
		SELECT e.candidate_id, ` + observationProjection("o") + `
		  FROM (
		        SELECT source_id AS candidate_id, target_id AS other_id
		          FROM observation_links
		         WHERE link_type = ? AND source_id IN (` + ph + `)
		        UNION
		        SELECT target_id AS candidate_id, source_id AS other_id
		          FROM observation_links
		         WHERE link_type = ? AND target_id IN (` + ph + `)
		       ) e
		  JOIN observations o ON o.id = e.other_id
		 WHERE ` + liveObsSQL("o") + `
		 ORDER BY e.candidate_id, e.other_id`
	args := make([]any, 0, 2*len(ids)+4)
	args = append(args, string(memory.LinkContradicts))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, string(memory.LinkContradicts))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, now, now)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list contradictors: %w", err)
	}
	defer rows.Close()

	var out []memory.Contradictor
	for rows.Next() {
		var candidateID string
		o := &memory.Observation{}
		d := newObsScanDest(o)
		if err := rows.Scan(append([]any{&candidateID}, d.args()...)...); err != nil {
			return nil, fmt.Errorf("scan contradictor: %w", err)
		}
		if err := d.finalise(); err != nil {
			return nil, err
		}
		out = append(out, memory.Contradictor{
			CandidateID: candidateID,
			Observation: *o,
		})
	}
	return out, rows.Err()
}

// --- scanners -----------------------------------------------------------

type obsScanDest struct {
	o                         *memory.Observation
	sessID, project           *sql.NullString
	contentHash, structured   *sql.NullString
	rationale, embeddingModel *sql.NullString
	lastAcc, validUntil       *sql.NullTime
	invalidatedAt, expiresAt  *sql.NullTime
	lastExportedAt            *sql.NullTime
	tagsJSON                  *string
	embedding                 *[]byte
	sourceKind, trustTier     *string
	derivedFromJSON           *string
	pinned                    *int64
	topicKey                  *sql.NullString
	evidenceJSON              *string
}

func newObsScanDest(o *memory.Observation) obsScanDest {
	return obsScanDest{
		o:               o,
		sessID:          &sql.NullString{},
		project:         &sql.NullString{},
		contentHash:     &sql.NullString{},
		structured:      &sql.NullString{},
		rationale:       &sql.NullString{},
		embeddingModel:  &sql.NullString{},
		lastAcc:         &sql.NullTime{},
		validUntil:      &sql.NullTime{},
		invalidatedAt:   &sql.NullTime{},
		expiresAt:       &sql.NullTime{},
		lastExportedAt:  &sql.NullTime{},
		tagsJSON:        new(string),
		embedding:       new([]byte),
		sourceKind:      new(string),
		trustTier:       new(string),
		derivedFromJSON: new(string),
		pinned:          new(int64),
		topicKey:        &sql.NullString{},
		evidenceJSON:    new(string),
	}
}

func (d obsScanDest) args() []any {
	return []any{
		&d.o.ID, d.sessID, &d.o.AgentID, d.project,
		&d.o.Title, &d.o.Content, &d.o.Type, d.tagsJSON, &d.o.Importance,
		&d.o.AccessCount, d.lastAcc,
		&d.o.CreatedAt, &d.o.ValidFrom, d.validUntil,
		d.invalidatedAt, d.expiresAt,
		d.contentHash, d.structured, d.rationale,
		d.embedding, d.embeddingModel, d.lastExportedAt,
		d.sourceKind, d.trustTier, d.derivedFromJSON,
		d.pinned, d.topicKey, d.evidenceJSON,
	}
}

func (d obsScanDest) finalise() error {
	d.o.SessionID = d.sessID.String
	d.o.Project = d.project.String
	d.o.ContentHash = d.contentHash.String
	d.o.Structured = d.structured.String
	d.o.Rationale = d.rationale.String
	d.o.EmbeddingModel = d.embeddingModel.String
	d.o.LastAccessedAt = nullableTimePtr(*d.lastAcc)
	d.o.ValidUntil = nullableTimePtr(*d.validUntil)
	d.o.InvalidatedAt = nullableTimePtr(*d.invalidatedAt)
	d.o.ExpiresAt = nullableTimePtr(*d.expiresAt)
	d.o.LastExportedAt = nullableTimePtr(*d.lastExportedAt)
	d.o.Embedding = decodeVector(*d.embedding)
	_ = json.Unmarshal([]byte(*d.tagsJSON), &d.o.Tags)
	d.o.SourceKind = memory.SourceKind(*d.sourceKind)
	d.o.TrustTier = memory.TrustTier(*d.trustTier)
	_ = json.Unmarshal([]byte(*d.derivedFromJSON), &d.o.DerivedFrom)
	d.o.Pinned = *d.pinned != 0
	d.o.TopicKey = d.topicKey.String
	if err := json.Unmarshal([]byte(*d.evidenceJSON), &d.o.Evidence); err != nil {
		return fmt.Errorf("storage: decode evidence metadata: %w", err)
	}
	if len(d.o.Evidence) == 0 {
		d.o.Evidence = nil
	}
	return nil
}

func scanObs(row scanner) (*memory.Observation, error) {
	o := &memory.Observation{}
	d := newObsScanDest(o)
	if err := row.Scan(d.args()...); err != nil {
		return nil, err
	}
	if err := d.finalise(); err != nil {
		return nil, err
	}
	return o, nil
}

func scanObsWithScore(row scanner) (*memory.Observation, float64, string, error) {
	o := &memory.Observation{}
	d := newObsScanDest(o)
	var score float64
	var snippet string
	args := append(d.args(), &score, &snippet)
	if err := row.Scan(args...); err != nil {
		return nil, 0, "", fmt.Errorf("scan: %w", err)
	}
	if err := d.finalise(); err != nil {
		return nil, 0, "", err
	}
	return o, score, snippet, nil
}

func scanObsList(rows *sql.Rows) ([]memory.Observation, error) {
	var out []memory.Observation
	for rows.Next() {
		o, err := scanObs(rows)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// --- helpers ------------------------------------------------------------

// pinnedInt maps the pinned flag to its SQLite INTEGER form (the column is
// INTEGER CHECK (pinned IN (0, 1))). The conversion is explicit so the
// driver never sees a bool and the CHECK stays trivially honest.
func pinnedInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// placeholders renders n bind markers ("?,?,?") for an IN clause.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// prefixed returns the column list with a table alias prefixed to each name.
func prefixed(alias, columns string) string {
	var out []string
	for _, raw := range strings.Split(columns, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		out = append(out, alias+"."+name)
	}
	return strings.Join(out, ", ")
}

func coalesceSliceStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func collectTopTags(ctx context.Context, db *sql.DB) []memory.TagCount {
	rows, err := db.QueryContext(ctx, `SELECT tags FROM observations WHERE tags <> '[]'`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			continue
		}
		for _, t := range tags {
			counts[t]++
		}
	}

	out := make([]memory.TagCount, 0, len(counts))
	for tag, c := range counts {
		out = append(out, memory.TagCount{Tag: tag, Count: c})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func collectRecentSessions(ctx context.Context, db *sql.DB, limit int) []memory.RecentSession {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project, goal, started_at, ended_at
		   FROM sessions ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []memory.RecentSession
	for rows.Next() {
		var rs memory.RecentSession
		var project, goal sql.NullString
		var endedAt sql.NullTime
		if err := rows.Scan(&rs.ID, &project, &goal, &rs.StartedAt, &endedAt); err != nil {
			continue
		}
		rs.Project = project.String
		rs.Goal = goal.String
		rs.EndedAt = nullableTimePtr(endedAt)
		out = append(out, rs)
	}
	return out
}
