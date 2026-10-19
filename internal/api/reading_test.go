package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestScopedReadHTTPValidation(t *testing.T) {
	ts := newAPIServer(t, "")
	for _, tc := range []struct {
		path string
		body any
		want int
	}{
		{"batch", map[string]any{"ids": []string{"missing"}}, 400},
		{"batch", map[string]any{"project": "p", "ids": []string{}}, 400},
		{"batch", map[string]any{"project": "p", "ids": []string{"missing"}, "max_bytes": 262145}, 400},
		{"batch", map[string]any{"project": "p", "ids": []string{"missing"}, "as_of": "bad-time"}, 400},
		{"batch", map[string]any{"project": "p", "ids": []string{"missing"}}, 200},
		{"neighborhood", map[string]any{"project": "p", "id": "missing", "before": -1}, 400},
		{"neighborhood", map[string]any{"project": "p", "id": "missing", "after": 11}, 400},
		{"neighborhood", map[string]any{"project": "p", "id": "missing", "before": 0, "after": 0}, 200},
	} {
		t.Run(tc.path, func(t *testing.T) {
			code, out := request(t, ts, "POST", "/v1/observations/"+tc.path, "", tc.body)
			if code != tc.want {
				t.Fatalf("status=%d want=%d out=%v", code, tc.want, out)
			}
		})
	}
}

func TestScopedReadHTTPOperationalFailure(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "failed.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := memory.NewService(memory.Config{Store: db.Observations()})
	_ = db.Close()
	ts := httptest.NewServer(api.NewServer(api.Config{Memory: svc}).Handler())
	defer ts.Close()
	for _, path := range []string{"batch", "neighborhood"} {
		code, _ := request(t, ts, http.MethodPost, "/v1/observations/"+path, "", map[string]any{"project": "p", "ids": []string{"a"}, "id": "a"})
		if code != 500 {
			t.Fatalf("operational failure returned %d", code)
		}
	}
}
