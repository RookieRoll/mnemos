// Package mcp wraps the official Model Context Protocol Go SDK
// (github.com/modelcontextprotocol/go-sdk) with Mnemos-specific tool and
// resource registrations. The heavy lifting — JSON-RPC framing, schema
// inference, transport plumbing — lives in the SDK; this package owns
// the mapping between MCP messages and Mnemos services.
package mcp

import (
	"context"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Server is the Mnemos MCP server. It is a thin wrapper around the SDK
// server, holding the service dependencies our tool handlers close over.
type Server struct {
	sdk *mcpsdk.Server
	cfg Config
}

// Config bundles the dependencies a Server needs. Every field except
// Name, Version, Logger, and Rumination is required.
type Config struct {
	Name    string
	Version string
	// Profile is fixed before construction and selects the startup tool set.
	Profile    string
	Memory     *memory.Service
	Sessions   *session.Service
	Skills     *skills.Service
	Touches    memory.TouchStore
	Prewarm    *prewarm.Service
	Rumination *rumination.Service // optional; enables the mnemos_ruminate_* tools
	Logger     *slog.Logger
	// DefaultProject is the project a write lands in when the tool call
	// names none. serve sets it to the basename of the directory it was
	// started in, which Claude Code and Codex both set to the project
	// root. Empty keeps such writes project-less.
	DefaultProject string
	// Continuity is optional; its explicit v1 operations never use legacy inference.
	Continuity *continuityflow.Service
	// Flow is the unified task-session/context entry point. Continuity is kept
	// as a source-compatible alias for existing server constructors.
	Flow *continuityflow.Service
}

// NewServer constructs an MCP server with all Mnemos tools and resources
// registered. The server is ready to call Run/ServeStdio; no further
// registration is required.
func NewServer(cfg Config) *Server {
	if cfg.Profile == "" {
		cfg.Profile = "core"
	}
	if cfg.Flow == nil {
		cfg.Flow = cfg.Continuity
	}
	if cfg.Flow == nil && (cfg.Sessions != nil || cfg.Prewarm != nil) {
		cfg.Flow = continuityflow.New(continuityflow.Config{LegacySessions: cfg.Sessions, Composer: cfg.Prewarm})
	}
	if cfg.Continuity == nil && cfg.Flow != nil && cfg.Flow.SupportsContinuity() {
		cfg.Continuity = cfg.Flow
	}
	if cfg.Name == "" {
		cfg.Name = "mnemos"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	sdk := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    cfg.Name,
		Version: cfg.Version,
	}, nil)

	s := &Server{sdk: sdk, cfg: cfg}
	sdk.AddReceivingMiddleware(continuityValidationMiddleware)
	s.registerTools()
	s.registerResources()
	return s
}

// ServeStdio runs the server over the stdio transport until ctx is
// cancelled or the peer disconnects.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.sdk.Run(ctx, &mcpsdk.StdioTransport{})
}

// SDK returns the underlying SDK server. Useful for tests that need to
// wire an in-memory transport pair.
func (s *Server) SDK() *mcpsdk.Server { return s.sdk }
