package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// scopedRead maps domain validation separately from operational failures.
func scopedRead[In any, Out any](read func(context.Context, In) (Out, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in In
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("decode: %s", err))
			return
		}
		out, err := read(r.Context(), in)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, memory.ErrInvalidReadInput) {
				status = http.StatusBadRequest
			}
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
