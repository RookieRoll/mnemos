package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
)

type getManyArgs struct {
	IDs          []string `json:"ids" jsonschema:"1-32 observation IDs; first occurrences determine result order"`
	Project      string   `json:"project" jsonschema:"required hard project scope"`
	AgentID      string   `json:"agent_id,omitempty"`
	IncludeRaw   bool     `json:"include_raw,omitempty"`
	IncludeStale bool     `json:"include_stale,omitempty"`
	AsOf         string   `json:"as_of,omitempty" jsonschema:"RFC3339 historical validity time; not a complete database snapshot"`
	MaxBytes     int      `json:"max_bytes,omitempty" jsonschema:"encoded selected observation JSON bytes, excluding status envelope; default 32768, maximum 262144"`
}

func (s *Server) handleGetMany(ctx context.Context, _ *mcpsdk.CallToolRequest, a getManyArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.GetManyInput{IDs: a.IDs, Project: a.Project, AgentID: a.AgentID, IncludeRaw: a.IncludeRaw, IncludeStale: a.IncludeStale, MaxBytes: a.MaxBytes}
	if t, err := parseTime(a.AsOf); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.AsOf = *t
	}
	out, err := s.cfg.Memory.GetMany(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(out)
}

type neighborhoodArgs struct {
	ID           string `json:"id"`
	Project      string `json:"project" jsonschema:"required hard project scope"`
	AgentID      string `json:"agent_id,omitempty"`
	Before       *int   `json:"before,omitempty" jsonschema:"visible observations before anchor; default 3, range 0-10"`
	After        *int   `json:"after,omitempty" jsonschema:"visible observations after anchor; default 3, range 0-10"`
	IncludeRaw   bool   `json:"include_raw,omitempty"`
	IncludeStale bool   `json:"include_stale,omitempty"`
	AsOf         string `json:"as_of,omitempty" jsonschema:"RFC3339 historical validity time"`
	MaxBytes     int    `json:"max_bytes,omitempty" jsonschema:"encoded selected summary JSON bytes, excluding status envelope; default 32768, maximum 262144"`
}

func (s *Server) handleNeighborhood(ctx context.Context, _ *mcpsdk.CallToolRequest, a neighborhoodArgs) (*mcpsdk.CallToolResult, any, error) {
	in := memory.NeighborhoodInput{ID: a.ID, Project: a.Project, AgentID: a.AgentID, Before: a.Before, After: a.After, IncludeRaw: a.IncludeRaw, IncludeStale: a.IncludeStale, MaxBytes: a.MaxBytes}
	if t, err := parseTime(a.AsOf); err != nil {
		return nil, nil, err
	} else if t != nil {
		in.AsOf = *t
	}
	out, err := s.cfg.Memory.Neighborhood(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(out)
}
