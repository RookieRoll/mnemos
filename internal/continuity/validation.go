package continuity

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

func invalid(message string) error {
	return &Error{Code: "validation", Message: message, Cause: ErrValidation}
}

// DecodeJSON strictly decodes a bounded request and never returns submitted text in errors.
func DecodeJSON(data []byte, target any) error {
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return invalid("request size or UTF-8 invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return invalid("invalid request JSON or unknown field")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return invalid("trailing JSON data")
	}
	return nil
}

func bounded(s string, max int) bool { return utf8.ValidString(s) && len(s) <= max }

func normalizeScope(version int, agent, cwd, root string) (string, error) {
	if version == 0 {
		return "", invalid("protocol_version is required and must be nonzero")
	}
	if version != ProtocolVersion {
		return "", &Error{Code: "unsupported", Message: "unsupported protocol version", Cause: ErrUnsupported}
	}
	if agent == "" {
		agent = "default"
	}
	if !bounded(agent, 256) || strings.TrimSpace(agent) == "" || !bounded(cwd, 4096) || !bounded(root, 4096) {
		return "", invalid("scope exceeds limits or is invalid")
	}
	return agent, nil
}

func validateContextOptions(source, task string, limit, maxBytes int) error {
	if !bounded(source, 256) || !bounded(task, 256) || limit < 0 || limit > 20 || maxBytes < 0 || maxBytes > MaxResponseBytes {
		return invalid("invalid source, task or response limits")
	}
	return nil
}

func validatePayload(p *Payload) error {
	if p == nil {
		return invalid("save requires payload")
	}
	if !bounded(p.Task, 256) || strings.TrimSpace(p.Task) == "" || !bounded(p.Summary, 4096) || strings.TrimSpace(p.Summary) == "" {
		return invalid("task or summary exceeds limits or is empty")
	}
	if p.State != "active" && p.State != "blocked" && p.State != "done" {
		return invalid("invalid reported state")
	}
	for _, list := range [][]string{p.NextSteps, p.Blockers} {
		if len(list) > 8 {
			return invalid("too many steps or blockers")
		}
		for _, s := range list {
			if !bounded(s, 512) {
				return invalid("step or blocker exceeds limits")
			}
		}
	}
	if len(p.References) > 16 {
		return invalid("too many references")
	}
	for _, ref := range p.References {
		if !bounded(ref.ID, 256) || ref.ID == "" || (ref.Kind != "file" && ref.Kind != "observation") {
			return invalid("invalid reference")
		}
		if ref.Kind == "file" {
			name := strings.ReplaceAll(ref.ID, "\\", "/")
			if strings.HasPrefix(name, "/") || strings.Contains(name, ":") || path.Clean(name) == ".." || strings.HasPrefix(path.Clean(name), "../") {
				return invalid("file reference must stay relative to workspace")
			}
		}
	}
	return nil
}

func encodedSize(v any) int { data, _ := json.Marshal(v); return len(data) }
