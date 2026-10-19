package api_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEvolutionAPIDocumentExamples(t *testing.T) {
	ts := newAPIServer(t, "")
	data, err := os.ReadFile("../../docs/API.md")
	if err != nil {
		t.Fatal(err)
	}
	var id, key string
	tested := 0
	for _, block := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "```json\n")[1:] {
		var in map[string]any
		if err := json.Unmarshal([]byte(strings.SplitN(block, "```", 2)[0]), &in); err != nil {
			t.Fatal(err)
		}
		if in["Title"] != nil {
			tested++
			code, out := request(t, ts, "POST", "/v1/observations", "", in)
			if code != 201 {
				t.Fatalf("documented save rejected: %d %v", code, out)
			}
			o := out["Observation"].(map[string]any)
			id = o["ID"].(string)
			refs := o["evidence"].([]any)
			key = refs[0].(map[string]any)["key"].(string)
		} else if in["evidence_key"] != nil {
			tested++
			if id == "" || key == "" {
				t.Fatal("check example requires documented saved reference")
			}
			in["evidence_key"] = key
			code, out := request(t, ts, "POST", "/v1/observations/"+id+"/evidence/check", "", in)
			if code != 200 || out["status"] != "changed" {
				t.Fatalf("documented check rejected: %d %v", code, out)
			}
		}
	}
	if id == "" || tested != 2 {
		t.Fatalf("expected2 evolution JSON examples, executed%d", tested)
	}
	t.Logf("executed %d evolution JSON examples", tested)
}
