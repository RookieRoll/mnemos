package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/continuity"
)

// continuityValidationMiddleware keeps SDK-generated strict schemas intact but
// translates their pre-handler tool errors into the explicit continuity wire
// contract. Business errors already carry that envelope and pass through.
func continuityValidationMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, request mcpsdk.Request) (mcpsdk.Result, error) {
		req, ok := request.(*mcpsdk.CallToolRequest)
		continuityCall := method == "tools/call" && ok && req != nil && req.Params != nil && isContinuityCall(req)
		if continuityCall && len(req.Params.Arguments) > continuity.MaxRequestBytes {
			res, _, err := continuityError(continuity.ErrValidation)
			return res, err
		}
		result, err := next(ctx, method, request)
		if err != nil || !continuityCall {
			return result, err
		}
		response, ok := result.(*mcpsdk.CallToolResult)
		if !ok || !response.IsError || hasContinuityErrorEnvelope(response) {
			return result, nil
		}
		// Do not echo SDK validation text: it may contain submitted field data.
		res, _, err := continuityError(continuity.ErrValidation)
		return res, err
	}
}

func isContinuityCall(req *mcpsdk.CallToolRequest) bool {
	switch req.Params.Name {
	case "mnemos_checkpoint", "mnemos_resume_context":
		return true
	case "mnemos_session_start":
		var fields map[string]json.RawMessage
		if json.Unmarshal(req.Params.Arguments, &fields) != nil {
			return false
		}
		value, present := fields["continuity"]
		return present && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
	default:
		return false
	}
}

func hasContinuityErrorEnvelope(result *mcpsdk.CallToolResult) bool {
	for _, content := range result.Content {
		text, ok := content.(*mcpsdk.TextContent)
		if !ok {
			continue
		}
		var body struct {
			ProtocolVersion int    `json:"protocol_version"`
			Code            string `json:"code"`
		}
		if json.Unmarshal([]byte(text.Text), &body) == nil && body.ProtocolVersion == 1 && body.Code != "" {
			return true
		}
	}
	return false
}

func (s *Server) handleCheckpoint(ctx context.Context, req *mcpsdk.CallToolRequest, in continuity.CheckpointInput) (*mcpsdk.CallToolResult, any, error) {
	if s.cfg.Continuity == nil {
		return continuityError(continuity.ErrUnsupported)
	}
	if req != nil && req.Params != nil {
		if err := continuity.DecodeJSON(req.Params.Arguments, &in); err != nil {
			return continuityError(err)
		}
	}
	out, err := s.cfg.Continuity.Checkpoint(ctx, in)
	if err != nil {
		return continuityError(err)
	}
	return continuityResult(out)
}

func (s *Server) handleResumeContext(ctx context.Context, req *mcpsdk.CallToolRequest, in continuity.ContextInput) (*mcpsdk.CallToolResult, any, error) {
	if s.cfg.Continuity == nil {
		return continuityError(continuity.ErrUnsupported)
	}
	if req != nil && req.Params != nil {
		if err := continuity.DecodeJSON(req.Params.Arguments, &in); err != nil {
			return continuityError(err)
		}
	}
	out, err := s.cfg.Continuity.Context(ctx, in)
	if err != nil {
		return continuityError(err)
	}
	return continuityResult(out)
}

func continuityResult(out any) (*mcpsdk.CallToolResult, any, error) {
	// Compact encoding preserves the complete JSON byte budget returned by core.
	buf, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(buf)}}, StructuredContent: out}, nil, nil
}

func continuityError(err error) (*mcpsdk.CallToolResult, any, error) {
	code := continuity.ErrorCode(err)
	body := map[string]any{"protocol_version": 1, "code": code, "error": "continuity request failed: " + code}
	var detail *continuity.Error
	if errors.As(err, &detail) {
		body["error"] = detail.Message
		if detail.CurrentRevision != nil {
			body["current_revision"] = *detail.CurrentRevision
		}
	}
	res, _, marshalErr := continuityResult(body)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	res.IsError = true
	return res, nil, nil
}
