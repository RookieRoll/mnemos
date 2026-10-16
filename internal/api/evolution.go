package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/polyxmedia/mnemos/internal/memory"
)

type evidenceCheckRequest struct {
	EvidenceKey string `json:"evidence_key"`
	Status      string `json:"status"`
}

func (s *Server) evidenceCheck(w http.ResponseWriter, r *http.Request) {
	var in evidenceCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("decode: %s", err))
		return
	}
	out, err := s.mem.CheckEvidence(r.Context(), memory.EvidenceCheckInput{ObservationID: r.PathValue("id"), EvidenceKey: in.EvidenceKey, Status: in.Status})
	if err != nil {
		switch {
		case errors.Is(err, memory.ErrConflict):
			writeJSON(w, http.StatusConflict, map[string]string{"code": "conflict", "error": err.Error()})
		case errors.Is(err, memory.ErrInvalidEvolutionInput):
			writeErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, memory.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found")
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
