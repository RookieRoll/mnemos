package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// registerTools wires every Mnemos tool into the SDK server. Input
// schemas are inferred from struct tags; the SDK validates arguments
// before calling our handler.
func (s *Server) registerTools() {
	// Save / search / get / delete / link --------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_save",
		Description: "Call IMMEDIATELY when the user says \"save this\", \"remember\", \"note this\", " +
			"\"record\", \"keep this for next time\", \"for future sessions\", or shares an architectural " +
			"decision worth carrying forward (\"we just decided to use X over Y\", \"going with X\"). " +
			"Also call after any non-obvious decision or surprise the next session would benefit from " +
			"knowing. For corrections specifically use mnemos_correct (sharper schema); for project " +
			"rules use mnemos_convention. Deliberately curated: save things worth remembering later, " +
			"not routine task progress. Better to over-save a real signal than skip it.",
	}, s.handleSave)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_search",
		Description: "Call BEFORE any non-trivial code change, design decision, or refactor in this " +
			"project to surface prior decisions, conventions, and corrections. Skipping risks repeating " +
			"known mistakes already recorded. Hybrid BM25 + vector ranked search with recency/importance/" +
			"access multipliers. Default scope is valid-now (invalidated/expired hidden). " +
			"Cheap; call liberally with the user's request as the query.",
	}, s.handleSearch)

	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_get",
		Description: "Fetch a full observation by ID. Bumps the access counter.",
	}, s.handleGet)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_delete",
		Description: "Hard-delete a mistaken save. For facts that changed, use mnemos_link with " +
			"link_type=supersedes — preserves provenance and hides the stale fact from default search.",
	}, s.handleDelete)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_link",
		Description: "Link two observations (related, caused_by, supersedes, contradicts, refines). " +
			"'supersedes' auto-invalidates the target.",
	}, s.handleLink)

	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_get_many",
		Description: "Read selected observation details in first-requested order. Requires project; agent defaults to the save identity. Accepts 1-32 IDs, deduplicated. Live non-raw by default; explicit raw/stale/history options remain scoped. Encoded selected observation objects share a 32 KiB default byte budget (max 256 KiB); per-ID statuses expose unavailable and size-omitted items. Counts only returned details as reads, never successful use.",
	}, s.handleGetMany)
	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_neighborhood",
		Description: "Browse bounded chronological summaries around a visible observation in its session, project and agent scope. Requires project. Defaults to 3 before and after (0-10 each), live non-raw visibility, 32 KiB encoded selected-summary budget (max 256 KiB). Explicit raw/stale/history inspection stays scoped. Returns availability, window/size limits and UTF-8-safe summary truncation; does not increment detail access counters.",
	}, s.handleNeighborhood)

	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_evidence_check",
		Description: "Record the latest explicit caller-reported source check (matched, changed, unavailable or unverified) for an existing observation reference. A matched report requires a stored fingerprint baseline. Mnemos does not fetch sources or independently certify the claim. Replaces the prior marker/time only; never changes trust, validity, pinning or ranking and keeps no check history.",
	}, s.handleEvidenceCheck)

	// Sessions -----------------------------------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_session_start",
		Description: "Open a session. Returns session_id AND a pre-warmed context block composed " +
			"of conventions, recent sessions, matching skills, corrections, and hot files. Push, not pull.",
	}, s.handleSessionStart)
	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_checkpoint",
		Description: "Explicitly save or clear the exact current session's latest progress checkpoint. Requires continuity v1, exact session/workspace/owner, expected_revision (0 initially) and stable request_id. Save agent-authored task/state/summary, optional next_steps/blockers/references; visibility defaults to owner, workspace explicitly publishes it. Full replacement, no merge. Trust only applied/replayed receipts; retry unknown outcomes with the identical request. Does not save permanent knowledge or close the session.",
	}, s.handleCheckpoint)
	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_resume_context",
		Description: "Read bounded progress candidates or an exact source from the current continuity-bound workspace. Requires v1 and exact current session, never creates/claims/consumes work. No automatic selection without resume_intent and one fresh matching checkpoint; ambiguous, stale and changed-workspace sources need explicit choice and verification of current files/tests. References do not grant access to private memories.",
	}, s.handleResumeContext)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_session_end",
		Description: "Close a session with summary, status (ok|failed|blocked|abandoned), and " +
			"reflection. Accepted only for the invocation's current session, or a continuity-bound " +
			"session whose receipt workspace matches; any other id fails with session_not_owned. " +
			"Orphaned or batch closures belong to the local 'mnemos sessions close' command. " +
			"Failed sessions' observations get a ranking boost — agents learn from mistakes.",
	}, s.handleSessionEnd)

	// Context ------------------------------------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_context",
		Description: "Call when starting a substantial task to assemble a token-budgeted context block " +
			"(conventions + corrections + prior decisions matching the goal). Cheaper than reading the " +
			"raw store. Default mode: query-based search-and-pack. Mode='recovery' restores current " +
			"session state after a compaction — the 'oh shit' button.",
	}, s.handleContext)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_premortem",
		Description: "Call BEFORE executing a non-trivial plan — after deciding the approach, before " +
			"the first edit. Submit the plan text; returns how similar attempts failed: matching " +
			"corrections (tried/wrong/fix), past sessions with overlapping goals that ended " +
			"failed/blocked/abandoned, applicable skills (flagged if under rumination review), and " +
			"conventions the plan must respect. Catches known failure modes before they are repeated " +
			"instead of correcting them after. Cheap (one call, token-budgeted block); skipping it on " +
			"a plan that later fails a known way is the exact loop mnemos exists to break.",
	}, s.handlePremortem)

	// Provenance / promotion (Bet 2 phase 2) ----------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_promote",
		Description: "Promote an observation between trust tiers (raw → curated when " +
			"tool-output content is validated, curated → skill when a pattern is worth " +
			"reusing). Requires why_better: one sentence stating the concrete signal " +
			"that justifies the tier change — Popper's falsifiability guard; filler " +
			"reasons (<16 chars) are rejected.",
	}, s.handlePromote)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_pin",
		Description: "Pin or unpin an observation as core memory. Pinned memories are immune to " +
			"time decay (recency factor 1.0), exempt from dream-pass idle importance decay, " +
			"and guaranteed a Tier-0 slot at the top of every prewarm block. Pinning an observation " +
			"clears its expiry so it cannot be pruned by TTL; unpinning restores type-differentiated decay.",
	}, s.handlePin)

	// Agent supercharge --------------------------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_correct",
		Description: "Call IMMEDIATELY when the user signals a correction or shares a past failure. " +
			"Trigger phrases include: \"no\", \"don't\", \"that's wrong\", \"actually\", \"we tried X\", " +
			"\"we were wrong about\", \"going forward use\", \"don't do X — it caused Y\", \"X failed " +
			"because Y\", \"last time we did X and it broke\". Even mid-task corrections count — " +
			"capture before continuing the work. Record: tried + wrong_because + fix + trigger_context. " +
			"Auto-surfaced in session pre-warm when the session goal matches. This is the " +
			"anti-repeat-mistakes loop; every skipped call is a future repeat.",
	}, s.handleCorrect)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_convention",
		Description: "Call IMMEDIATELY when the user declares a project rule or you derive one from " +
			"repeated patterns. Trigger phrases: \"we always\", \"we never\", \"the convention here " +
			"is\", \"in this codebase we\", \"every X uses Y\", \"the rule is\", \"by convention\", " +
			"\"don't ever\". Even a quick aside (\"btw we always do X\") counts — capture it before " +
			"answering the rest of the prompt. Declares a project convention; auto-injected at every " +
			"session_start for the matching project. Declared once, applied forever — and the next " +
			"agent on this project starts knowing it.",
	}, s.handleConvention)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_touch",
		Description: "Record a file touch for the heat map. Frequently-touched files get priority in " +
			"session pre-warming so the agent focuses on what actually matters.",
	}, s.handleTouch)

	// Skills -------------------------------------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_skill_match",
		Description: "Find skills matching a task. Ranking factors in effectiveness (skills that worked before rise up).",
	}, s.handleSkillMatch)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_skill_save",
		Description: "Save or version a reusable procedure. Keyed by (agent_id, name); same name bumps " +
			"the version.",
	}, s.handleSkillSave)

	addProfileTool(s, &mcpsdk.Tool{
		Name: "mnemos_skill_score",
		Description: "Return a structured quality report for one skill: raw effectiveness, use_count, " +
			"success_count, Bayesian-shrunk effectiveness (so a 1/1 record doesn't outrank 9/10), a " +
			"recency factor decaying over 90 days, and a composite 0-1 score. Use this when ranking, " +
			"selecting, or reporting on skills measured rather than guessed quality.",
	}, s.handleSkillScore)

	// Rumination ---------------------------------------------------------
	// Guarded: only expose the ruminate_* surface when a rumination
	// service is wired. Makes rumination.enabled = false (in config) a
	// clean no-op instead of surfacing tools that immediately error.
	if s.cfg.Rumination != nil {
		addProfileTool(s, &mcpsdk.Tool{
			Name: "mnemos_ruminate_list",
			Description: "List pending rumination candidates (skills whose effectiveness has " +
				"fallen below the threshold, or other threshold breaches). Ordered severity-desc, " +
				"detected-at-desc. Each candidate is a hypothesis waiting for the hostile review " +
				"ritual — fetch the full review block with mnemos_ruminate_pack.",
		}, s.handleRuminateList)

		addProfileTool(s, &mcpsdk.Tool{
			Name: "mnemos_ruminate_pack",
			Description: "Fetch the review block for one rumination candidate: hypothesis verbatim, " +
				"disconfirming evidence, falsifiable restatement, and hostile-review prompts. Answer " +
				"the prompts before writing a revision — the structure enforces scientific-method " +
				"reasoning over the stored belief.",
		}, s.handleRuminatePack)

		addProfileTool(s, &mcpsdk.Tool{
			Name: "mnemos_ruminate_resolve",
			Description: "Close a rumination candidate by naming the revision that replaces the " +
				"flagged belief. Requires resolved_by (the ID of the new skill version or superseding " +
				"observation) AND why_better (one sentence stating a new prediction the revision " +
				"makes that the old version did not — Popper's falsifiability guard).",
		}, s.handleRuminateResolve)

		addProfileTool(s, &mcpsdk.Tool{
			Name: "mnemos_ruminate_dismiss",
			Description: "Close a rumination candidate as noise — the rule stands. Reason is " +
				"required; it prevents the next dream pass from re-raising the same flag without " +
				"context. Use when you've decided the disconfirming evidence was a one-off or the " +
				"rule's premise is still sound.",
		}, s.handleRuminateDismiss)
	}

	// Stats --------------------------------------------------------------
	addProfileTool(s, &mcpsdk.Tool{
		Name:        "mnemos_stats",
		Description: "System statistics: counts, top tags, recent sessions, embedding status, storage size.",
	}, s.handleStats)
}

func isCoreProfileTool(name string) bool {
	switch name {
	case "mnemos_session_start", "mnemos_session_end", "mnemos_search", "mnemos_get_many",
		"mnemos_save", "mnemos_correct", "mnemos_convention", "mnemos_checkpoint", "mnemos_resume_context":
		return true
	default:
		return false
	}
}

// addProfileTool registers one startup tool unless the fixed core profile
// excludes it. Unknown profiles fail closed; configuration validation rejects
// them before production server startup.
func addProfileTool[In, Out any](s *Server, tool *mcpsdk.Tool, handler mcpsdk.ToolHandlerFor[In, Out]) {
	switch s.cfg.Profile {
	case "core":
		if !isCoreProfileTool(tool.Name) {
			return
		}
	case "maintenance":
	default:
		return
	}
	mcpsdk.AddTool(s.sdk, tool, handler)
}

// GlobalProject is the project a caller names to save a memory that
// applies to every project. It is stored as no project.
const GlobalProject = "*"

// writeProject resolves the project a write lands in. An omitted project
// takes the server's DefaultProject: between April and September 2026, ten
// of the eleven project-less memories in one store were really about a
// single project, because the agent never passed one. They then surfaced
// in every other project and took 62% of prompt-hook injections.
func (s *Server) writeProject(project string) string {
	switch project {
	case "":
		return s.cfg.DefaultProject
	case GlobalProject:
		return ""
	}
	return project
}

// ---- mnemos_save -------------------------------------------------------

type saveArgs struct {
	Title            string             `json:"title" jsonschema:"short scannable label"`
	Content          string             `json:"content" jsonschema:"the memory — structure as what/why/where/learned"`
	Type             string             `json:"type" jsonschema:"decision|bugfix|pattern|preference|context|architecture|episodic|semantic|procedural|correction|convention"`
	Tags             []string           `json:"tags,omitempty"`
	Importance       int                `json:"importance,omitempty" jsonschema:"1..10, defaults to 5"`
	TTLDays          int                `json:"ttl_days,omitempty"`
	AgentID          string             `json:"agent_id,omitempty"`
	Project          string             `json:"project,omitempty" jsonschema:"defaults to the project the server runs in; pass \"*\" for a memory that applies to every project"`
	SessionID        string             `json:"session_id,omitempty"`
	ValidFrom        string             `json:"valid_from,omitempty" jsonschema:"RFC3339 fact-time lower bound"`
	ValidUntil       string             `json:"valid_until,omitempty" jsonschema:"RFC3339 fact-time upper bound"`
	Rationale        string             `json:"rationale,omitempty" jsonschema:"the WHY — surfaced in prewarm"`
	SourceKind       string             `json:"source_kind,omitempty" jsonschema:"user|tool|agent_inference|dream|import — defaults to user"`
	TrustTier        string             `json:"trust_tier,omitempty" jsonschema:"raw|curated|skill — only user-sourced saves keep this; any non-user source_kind is quarantined to raw automatically regardless of this value"`
	DerivedFrom      []string           `json:"derived_from,omitempty" jsonschema:"parent observation IDs this memory was derived from"`
	TopicKey         string             `json:"topic_key,omitempty" jsonschema:"optional exact case-sensitive topic; shared topic appends unless replaces_id is explicit"`
	ReplacesID       string             `json:"replaces_id,omitempty" jsonschema:"expected live same-scope version to replace atomically"`
	UnpinReplacement bool               `json:"unpin_replacement,omitempty" jsonschema:"explicitly allow unpinning when replacing a pinned version"`
	Evidence         []evidenceSaveArgs `json:"evidence,omitempty" jsonschema:"at most16 bounded source references; new saves start unverified; never fetches source bodies"`
	Pinned           bool               `json:"pinned,omitempty" jsonschema:"true to pin as core memory: immune to decay, Tier-0 prewarm slot, clears expiry"`
}

func (s *Server) handleSave(ctx context.Context, _ *mcpsdk.CallToolRequest, a saveArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.SaveInput{
		Title: a.Title, Content: a.Content, Type: memory.ObsType(a.Type),
		TopicKey: a.TopicKey, ReplacesID: a.ReplacesID, UnpinReplacement: a.UnpinReplacement, Evidence: toEvidence(a.Evidence),
		Tags: a.Tags, Importance: a.Importance, TTLDays: a.TTLDays,
		AgentID: a.AgentID, Project: s.writeProject(a.Project), SessionID: a.SessionID,
		Rationale:   a.Rationale,
		SourceKind:  memory.SourceKind(a.SourceKind),
		TrustTier:   memory.TrustTier(a.TrustTier),
		DerivedFrom: a.DerivedFrom,
		Pinned:      a.Pinned,
	}
	if t, err := parseTime(a.ValidFrom); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.ValidFrom = t
	}
	if t, err := parseTime(a.ValidUntil); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.ValidUntil = t
	}
	res, err := s.cfg.Memory.Save(ctx, in)
	if err != nil {
		return evolutionError(err)
	}
	return saveResultJSON(res, map[string]any{
		"id":           res.Observation.ID,
		"title":        res.Observation.Title,
		"type":         string(res.Observation.Type),
		"source_kind":  string(res.Observation.SourceKind),
		"trust_tier":   string(res.Observation.TrustTier),
		"derived_from": res.Observation.DerivedFrom,
		"pinned":       res.Observation.Pinned,
		"created_at":   res.Observation.CreatedAt,
		"deduped":      res.Deduped,
		"topic_key":    res.Observation.TopicKey,
		"evidence":     res.Observation.Evidence,
	})
}

// ---- mnemos_search -----------------------------------------------------

type searchArgs struct {
	TopicKey      string   `json:"topic_key,omitempty" jsonschema:"optional exact topic filter; does not change ranking"`
	Query         string   `json:"query"`
	Type          string   `json:"type,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	MinImportance int      `json:"min_importance,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	Project       string   `json:"project,omitempty"`
	IncludeStale  bool     `json:"include_stale,omitempty"`
	IncludeRaw    bool     `json:"include_raw,omitempty" jsonschema:"true to include quarantined raw-tier observations (tool output, agent inference)"`
	AsOf          string   `json:"as_of,omitempty"`
}

func (s *Server) handleSearch(ctx context.Context, _ *mcpsdk.CallToolRequest, a searchArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.SearchInput{
		Query: a.Query, Type: memory.ObsType(a.Type), Tags: a.Tags, TopicKey: a.TopicKey,
		MinImportance: a.MinImportance, Limit: a.Limit,
		AgentID: a.AgentID, Project: a.Project,
		IncludeStale: a.IncludeStale,
		IncludeRaw:   a.IncludeRaw,
	}
	if t, err := parseTime(a.AsOf); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.AsOf = *t
	}
	results, mode, err := s.cfg.Memory.SearchWithMode(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	hits := make([]map[string]any, 0, len(results))
	for _, r := range results {
		hits = append(hits, map[string]any{
			"id":                   r.Observation.ID,
			"title":                r.Observation.Title,
			"type":                 string(r.Observation.Type),
			"tags":                 r.Observation.Tags,
			"importance":           r.Observation.Importance,
			"score":                r.Score,
			"relevance":            r.Relevance,
			"policy_factor":        r.PolicyFactor,
			"graph_factor":         r.GraphFactor,
			"contradiction_factor": r.ContradictionFactor,
			"expanded_from":        r.ExpandedFrom,
			"contradicted_by":      r.ContradictedBy,
			"pinned":               r.Observation.Pinned,
			"snippet":              r.Snippet,
			"topic_key":            r.Observation.TopicKey,
			"evidence":             r.Observation.Evidence,
			"created_at":           r.Observation.CreatedAt,
			"source_kind":          string(r.Observation.SourceKind),
			"trust_tier":           string(r.Observation.TrustTier),
			"derived_from":         r.Observation.DerivedFrom,
		})
	}
	return jsonResult(map[string]any{"results": hits, "retrieval_mode": string(mode)})
}

// ---- mnemos_get / delete / link ---------------------------------------

type idArgs struct {
	ID string `json:"id"`
}

func (s *Server) handleGet(ctx context.Context, _ *mcpsdk.CallToolRequest, a idArgs) (*mcpsdk.CallToolResult, any, error) {
	o, err := s.cfg.Memory.Get(ctx, a.ID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return nil, nil, fmt.Errorf("observation not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(o)
}

func (s *Server) handleDelete(ctx context.Context, _ *mcpsdk.CallToolRequest, a idArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Memory.Delete(ctx, a.ID); err != nil {
		return nil, nil, err
	}
	return textResult("deleted " + a.ID)
}

type linkArgs struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	LinkType string `json:"link_type" jsonschema:"related|caused_by|supersedes|contradicts|refines"`
}

func (s *Server) handleLink(ctx context.Context, _ *mcpsdk.CallToolRequest, a linkArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Memory.Link(ctx, a.SourceID, a.TargetID, memory.LinkType(a.LinkType)); err != nil {
		return evolutionError(err)
	}
	return textResult(fmt.Sprintf("linked %s -[%s]-> %s", a.SourceID, a.LinkType, a.TargetID))
}

// ---- sessions ----------------------------------------------------------

type sessionStartArgs struct {
	AgentID    string                `json:"agent_id,omitempty"`
	Project    string                `json:"project,omitempty" jsonschema:"enables convention auto-injection; defaults to the project the server runs in"`
	Goal       string                `json:"goal,omitempty" jsonschema:"improves skill and correction matching"`
	Continuity *continuity.OpenInput `json:"continuity,omitempty" jsonschema:"optional explicit v1 workspace/start-key protocol; agent/project/goal are resolved from the outer fields when supplied"`
}

func (s *Server) handleSessionStart(ctx context.Context, req *mcpsdk.CallToolRequest, a sessionStartArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.Continuity != nil {
		if s.cfg.Flow == nil || !s.cfg.Flow.SupportsContinuity() {
			return continuityError(continuity.ErrUnsupported)
		}
		if req != nil && req.Params != nil {
			if err := continuity.DecodeJSON(req.Params.Arguments, &a); err != nil {
				return continuityError(err)
			}
		}
		in := *a.Continuity
		if a.AgentID != "" {
			in.AgentID = a.AgentID
		}
		if a.Project != "" {
			in.Project = a.Project
		}
		if a.Goal != "" {
			in.Goal = a.Goal
		}
		out, err := s.cfg.Flow.Start(ctx, continuityflow.TaskSessionInput{
			AgentID: a.AgentID, Project: a.Project, Goal: a.Goal, Continuity: &in,
		})
		if err != nil {
			return continuityError(err)
		}
		if out.Receipt.Continuity == nil {
			return continuityError(continuity.ErrUnavailable)
		}
		return continuityResult(&continuityflow.OpenResult{
			OpenResult: *out.Receipt.Continuity, ContextStatus: out.ContextStatus,
			Prewarm: out.Prewarm, ContinuityRefs: out.ContinuityRefs,
		})
	}
	project := s.writeProject(a.Project)
	flow := s.cfg.Flow
	if flow == nil {
		return nil, nil, fmt.Errorf("mcp: task-session flow unavailable")
	}
	out, err := flow.Start(ctx, continuityflow.TaskSessionInput{
		AgentID: a.AgentID, Project: project, Goal: a.Goal,
	})
	if err != nil {
		return nil, nil, err
	}
	sess := out.Receipt.LegacySession
	if sess == nil {
		return nil, nil, fmt.Errorf("mcp: legacy session receipt unavailable")
	}
	if req != nil {
		s.bindCurrentSession(req.Session, sess.ID)
	}
	legacyResponse := map[string]any{"session_id": sess.ID, "started_at": sess.StartedAt}
	if block := out.LegacyBlock; block != nil && block.Text != "" {
		legacyResponse["prewarm"] = map[string]any{
			"text": block.Text, "token_estimate": block.TokenEstimate,
			"section_count": len(block.Sections), "safety_risk": block.SafetyReport.MaxRisk.String(),
		}
	}
	return jsonResult(legacyResponse)
}

type sessionEndArgs struct {
	SessionID     string   `json:"session_id"`
	HostSessionID string   `json:"host_session_id,omitempty" jsonschema:"for hook-started legacy sessions, copy mnemos_host_session_id from startup context"`
	Summary       string   `json:"summary"`
	Reflection    string   `json:"reflection,omitempty" jsonschema:"transferable lessons"`
	Status        string   `json:"status,omitempty" jsonschema:"ok|failed|blocked|abandoned"`
	OutcomeTags   []string `json:"outcome_tags,omitempty"`
}

func (s *Server) handleSessionEnd(ctx context.Context, req *mcpsdk.CallToolRequest, a sessionEndArgs) (*mcpsdk.CallToolResult, any, error) {
	status := session.Status(a.Status)
	if status == "" {
		status = session.StatusOK
	}
	closeInput := session.CloseInput{
		ID: a.SessionID, Summary: a.Summary, Reflection: a.Reflection,
		Status: status, OutcomeTags: a.OutcomeTags,
	}
	if s.cfg.Sessions == nil {
		return nil, nil, fmt.Errorf("mcp: session service unavailable")
	}
	inv := s.sessionEndInvocation(ctx)
	inv.HostSessionID = a.HostSessionID
	if req != nil {
		inv.CurrentSessionID = s.currentSession(req.Session)
	}
	err := s.cfg.Sessions.EndOwned(ctx, closeInput, inv)
	if err != nil {
		var rejected *session.NotOwnedError
		if errors.As(err, &rejected) {
			return sessionNotOwnedResult(rejected)
		}
		return nil, nil, err
	}
	return textResult("session " + a.SessionID + " closed (" + string(status) + ")")
}

// sessionEndInvocation assembles workspace receipt authorization. The handler
// separately adds the exact session opened on its MCP connection; project
// recency is never used as a substitute for that identity.
func (s *Server) sessionEndInvocation(ctx context.Context) session.EndInvocation {
	inv := session.EndInvocation{Project: s.cfg.DefaultProject}
	inv.WorkspaceRoot = invocationWorkspaceRoot(ctx)
	if inv.WorkspaceRoot == "" || s.cfg.Flow == nil || !s.cfg.Flow.SupportsContinuity() {
		return inv
	}
	inv.Receipts = continuityReceiptAuthorizer{flow: s.cfg.Flow, workspaceRoot: inv.WorkspaceRoot}
	return inv
}

// invocationWorkspaceRoot resolves the directory the server was started in as
// a continuity workspace, reusing continuity's canonicalization instead of a
// second path convention. An unresolvable workspace returns "", which disables
// receipt authorization; the invocation's bound session stays closable.
func invocationWorkspaceRoot(ctx context.Context) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	resolver := continuity.LocalResolver{}
	if w, err := resolver.Resolve(ctx, cwd, ""); err == nil {
		return w.Root
	}
	// A non-Git server directory is a valid workspace only as an explicit root.
	if w, err := resolver.Resolve(ctx, cwd, cwd); err == nil {
		return w.Root
	}
	return ""
}

// continuityReceiptAuthorizer is the named receipt seam's current
// implementation: it verifies a continuity receipt through continuity's own
// read path instead of reaching into session_continuity_bindings. That path
// accepts a session only when its recorded binding matches the (owner agent,
// workspace key) pair, so success means the receipt's workspace is the
// invocation workspace. The agent used for the lookup is the target's own, not
// the caller's, which keeps the decision workspace-only. A dedicated receipt
// accessor can replace this type without touching session.EndOwned.
type continuityReceiptAuthorizer struct {
	flow          *continuityflow.Service
	workspaceRoot string
}

// AuthorizeSessionEnd reports whether target carries a continuity receipt
// bound to the authorizer's workspace. A session bound elsewhere is a plain
// refusal; only operational failures surface as errors.
func (a continuityReceiptAuthorizer) AuthorizeSessionEnd(ctx context.Context, target *session.Session) (bool, error) {
	_, err := a.flow.Context(ctx, continuity.ContextInput{
		ProtocolVersion: continuity.ProtocolVersion,
		AgentID:         target.AgentID,
		CWD:             a.workspaceRoot,
		WorkspaceRoot:   a.workspaceRoot,
		SessionID:       target.ID,
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, continuity.ErrUnavailable):
		return false, nil
	default:
		return false, err
	}
}

// sessionNotOwnedResult maps the guard's rejection onto the stable
// session_not_owned wire code. The text names both supported routes: close the
// current session, or run the local maintenance channel against the id.
func sessionNotOwnedResult(rejected *session.NotOwnedError) (*mcpsdk.CallToolResult, any, error) {
	body := map[string]any{
		"code": "session_not_owned",
		"error": fmt.Sprintf(
			"session %s is not owned by this invocation: close the current session, or run `mnemos sessions close %s` locally",
			rejected.SessionID, rejected.SessionID),
		"session_id": rejected.SessionID,
	}
	if rejected.WorkspaceRoot != "" {
		body["workspace"] = rejected.WorkspaceRoot
	}
	res, _, err := jsonResult(body)
	if err != nil {
		return nil, nil, err
	}
	res.IsError = true
	return res, nil, nil
}

// ---- mnemos_context ----------------------------------------------------

type contextArgs struct {
	Query     string `json:"query,omitempty"`
	Mode      string `json:"mode,omitempty" jsonschema:"empty for query mode, 'recovery' after compaction"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Project   string `json:"project,omitempty"`
	SessionID string `json:"session_id,omitempty" jsonschema:"required for recovery mode"`
	Goal      string `json:"goal,omitempty"`
}

func (s *Server) handleContext(ctx context.Context, _ *mcpsdk.CallToolRequest, a contextArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.Mode == "recovery" {
		if a.SessionID == "" {
			return nil, nil, fmt.Errorf("recovery mode requires session_id")
		}
		if s.cfg.Flow == nil || s.cfg.Prewarm == nil {
			return nil, nil, fmt.Errorf("recovery unavailable: prewarm service not wired")
		}
		block, err := s.cfg.Flow.Compose(ctx, prewarm.Request{
			Mode:      prewarm.ModeCompactionRecovery,
			AgentID:   a.AgentID,
			Project:   a.Project,
			SessionID: a.SessionID,
			Goal:      a.Goal,
			MaxTokens: a.MaxTokens,
		})
		if err != nil {
			return nil, nil, err
		}
		if block == nil {
			return nil, nil, fmt.Errorf("recovery unavailable: prewarm service not wired")
		}
		return jsonResult(map[string]any{
			"mode":           "recovery",
			"text":           block.Text,
			"token_estimate": block.TokenEstimate,
			"section_count":  len(block.Sections),
			"safety_risk":    block.SafetyReport.MaxRisk.String(),
		})
	}
	if a.Query == "" {
		return nil, nil, fmt.Errorf("query is required when mode != 'recovery'")
	}
	block, err := s.cfg.Memory.Context(ctx, memory.ContextInput{
		Query: a.Query, MaxTokens: a.MaxTokens,
		AgentID: a.AgentID, Project: a.Project,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"text":           block.Text,
		"token_estimate": block.TokenEstimate,
		"observations":   summariseObs(block.Observations),
	})
}

// ---- mnemos_premortem ---------------------------------------------------

type premortemArgs struct {
	Plan      string `json:"plan" jsonschema:"the intended approach in a few sentences — what you are about to do and how"`
	AgentID   string `json:"agent_id,omitempty"`
	Project   string `json:"project,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"budget for the returned block; default 800"`
}

func (s *Server) handlePremortem(ctx context.Context, _ *mcpsdk.CallToolRequest, a premortemArgs) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(a.Plan) == "" {
		return nil, nil, fmt.Errorf("plan is required")
	}
	if s.cfg.Flow == nil || s.cfg.Prewarm == nil {
		return nil, nil, fmt.Errorf("premortem unavailable: prewarm service not wired")
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		// Premortems are explicit asks, so the default budget is roomier
		// than the ambient session-start block.
		maxTokens = 800
	}
	block, err := s.cfg.Flow.Compose(ctx, prewarm.Request{
		Mode:      prewarm.ModePremortem,
		AgentID:   a.AgentID,
		Project:   a.Project,
		SessionID: a.SessionID,
		Goal:      a.Plan,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, nil, err
	}
	if block == nil {
		return nil, nil, fmt.Errorf("premortem unavailable: prewarm service not wired")
	}
	if block.Text == "" {
		return jsonResult(map[string]any{
			"text":    "",
			"verdict": "no recorded failures, corrections, or conventions match this plan — proceed, and record what you learn",
		})
	}
	return jsonResult(map[string]any{
		"text":           block.Text,
		"token_estimate": block.TokenEstimate,
		"section_count":  len(block.Sections),
		"safety_risk":    block.SafetyReport.MaxRisk.String(),
	})
}

// ---- mnemos_pin --------------------------------------------------------

type pinArgs struct {
	ID     string `json:"id" jsonschema:"observation ID to pin or unpin"`
	Pinned bool   `json:"pinned" jsonschema:"true to pin as core memory, false to unpin"`
}

func (s *Server) handlePin(ctx context.Context, _ *mcpsdk.CallToolRequest, a pinArgs) (*mcpsdk.CallToolResult, any, error) {
	o, err := s.cfg.Memory.Pin(ctx, a.ID, a.Pinned)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return nil, nil, fmt.Errorf("observation not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":         o.ID,
		"title":      o.Title,
		"pinned":     o.Pinned,
		"expires_at": o.ExpiresAt,
	})
}

// ---- mnemos_promote ----------------------------------------------------

type promoteArgs struct {
	ID        string `json:"id"`
	ToTier    string `json:"to_tier" jsonschema:"raw|curated|skill"`
	WhyBetter string `json:"why_better" jsonschema:"one sentence: what concrete signal justifies the tier change (min 16 chars)"`
}

func (s *Server) handlePromote(ctx context.Context, _ *mcpsdk.CallToolRequest, a promoteArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Memory.Promote(ctx, memory.PromoteInput{
		ID: a.ID, ToTier: memory.TrustTier(a.ToTier), WhyBetter: a.WhyBetter,
	}); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return nil, nil, fmt.Errorf("observation not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":         a.ID,
		"to_tier":    a.ToTier,
		"why_better": a.WhyBetter,
	})
}

// ---- mnemos_correct ----------------------------------------------------

type correctArgs struct {
	Title          string   `json:"title"`
	Tried          string   `json:"tried"`
	WrongBecause   string   `json:"wrong_because"`
	Fix            string   `json:"fix"`
	TriggerContext string   `json:"trigger_context,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	Project        string   `json:"project,omitempty" jsonschema:"defaults to the project the server runs in; pass \"*\" for a memory that applies to every project"`
	SessionID      string   `json:"session_id,omitempty"`
	Importance     int      `json:"importance,omitempty" jsonschema:"defaults to 8 — corrections matter"`
}

func (s *Server) handleCorrect(ctx context.Context, _ *mcpsdk.CallToolRequest, a correctArgs) (*mcpsdk.CallToolResult, any, error) {
	importance := a.Importance
	if importance == 0 {
		importance = 8
	}
	content := fmt.Sprintf("**Tried:** %s\n\n**Wrong because:** %s\n\n**Fix:** %s",
		a.Tried, a.WrongBecause, a.Fix)
	structuredJSON := marshalMap(map[string]string{
		"tried": a.Tried, "wrong_because": a.WrongBecause,
		"fix": a.Fix, "trigger_context": a.TriggerContext,
	})
	res, err := s.cfg.Memory.Save(ctx, memory.SaveInput{
		Title: a.Title, Content: content, Type: memory.TypeCorrection,
		Tags: a.Tags, Importance: importance,
		AgentID: a.AgentID, Project: s.writeProject(a.Project), SessionID: a.SessionID,
		Structured: structuredJSON,
	})
	if err != nil {
		return nil, nil, err
	}
	return saveResultJSON(res, map[string]any{"id": res.Observation.ID, "deduped": res.Deduped})
}

// ---- mnemos_convention -------------------------------------------------

type conventionArgs struct {
	Title     string   `json:"title"`
	Rule      string   `json:"rule"`
	Rationale string   `json:"rationale,omitempty" jsonschema:"the WHY — surfaced in pre-warm"`
	Example   string   `json:"example,omitempty"`
	Project   string   `json:"project,omitempty" jsonschema:"defaults to the project the server runs in; pass \"*\" for a memory that applies to every project"`
	Tags      []string `json:"tags,omitempty"`
	AgentID   string   `json:"agent_id,omitempty"`
}

func (s *Server) handleConvention(ctx context.Context, _ *mcpsdk.CallToolRequest, a conventionArgs) (*mcpsdk.CallToolResult, any, error) {
	content := a.Rule
	if a.Example != "" {
		content += "\n\nExample:\n" + a.Example
	}
	res, err := s.cfg.Memory.Save(ctx, memory.SaveInput{
		Title: a.Title, Content: content, Type: memory.TypeConvention,
		Tags: a.Tags, Project: s.writeProject(a.Project), AgentID: a.AgentID,
		Importance: 8, Rationale: a.Rationale,
	})
	if err != nil {
		return nil, nil, err
	}
	return saveResultJSON(res, map[string]any{"id": res.Observation.ID, "deduped": res.Deduped})
}

// saveResultJSON keeps near-duplicate feedback visible on every write tool.
// The caller already has the submitted body; returning the match identity and
// reason is enough to distinguish a fold from an exact repeated save.
func saveResultJSON(res *memory.SaveResult, out map[string]any) (*mcpsdk.CallToolResult, any, error) {
	if res.NearDupHint != "" {
		out["near_dup_hint"] = res.NearDupHint
	}
	if res.NearDup != nil {
		out["near_dup"] = map[string]any{"survivor_id": res.NearDup.SurvivorID, "reason": res.NearDup.Reason}
	}
	return jsonResult(out)
}

// ---- mnemos_touch ------------------------------------------------------

type touchArgs struct {
	Path      string `json:"path"`
	Project   string `json:"project,omitempty" jsonschema:"defaults to the project the server runs in"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (s *Server) handleTouch(ctx context.Context, _ *mcpsdk.CallToolRequest, a touchArgs) (*mcpsdk.CallToolResult, any, error) {
	if s.cfg.Touches == nil {
		return nil, nil, fmt.Errorf("touch store not wired")
	}
	if err := s.cfg.Touches.Record(ctx, memory.TouchInput{
		Project: s.writeProject(a.Project), AgentID: a.AgentID, Path: a.Path,
		SessionID: a.SessionID, Note: a.Note,
	}); err != nil {
		return nil, nil, err
	}
	return textResult("touched " + a.Path)
}

// ---- skills ------------------------------------------------------------

type skillMatchArgs struct {
	Query   string   `json:"query"`
	Tags    []string `json:"tags,omitempty"`
	AgentID string   `json:"agent_id,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

func (s *Server) handleSkillMatch(ctx context.Context, _ *mcpsdk.CallToolRequest, a skillMatchArgs) (*mcpsdk.CallToolResult, any, error) {
	matches, err := s.cfg.Skills.Match(ctx, skills.MatchInput{
		Query: a.Query, Tags: a.Tags, AgentID: a.AgentID, Limit: a.Limit,
	})
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		out = append(out, map[string]any{
			"id": m.Skill.ID, "name": m.Skill.Name,
			"description": m.Skill.Description, "procedure": m.Skill.Procedure,
			"pitfalls": m.Skill.Pitfalls, "tags": m.Skill.Tags,
			"use_count": m.Skill.UseCount, "effectiveness": m.Skill.Effectiveness,
			"version": m.Skill.Version, "score": m.Score,
		})
	}
	return jsonResult(map[string]any{"matches": out})
}

type skillSaveArgs struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Procedure      string   `json:"procedure" jsonschema:"numbered steps in markdown"`
	Pitfalls       string   `json:"pitfalls,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SourceSessions []string `json:"source_sessions,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
}

func (s *Server) handleSkillSave(ctx context.Context, _ *mcpsdk.CallToolRequest, a skillSaveArgs) (*mcpsdk.CallToolResult, any, error) {
	sk, err := s.cfg.Skills.Save(ctx, skills.SaveInput{
		AgentID: a.AgentID, Name: a.Name, Description: a.Description,
		Procedure: a.Procedure, Pitfalls: a.Pitfalls, Tags: a.Tags,
		SourceSessions: a.SourceSessions,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{"id": sk.ID, "name": sk.Name, "version": sk.Version})
}

type skillScoreArgs struct {
	ID string `json:"id" jsonschema:"the skill ID returned by skill_save or skill_match"`
}

func (s *Server) handleSkillScore(ctx context.Context, _ *mcpsdk.CallToolRequest, a skillScoreArgs) (*mcpsdk.CallToolResult, any, error) {
	rep, err := s.cfg.Skills.Score(ctx, a.ID)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"skill_id":               rep.SkillID,
		"effectiveness":          rep.Effectiveness,
		"use_count":              rep.UseCount,
		"success_count":          rep.SuccessCount,
		"adjusted_effectiveness": rep.AdjustedEffectiveness,
		"recency":                rep.Recency,
		"score":                  rep.Score,
		"updated_at":             rep.UpdatedAt,
	})
}

// ---- stats -------------------------------------------------------------

// statsArgs is empty — stats takes no parameters.
type statsArgs struct{}

// handleStats renders the typed statistics value through statsView, the same
// formatter the mnemos://stats resource uses: one derivation, two surfaces.
func (s *Server) handleStats(ctx context.Context, _ *mcpsdk.CallToolRequest, _ statsArgs) (*mcpsdk.CallToolResult, any, error) {
	st, err := s.cfg.Memory.Stats(ctx)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(s.statsView(ctx, st))
}

// ---- rumination --------------------------------------------------------

type ruminateListArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"max candidates to return; 0 = all"`
}

func (s *Server) handleRuminateList(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateListArgs) (*mcpsdk.CallToolResult, any, error) {
	list, err := s.cfg.Rumination.Pending(ctx, a.Limit)
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id":          c.ID,
			"monitor":     c.MonitorName,
			"severity":    c.Severity.String(),
			"reason":      c.Reason,
			"target_kind": string(c.TargetKind),
			"target_id":   c.TargetID,
			"detected_at": c.DetectedAt,
			"evidence_n":  len(c.Evidence),
		})
	}
	counts, _ := s.cfg.Rumination.Counts(ctx)
	return jsonResult(map[string]any{
		"candidates": out,
		"counts": map[string]any{
			"pending":   counts.Pending,
			"resolved":  counts.Resolved,
			"dismissed": counts.Dismissed,
		},
	})
}

type ruminatePackArgs struct {
	ID string `json:"id"`
}

func (s *Server) handleRuminatePack(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminatePackArgs) (*mcpsdk.CallToolResult, any, error) {
	if a.ID == "" {
		return nil, nil, fmt.Errorf("rumination: id required")
	}
	block, err := s.cfg.Rumination.PackByID(ctx, a.ID)
	if err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"candidate_id":   block.CandidateID,
		"target_kind":    string(block.Target.Kind),
		"target_id":      block.Target.ID,
		"target_name":    block.Target.Name,
		"text":           block.Text,
		"token_estimate": block.TokenEstimate,
	})
}

type ruminateResolveArgs struct {
	ID         string `json:"id"`
	ResolvedBy string `json:"resolved_by" jsonschema:"ID of the new skill version or superseding observation"`
	// WhyBetter is the Popper-grade guard: name one prediction the revision
	// makes that the old version did not. The schema enforces the field;
	// the handler enforces non-empty and a minimum length.
	WhyBetter string `json:"why_better" jsonschema:"one sentence — a concrete new prediction the revised version makes that the old one did not. Cosmetic rewording is rejected."`
}

func (s *Server) handleRuminateResolve(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateResolveArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Rumination.ResolveDecision(ctx, a.ID, a.ResolvedBy, a.WhyBetter); err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":          a.ID,
		"status":      string(rumination.StatusResolved),
		"resolved_by": a.ResolvedBy,
		"why_better":  a.WhyBetter,
	})
}

type ruminateDismissArgs struct {
	ID     string `json:"id"`
	Reason string `json:"reason" jsonschema:"one-line justification — preserved so a later pass does not re-raise the same flag without context"`
}

func (s *Server) handleRuminateDismiss(ctx context.Context, _ *mcpsdk.CallToolRequest, a ruminateDismissArgs) (*mcpsdk.CallToolResult, any, error) {
	if err := s.cfg.Rumination.DismissDecision(ctx, a.ID, a.Reason); err != nil {
		if errors.Is(err, rumination.ErrNotFound) {
			return nil, nil, fmt.Errorf("rumination candidate not found: %s", a.ID)
		}
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"id":     a.ID,
		"status": string(rumination.StatusDismissed),
		"reason": a.Reason,
	})
}

// ---- helpers -----------------------------------------------------------

func summariseObs(obs []memory.Observation) []map[string]any {
	out := make([]map[string]any, 0, len(obs))
	for _, o := range obs {
		out = append(out, map[string]any{
			"id": o.ID, "title": o.Title,
			"type": string(o.Type), "importance": o.Importance,
		})
	}
	return out
}

func parseTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", s, err)
	}
	return &t, nil
}

func marshalMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
