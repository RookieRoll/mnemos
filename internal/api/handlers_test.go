package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAllCrudEndpointsPresent(t *testing.T) {
	ts := newAPIServer(t, "")
	// Save → get by ID → delete → confirm 404.
	code, out := request(t, ts, "POST", "/v1/observations", "", map[string]any{
		"Title": "x", "Content": "y", "Type": "pattern",
	})
	if code != 201 {
		t.Fatalf("save: %d %v", code, out)
	}
	obs := out["Observation"].(map[string]any)
	id := obs["ID"].(string)

	code, _ = request(t, ts, "GET", "/v1/observations/"+id, "", nil)
	if code != 200 {
		t.Errorf("get: %d", code)
	}

	code, _ = request(t, ts, "DELETE", "/v1/observations/"+id, "", nil)
	if code != 204 {
		t.Errorf("delete: %d", code)
	}

	code, _ = request(t, ts, "GET", "/v1/observations/"+id, "", nil)
	if code != 404 {
		t.Errorf("expected 404 after delete, got %d", code)
	}
}

func TestStatsEndpoint(t *testing.T) {
	ts := newAPIServer(t, "")
	code, out := request(t, ts, "GET", "/v1/stats", "", nil)
	if code != 200 {
		t.Fatalf("stats: %d %v", code, out)
	}
	if _, ok := out["observations"]; !ok {
		t.Errorf("stats missing observations: %v", out)
	}
	if _, ok := out["embedding"]; !ok {
		t.Errorf("stats missing embedding section: %v", out)
	}
}

func TestSkillEndpoints(t *testing.T) {
	ts := newAPIServer(t, "")
	code, _ := request(t, ts, "POST", "/v1/skills", "", map[string]any{
		"Name":        "wire-route",
		"Description": "add an API route",
		"Procedure":   "define, register, test",
	})
	if code != 201 {
		t.Errorf("skill save: %d", code)
	}
	code, out := request(t, ts, "POST", "/v1/skills/match", "", map[string]any{
		"Query": "api route",
	})
	if code != 200 {
		t.Fatalf("skill match: %d %v", code, out)
	}
	matches := out["matches"].([]any)
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}
}

func TestCorrectionAndConventionEndpoints(t *testing.T) {
	ts := newAPIServer(t, "")
	code, _ := request(t, ts, "POST", "/v1/correct", "", map[string]any{
		"title":         "oauth retry",
		"tried":         "retry on 401",
		"wrong_because": "401 is auth",
		"fix":           "refresh then retry",
		"project":       "mnemos",
	})
	if code != 201 {
		t.Errorf("correction: %d", code)
	}
	code, _ = request(t, ts, "POST", "/v1/convention", "", map[string]any{
		"title":     "error wrap",
		"rule":      "use %w",
		"rationale": "chain preservation",
		"project":   "mnemos",
	})
	if code != 201 {
		t.Errorf("convention: %d", code)
	}
}

func TestTouchEndpoint(t *testing.T) {
	ts := newAPIServer(t, "")
	code, out := request(t, ts, "POST", "/v1/touch", "", map[string]any{
		"Path": "main.go", "Project": "mnemos",
	})
	if code != 201 {
		t.Fatalf("touch: %d %v", code, out)
	}
}

func TestLinkEndpoint(t *testing.T) {
	ts := newAPIServer(t, "")
	ids := make([]string, 2)
	for i := range ids {
		_, out := request(t, ts, "POST", "/v1/observations", "", map[string]any{
			"Title": fmt.Sprintf("obs %d", i), "Content": fmt.Sprintf("body %d", i), "Type": "decision",
		})
		ids[i] = out["Observation"].(map[string]any)["ID"].(string)
	}
	code, _ := request(t, ts, "POST", "/v1/link", "", map[string]any{
		"source_id": ids[1], "target_id": ids[0], "link_type": "supersedes",
	})
	if code != 200 {
		t.Errorf("link: %d", code)
	}
	// Default search should now hide the superseded one.
	code, out := request(t, ts, "POST", "/v1/search", "", map[string]any{
		"Query": "obs",
	})
	if code != 200 {
		t.Fatal(code)
	}
	results := out["results"].([]any)
	for _, r := range results {
		hit := r.(map[string]any)
		if hit["Observation"].(map[string]any)["ID"] == ids[0] {
			t.Error("superseded observation should be hidden")
		}
	}
}

func TestBadJSONReturns400(t *testing.T) {
	ts := newAPIServer(t, "")
	resp, err := http.Post(ts.URL+"/v1/observations", "application/json",
		nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 on empty body, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if _, ok := body["error"]; !ok {
		t.Errorf("expected error field in response: %v", body)
	}
}

func TestPinEndpoints(t *testing.T) {
	ts := newAPIServer(t, "")

	// 1. Save unpinned observation
	code, out := request(t, ts, "POST", "/v1/observations", "", map[string]any{
		"Title": "api note", "Content": "content", "Type": "context",
	})
	if code != 201 {
		t.Fatalf("save: %d %v", code, out)
	}
	obs := out["Observation"].(map[string]any)
	id := obs["ID"].(string)

	// 2. Pin via POST /v1/observations/{id}/pin
	code, pinOut := request(t, ts, "POST", "/v1/observations/"+id+"/pin", "", map[string]any{
		"pinned": true,
	})
	if code != 200 {
		t.Fatalf("pin: %d %v", code, pinOut)
	}
	if pinOut["Pinned"] != true {
		t.Errorf("pin endpoint should return Pinned=true: %v", pinOut)
	}

	// 3. Unpin via POST /v1/observations/{id}/pin
	code, unpinOut := request(t, ts, "POST", "/v1/observations/"+id+"/pin", "", map[string]any{
		"pinned": false,
	})
	if code != 200 {
		t.Fatalf("unpin: %d %v", code, unpinOut)
	}
	if unpinOut["Pinned"] != false {
		t.Errorf("unpin endpoint should return Pinned=false: %v", unpinOut)
	}

	// 4. Pin nonexistent returns 404
	code, _ = request(t, ts, "POST", "/v1/observations/nonexistent/pin", "", map[string]any{
		"pinned": true,
	})
	if code != 404 {
		t.Errorf("pin nonexistent should return 404, got %d", code)
	}

	// 5. Convention endpoint supports pinned
	code, convOut := request(t, ts, "POST", "/v1/convention", "", map[string]any{
		"title": "zero cgo", "rule": "keep pure go", "project": "p", "pinned": true,
	})
	if code != 201 {
		t.Fatalf("convention save: %d %v", code, convOut)
	}
	convObs := convOut["Observation"].(map[string]any)
	if convObs["Pinned"] != true {
		t.Errorf("convention should be pinned: %v", convObs)
	}

	// 6. Correct endpoint supports pinned
	code, corrOut := request(t, ts, "POST", "/v1/correct", "", map[string]any{
		"title": "fixed bug", "tried": "x", "wrong_because": "y", "fix": "z", "pinned": true,
	})
	if code != 201 {
		t.Fatalf("correct save: %d %v", code, corrOut)
	}
	corrObs := corrOut["Observation"].(map[string]any)
	if corrObs["Pinned"] != true {
		t.Errorf("correction should be pinned: %v", corrObs)
	}
}
