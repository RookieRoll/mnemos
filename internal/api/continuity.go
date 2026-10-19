package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

func (s *Server) registerContinuity(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/continuity/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeContinuity(w, http.StatusOK, map[string]any{"protocol_version": 1, "supported": s.continuity != nil})
	})
	mux.HandleFunc("POST /v1/continuity/open", continuityHandler(s, func(ctx context.Context, in continuity.OpenInput) (any, error) { return s.continuity.Open(ctx, in) }))
	mux.HandleFunc("POST /v1/continuity/checkpoint", continuityHandler(s, func(ctx context.Context, in continuity.CheckpointInput) (any, error) {
		return s.continuity.Checkpoint(ctx, in)
	}))
	mux.HandleFunc("POST /v1/continuity/context", continuityHandler(s, func(ctx context.Context, in continuity.ContextInput) (any, error) {
		return s.continuity.Context(ctx, in)
	}))
}

func continuityHandler[In any](s *Server, call func(context.Context, In) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.continuity == nil {
			writeContinuityError(w, continuity.ErrUnsupported)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, continuity.MaxRequestBytes+1))
		if err != nil {
			writeContinuityError(w, fmt.Errorf("%w: invalid body", continuity.ErrValidation))
			return
		}
		var in In
		if err := continuity.DecodeJSON(raw, &in); err != nil {
			writeContinuityError(w, err)
			return
		}
		out, err := call(r.Context(), in)
		if err != nil {
			writeContinuityError(w, err)
			return
		}
		writeContinuity(w, http.StatusOK, out)
	}
}

func writeContinuity(w http.ResponseWriter, status int, out any) {
	buf, err := json.Marshal(out)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "continuity response unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

func writeContinuityError(w http.ResponseWriter, err error) {
	code := continuity.ErrorCode(err)
	status := http.StatusInternalServerError
	switch code {
	case "validation":
		status = http.StatusBadRequest
	case "unavailable":
		status = http.StatusNotFound
	case "conflict":
		status = http.StatusConflict
	case "unsupported":
		status = http.StatusNotImplemented
	}
	out := map[string]any{"protocol_version": 1, "code": code, "error": "continuity request failed: " + code}
	var detail *continuity.Error
	if errors.As(err, &detail) {
		out["error"] = detail.Message
		if detail.CurrentRevision != nil {
			out["current_revision"] = *detail.CurrentRevision
		}
	}
	writeContinuity(w, status, out)
}
