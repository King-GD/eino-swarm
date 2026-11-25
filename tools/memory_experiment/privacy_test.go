package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	experiment "github.com/LubyRuffy/eino-swarm/internal/memoryexperiment"
	"github.com/cloudwego/eino/schema"
)

// The complete catalog exists locally for overlap validation. Its contents
// must never be serialized into either provider protocol or model-input audit.
// All canaries below are synthetic and contain no personal or credential data.
func TestDreamKeepsCompleteCatalogLocalAtTheActualProviderBoundary(t *testing.T) {
	for _, mode := range []string{config.APIModeChatCompletions, config.APIModeResponses} {
		t.Run(mode, func(t *testing.T) {
			input := fixtureInput()
			input.EntryMax = 123
			input.Notes = []string{"CLEAN-NOTE-CANARY Preserve an explicit durable preference."}
			input.Sources[0].Kind = "user_message"
			input.Sources[0].Text = "CLEAN-SOURCE-CANARY Preserve the explicitly stated scope."
			input.Skills = []dreamSkill{{Name: "private-catalog-canary", Description: "PRIVATE-DESCRIPTION-CANARY: applicability retained locally.", Body: "PRIVATE-BODY-CANARY: procedure retained only for local overlap validation."}}
			forbidden := []string{input.Skills[0].Name, input.Skills[0].Description, input.Skills[0].Body, "PRIVATE-DESCRIPTION-CANARY", "PRIVATE-BODY-CANARY"}
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				rawRequest, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				for _, canary := range forbidden {
					if bytes.Contains(rawRequest, []byte(canary)) {
						t.Errorf("local catalog leaked in actual %s request: %q", mode, canary)
					}
				}
				for _, canary := range []string{"CLEAN-NOTE-CANARY", "CLEAN-SOURCE-CANARY"} {
					if !bytes.Contains(rawRequest, []byte(canary)) {
						t.Errorf("permitted input missing from actual request: %q", canary)
					}
				}
				var body map[string]any
				if err := json.Unmarshal(rawRequest, &body); err != nil {
					t.Error(err)
					return
				}
				if body["tools"] != nil {
					t.Error("tool-free writer exposed tools")
				}
				assertProviderCanaryEntryCap(t, body, input.EntryMax)
				snapshot := requestFrozenNotes(t, body)
				proposal := experiment.DreamProposal{Revision: snapshot.Rev, Changes: []experiment.DreamChange{}}
				raw, err := json.Marshal(proposal)
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var response any
				if mode == config.APIModeResponses {
					response = map[string]any{"id": "response", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": string(raw)}}}}, "usage": map[string]int{"input_tokens": 13, "output_tokens": 5, "total_tokens": 18}}
				} else {
					response = map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 13, "completion_tokens": 5, "total_tokens": 18}}
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer endpoint.Close()
			configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "configured", APIMode: mode})
			dir := t.TempDir()
			inputPath, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "out")
			writeTestJSON(t, inputPath, input)
			original, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := runDream(context.Background(), options{input: inputPath, out: out, config: configPath, provider: "fixture", timeout: 5}); err != nil {
				t.Fatal(err)
			}
			result := readDreamResult(t, out)
			if requests.Load() != 1 || len(result.ModelInput) == 0 || result.Error != "" || !result.CostComplete {
				t.Fatalf("privacy probe did not exercise a complete request: requests=%d calls=%d", requests.Load(), len(result.Calls))
			}
			for _, canary := range forbidden {
				if bytes.Contains(result.ModelInput, []byte(canary)) {
					t.Errorf("catalog leaked to model-input audit: %q", canary)
				}
			}
			for _, canary := range []string{"CLEAN-NOTE-CANARY", "CLEAN-SOURCE-CANARY"} {
				if !bytes.Contains(result.ModelInput, []byte(canary)) {
					t.Errorf("permitted input missing from model-input audit: %q", canary)
				}
			}
			var audited []*schema.Message
			if err := json.Unmarshal(result.ModelInput, &audited); err != nil {
				t.Fatal(err)
			}
			if len(audited) != 2 {
				t.Fatal("unexpected audited model input")
			}
			var auditedPayload map[string]any
			if err := json.Unmarshal([]byte(audited[1].Content), &auditedPayload); err != nil {
				t.Fatal(err)
			}
			if auditedPayload["entry_max"] != float64(input.EntryMax) {
				t.Errorf("actual per-note cap missing from model audit: %v", auditedPayload["entry_max"])
			}
			if strings.Contains(string(result.ModelInput), `"skills"`) {
				t.Fatal("model audit includes a catalog field")
			}
			local := memory.New(filepath.Join(out, "memory"), 2200)
			skill, err := local.ReadSkill(input.Skills[0].Name)
			if err != nil || skill.Description != input.Skills[0].Description || skill.Body != input.Skills[0].Body {
				t.Fatalf("local validation catalog lost: %v", err)
			}
			if _, err := os.Stat(filepath.Join(local.Dir(), memory.SkillsDir, skill.Name, memory.SkillUsageFile)); !os.IsNotExist(err) {
				t.Fatal("privacy probe fabricated skill use")
			}
			after, err := os.ReadFile(inputPath)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("frozen input changed")
			}
		})
	}
}

func assertProviderCanaryEntryCap(t *testing.T, body map[string]any, expected int) {
	t.Helper()
	var content string
	if items, ok := body["messages"].([]any); ok {
		for _, item := range items {
			message := item.(map[string]any)
			if message["role"] == "user" {
				content, _ = message["content"].(string)
			}
		}
	} else if items, ok := body["input"].([]any); ok {
		for _, item := range items {
			message := item.(map[string]any)
			if message["role"] == "user" {
				for _, part := range message["content"].([]any) {
					content, _ = part.(map[string]any)["text"].(string)
				}
			}
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Error(err)
		return
	}
	if payload["entry_max"] != float64(expected) {
		t.Errorf("per-note cap missing from actual provider payload: got %v, want %d", payload["entry_max"], expected)
	}
}

// The product's deliberately small front-matter parser only unquotes double
// quotes and backslashes. JSON escaping would change the local validation data.
func TestDreamLocalCatalogPreservesAllRepresentableDescriptionCharacters(t *testing.T) {
	for name, description := range map[string]string{
		"html":                "Use <input> & compare > output.",
		"quote and backslash": `Keep "quoted" paths such as C:\local\input and literal \u2028.`,
		"unicode separator":   "Preserve an internal\u2028separator exactly.",
		"tab":                 "Preserve an internal\ttab exactly.",
		"nul":                 "Preserve an internal\x00NUL exactly.",
		"outer whitespace":    " Preserve existing quoted outer whitespace. ",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			want := dreamSkill{Name: "complete-local-guard", Description: description, Body: "Inspect the original identity and verify the stored result."}
			if err := seedDreamSkills(dir, []dreamSkill{want}); err != nil {
				t.Fatal(err)
			}
			got, err := memory.New(dir, config.DefaultMemoryCharLimit).ReadSkill(want.Name)
			if err != nil || got.Name != want.Name || got.Description != want.Description || got.Body != want.Body {
				t.Fatalf("description round trip changed local guard: got %q want %q err=%v", got.Description, want.Description, err)
			}
		})
	}
}

func TestDreamLocalCatalogRejectsLiteralLineBreaksBeforeWritingAnySkill(t *testing.T) {
	for _, description := range []string{"first\nsecond", "first\rsecond", "first\r\nsecond"} {
		dir := filepath.Join(t.TempDir(), "new")
		input := []dreamSkill{{Name: "first", Description: "A valid single line.", Body: "An existing procedure."}, {Name: "second", Description: description, Body: "Another existing procedure."}}
		if err := seedDreamSkills(dir, input); err == nil {
			t.Fatal("unrepresentable front matter admitted")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("line-break refusal wrote a partial local guard")
		}
	}
}

func TestDreamLocalCatalogRefusesContentThatDoesNotRoundTripThroughProductReader(t *testing.T) {
	input := []dreamSkill{{Name: "exact-local-guard", Description: "A representable description.", Body: "  A noncanonical body with outer whitespace.\n"}}
	if err := seedDreamSkills(t.TempDir(), input); err == nil || !strings.Contains(err.Error(), "round-trip") {
		t.Fatalf("product readback mismatch was ignored: %v", err)
	}
	if input[0].Body != "  A noncanonical body with outer whitespace.\n" {
		t.Fatal("readback validation mutated the frozen input")
	}
}
