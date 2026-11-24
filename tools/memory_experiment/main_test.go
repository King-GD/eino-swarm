package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	experiment "github.com/LubyRuffy/eino-swarm/internal/memoryexperiment"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// Invalid invocations must stop before creating evidence or opening a model.
func TestInvalidExperimentArgumentsNeverStartWork(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"dream"}, {"serve"}, {"serve", "--memory-dir", t.TempDir(), "--limit", "0"}} {
		var output bytes.Buffer
		if runCLI(context.Background(), args, &output, &output) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestOptionsRejectInvalidValuesAndKeepExplicitOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--unknown"}, {"serve", "--memory-dir", "x", "extra"}, {"serve", "--memory-dir", "x", "--max-chars", "2"},
		{"dream", "--input", "x", "--out", "y", "--config", "z", "--provider", "p", "--timeout-seconds", "NaN"},
		{"dream", "--input", "x", "--out", "y", "--config", "z", "--provider", "p", "--timeout-seconds", "+Inf"},
		{"dream", "--input", "x", "--out", "y", "--config", "z", "--provider", "p", "--timeout-seconds", "3601"},
		{"dream", "--input", "x", "--out", "y", "--config", "z", "--provider", "p", "--timeout-seconds", "0"},
	} {
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parseOptions([]string{"dream", "--input", "x", "--out", "y", "--config", "z", "--provider", "p", "--model", "override", "--timeout-seconds", "3"}, io.Discard)
	if err != nil || o.model != "override" || o.timeout != 3 {
		t.Fatalf("options %+v %v", o, err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeTestConfig(t *testing.T, p config.Provider) string {
	t.Helper()
	cfg := config.Default()
	cfg.Models.Default = p.ID
	cfg.Models.Providers = []config.Provider{p, {ID: "unused", BaseURL: "http://unused.invalid", Model: "unused"}}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "provider.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureInput() dreamInput {
	return dreamInput{ProjectID: "project", Cutoff: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), Notes: []string{"An obsolete belief."}, Sources: []experiment.DreamSource{{ProjectID: "project", ThreadID: "original", Seq: 1, Kind: "human", Text: "Use the corrected setting.", ObservedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}}
}

func TestInputAndProviderLoadingNeverRewriteTheirSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	for _, raw := range []string{`{`, `null`, `{}`, `{"project_id":"p","cutoff":"2021-01-01T00:00:00Z","target_question":"leak"}`, `{"project_id":"p","cutoff":"2021-01-01T00:00:00Z"} {}`, `{"project_id":"p","cutoff":"2021-01-01T00:00:00Z","char_limit":-1}`, `{"project_id":"p","cutoff":"2021-01-01T00:00:00Z","entry_max":-1}`, `{"project_id":"p","cutoff":"2021-01-01T00:00:00Z","char_limit":1,"entry_max":2}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readInput(path); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := readInput(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing input accepted")
	}
	writeTestJSON(t, path, fixtureInput())
	in, err := readInput(path)
	if err != nil || in.CharLimit != config.DefaultMemoryCharLimit || in.EntryMax != config.DefaultMemoryEntryMax {
		t.Fatalf("default limits %+v %v", in, err)
	}
	p := config.Provider{ID: "selected", BaseURL: "http://loopback.invalid", APIKey: "private-key-fixture", Model: "configured", APIMode: config.APIModeResponses, BuiltinTools: []map[string]any{{"type": "web_search"}}}
	configPath := writeTestConfig(t, p)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, secret, err := loadProvider(options{config: configPath, provider: p.ID, model: "override"})
	if err != nil || secret != p.APIKey || len(cfg.Models.Providers) != 1 || cfg.Models.Providers[0].Model != "override" || len(cfg.Models.Providers[0].BuiltinTools) != 0 {
		t.Fatalf("provider scope/builtins %+v %q %v", cfg, secret, err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("loading changed the source provider configuration")
	}
	for _, o := range []options{{config: "missing", provider: p.ID}, {config: configPath, provider: "missing"}} {
		if _, _, err := loadProvider(o); err == nil {
			t.Fatal("invalid provider accepted")
		}
	}
	if err := os.WriteFile(configPath, []byte("models: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadProvider(options{config: configPath, provider: p.ID}); err == nil {
		t.Fatal("malformed provider accepted")
	}
}

func TestEvidenceErrorsAndExistingPermissionsAreHandled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	if err := os.WriteFile(path, []byte("old"), 0666); err != nil {
		t.Fatal(err)
	}
	before, err := exec.Command("sh", "-c", "umask").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(path, map[string]string{"a": "plain"}, ""); err != nil {
		t.Fatal(err)
	}
	after, err := exec.Command("sh", "-c", "umask").Output()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("evidence creation changed the inherited subprocess umask")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("existing evidence was not made private")
	}
	if err := saveJSON(path, func() {}, ""); err == nil {
		t.Fatal("JSON encoding failure ignored")
	}
	if err := saveJSON(dir, map[string]string{}, ""); err == nil {
		t.Fatal("evidence path failure ignored")
	}
}

func TestRecallMCPTransportExposesOnlyRecordedMetadata(t *testing.T) {
	dir := t.TempDir()
	store := memory.New(dir, 2200)
	if _, err := store.WriteSkill("recovery", "When recovering presence.", "Private procedure body."); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir           string
		limit, budget int
	}{{filepath.Join(dir, "absent"), 1, 1000}, {filepath.Join(dir, "skills", "recovery", "SKILL.md"), 1, 1000}, {dir, 0, 1000}} {
		if _, err := recallServer(tc.dir, tc.limit, tc.budget); err == nil {
			t.Fatalf("accepted invalid server %+v", tc)
		}
	}
	server, err := recallServer(dir, 3, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != experiment.ToolRecallMemory {
		t.Fatalf("unexpected tools %+v %v", listed, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: experiment.ToolRecallMemory, Arguments: map[string]string{"query": "presence"}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("MCP recall failed %+v %v", result, err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "recovery") || strings.Contains(text.Text, "Private procedure body") {
		t.Fatalf("MCP response lost scope or disclosed bodies: %+v", result)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: experiment.ToolRecallMemory, Arguments: map[string]string{"query": "!!!"}})
	if err == nil && !result.IsError {
		t.Fatal("invalid recall query accepted through actual MCP handler")
	}
}

func readDreamResult(t *testing.T, out string) dreamResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, "dream.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result dreamResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func requestFrozenNotes(t *testing.T, body map[string]any) memory.Snapshot {
	t.Helper()
	var content string
	if items, ok := body["messages"].([]any); ok {
		for _, item := range items {
			m := item.(map[string]any)
			if m["role"] == "user" {
				content, _ = m["content"].(string)
			}
		}
	} else if items, ok := body["input"].([]any); ok {
		for _, item := range items {
			m := item.(map[string]any)
			if m["role"] == "user" {
				for _, part := range m["content"].([]any) {
					content, _ = part.(map[string]any)["text"].(string)
				}
			}
		}
	}
	var input struct {
		Notes memory.Snapshot `json:"notes"`
	}
	if err := json.Unmarshal([]byte(content), &input); err != nil {
		t.Errorf("missing actual frozen notes: %v", err)
	}
	return input.Notes
}

func TestDreamActualProviderRecordsCostsAndCommitsOnlyAnIsolatedCopy(t *testing.T) {
	for _, mode := range []string{config.APIModeChatCompletions, config.APIModeResponses} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["tools"] != nil || body["model"] != "override" {
					t.Errorf("Dream exposed tools or lost model override: %+v", body)
				}
				snap := requestFrozenNotes(t, body)
				proposal := experiment.DreamProposal{Revision: snap.Rev, Changes: []experiment.DreamChange{{Action: "replace", OldText: snap.Entries[0], NewText: "Use the corrected setting.", Evidence: []experiment.DreamCitation{{ThreadID: "original", Seq: 1, Quote: "Use the corrected setting."}}}}}
				raw, err := json.Marshal(proposal)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				var response any
				if mode == config.APIModeResponses {
					response = map[string]any{"id": "response", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": string(raw)}}}}, "usage": map[string]any{"input_tokens": 17, "output_tokens": 7, "total_tokens": 24, "input_tokens_details": map[string]any{"cached_tokens": 3}}}
				} else {
					response = map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 7, "total_tokens": 24, "prompt_tokens_details": map[string]any{"cached_tokens": 3}}}
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer endpoint.Close()
			configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", APIKey: "secret-fixture-value", Model: "configured", APIMode: mode, BuiltinTools: []map[string]any{{"type": "web_search"}}})
			dir := t.TempDir()
			inputPath := filepath.Join(dir, "input.json")
			writeTestJSON(t, inputPath, fixtureInput())
			original, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out")
			var output, errors bytes.Buffer
			code := runCLI(context.Background(), []string{"dream", "--input", inputPath, "--out", out, "--config", configPath, "--provider", "fixture", "--model", "override"}, &output, &errors)
			if code != 0 {
				t.Fatalf("Dream failed: %s", errors.String())
			}
			result := readDreamResult(t, out)
			if calls.Load() != 1 || !result.CostComplete || result.Error != "" || len(result.Calls) != 1 || result.Calls[0].Prompt != 17 || result.Calls[0].Completion != 7 || result.Calls[0].Total != 24 || result.Calls[0].Cached != 3 || len(result.Notes) != 1 || result.Notes[0] != "Use the corrected setting." {
				t.Fatalf("wrong Dream outcome %+v", result)
			}
			after, err := os.ReadFile(inputPath)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("Dream mutated its frozen input")
			}
			info, err := os.Stat(filepath.Join(out, "dream.json"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("Dream receipt was not private")
			}
			if !strings.Contains(output.String(), filepath.Join(out, "dream.json")) {
				t.Fatalf("missing evidence locator: %s", output.String())
			}
			if err := runDream(context.Background(), options{input: inputPath, out: out, config: configPath, provider: "fixture", timeout: 1}); err == nil || calls.Load() != 1 {
				t.Fatal("existing output directory was reused or model called again")
			}
		})
	}
}

func TestDreamFailuresRetainObservedCallsAndDoNotApplyInvalidChanges(t *testing.T) {
	for _, kind := range []string{"malformed", "entry-cap", "output-limit", "http-error", "zero-usage", "receipt-error"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			dir := t.TempDir()
			out := filepath.Join(dir, "out")
			input := fixtureInput()
			if kind == "entry-cap" {
				input.Notes = []string{"old"}
				input.EntryMax = 4
			}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if kind == "http-error" {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"error":{"message":"secret-fixture-value refused","type":"permission_denied"}}`)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				snap := requestFrozenNotes(t, body)
				proposal := experiment.DreamProposal{Revision: snap.Rev, Changes: []experiment.DreamChange{}}
				if kind == "entry-cap" || kind == "output-limit" {
					proposal.Changes = []experiment.DreamChange{{Action: "replace", OldText: snap.Entries[0], NewText: input.Sources[0].Text, Evidence: []experiment.DreamCitation{{ThreadID: "original", Seq: 1, Quote: input.Sources[0].Text}}}}
				}
				raw, err := json.Marshal(proposal)
				if err != nil {
					t.Error(err)
				}
				content := string(raw)
				if kind == "malformed" {
					content = "This is not a proposal."
				}
				if kind == "receipt-error" {
					if err := os.Mkdir(filepath.Join(out, "dream.json"), 0700); err != nil {
						t.Error(err)
					}
				}
				finishReason := "stop"
				if kind == "output-limit" {
					finishReason = "length"
				}
				response := map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": finishReason}}}
				if kind != "zero-usage" {
					response["usage"] = map[string]int{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer endpoint.Close()
			configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", APIKey: "secret-fixture-value", Model: "configured"})
			inputPath := filepath.Join(dir, "input.json")
			writeTestJSON(t, inputPath, input)
			err := runDream(context.Background(), options{input: inputPath, out: out, config: configPath, provider: "fixture", timeout: 2})
			if calls.Load() != 1 {
				t.Fatalf("unexpected calls %d", calls.Load())
			}
			if kind == "receipt-error" {
				if err == nil {
					t.Fatal("receipt failure concealed")
				}
				return
			}
			result := readDreamResult(t, out)
			if kind == "zero-usage" {
				if err != nil || result.Error != "" || result.FailureKind != "" || result.CostComplete || len(result.Calls) != 1 || result.Calls[0].Total != 0 {
					t.Fatalf("missing usage represented as complete: %+v %v", result, err)
				}
				return
			}
			if err == nil || result.Error == "" || len(result.Calls) != 1 || strings.Join(result.Notes, "\n") != strings.Join(input.Notes, "\n") {
				t.Fatalf("failed Dream lost failure/evidence/notes: %+v %v", result, err)
			}
			if kind != "http-error" && (!result.CostComplete || result.Calls[0].Total != 16 || result.Calls[0].Error != "") {
				t.Fatalf("candidate failure incorrectly invalidated successful paid-call telemetry: %+v", result)
			}
			wantKind := "proposal_invalid"
			if kind == "output-limit" {
				wantKind = "output_limit"
			}
			if kind == "http-error" {
				wantKind = "input_or_provider_error"
				if result.CostComplete {
					t.Fatal("failed provider request was represented as complete cost")
				}
			}
			if result.FailureKind != wantKind {
				t.Fatalf("failed Dream classification %q, want %q", result.FailureKind, wantKind)
			}
			if kind == "http-error" {
				raw, err := os.ReadFile(filepath.Join(out, "dream.json"))
				if err != nil || bytes.Contains(raw, []byte("secret-fixture-value")) || result.Calls[0].Error == "" {
					t.Fatal("provider failure lost its typed receipt or disclosed key")
				}
			}
			if kind == "malformed" && (result.ModelOutput == nil || result.ModelOutput.Content != "This is not a proposal." || len(result.ModelInput) == 0) {
				t.Fatalf("failed proposal lost its original model input/output: %+v", result)
			}
		})
	}
}

func TestDreamAuditRefusesUnencodableInputBeforeCallingInnerModel(t *testing.T) {
	result := dreamResult{}
	// No inner model is installed: any attempt to forward this malformed input
	// would panic instead of passing the refusal assertion.
	audit := dreamAuditModel{result: &result}
	_, err := audit.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "input", Extra: map[string]any{"unsupported": make(chan int)}}})
	if err == nil || len(result.ModelInput) != 0 || result.ModelOutput != nil {
		t.Fatalf("unencodable model input was forwarded or silently lost: %+v %v", result, err)
	}
}

func TestEvidenceRedactsEscapedAndShortSecretsWithoutCorruptingJSON(t *testing.T) {
	for _, secret := range []string{"fixture\"key", "fixture\\key", "fixture\nkey", "\"", "x"} {
		path := filepath.Join(t.TempDir(), "receipt.json")
		if err := saveJSON(path, map[string]any{"text": "before " + secret + " after", "nested": []any{map[string]string{"message": secret}}}, secret); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("secret %q corrupted JSON: %v", secret, err)
		}
		if decoded["text"] != "before <redacted> after" {
			t.Fatalf("secret %q leaked or changed receipt keys: %+v", secret, decoded)
		}
		nested := decoded["nested"].([]any)[0].(map[string]any)
		if nested["message"] != "<redacted>" {
			t.Fatalf("nested secret %q leaked: %+v", secret, nested)
		}
	}
	path := filepath.Join(t.TempDir(), "numeric.json")
	if err := saveJSON(path, map[string]any{"count": json.Number("18446744073709551615"), "nested": []any{nil, false}}, "private-value"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("18446744073709551615")) {
		t.Fatalf("redaction rounded an unrelated numeric fact: %s %v", raw, err)
	}
}

func TestDreamRejectsFutureOrForeignSourcesBeforeAnyModelRequest(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusTeapot) }))
	defer endpoint.Close()
	configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "configured"})
	for _, kind := range []string{"future", "foreign", "invalid-provider", "bad-config", "bad-input", "initial-cap"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			input := fixtureInput()
			path := filepath.Join(dir, "input.json")
			out := filepath.Join(dir, "out")
			o := options{input: path, out: out, config: configPath, provider: "fixture", timeout: 1}
			switch kind {
			case "future":
				input.Sources[0].ObservedAt = input.Cutoff
			case "foreign":
				input.Sources[0].ProjectID = "another-project"
			case "invalid-provider":
				o.config = writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: "", Model: "configured"})
			case "bad-config":
				o.config = filepath.Join(dir, "absent-config")
			case "bad-input":
				o.input = filepath.Join(dir, "absent-input")
			case "initial-cap":
				input.CharLimit = 1
				input.EntryMax = 1
			}
			writeTestJSON(t, path, input)
			if err := runDream(context.Background(), o); err == nil {
				t.Fatal("invalid Dream accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("invalid Dream reached the model")
			}
			if kind == "future" || kind == "foreign" || kind == "invalid-provider" {
				result := readDreamResult(t, out)
				if len(result.Calls) != 0 || result.Error == "" || result.FailureKind != "input_or_provider_error" || result.CostComplete || len(result.ModelInput) != 0 || result.ModelOutput != nil {
					t.Fatalf("refusal has false request accounting %+v", result)
				}
			}
		})
	}
}

func TestMemoryExperimentStdioHelper(t *testing.T) {
	if os.Getenv("MEMORY_EXPERIMENT_STDIO_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(runCLI(context.Background(), os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(3)
}

func TestServeCLIWorksOverActualStdioAndExitsOnClose(t *testing.T) {
	dir := t.TempDir()
	store := memory.New(dir, 2200)
	if _, err := store.WriteSkill("lease", "When recovering a lease.", "The private procedure."); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMemoryExperimentStdioHelper$", "--", "serve", "--memory-dir", dir)
	command.Env = append(os.Environ(), "MEMORY_EXPERIMENT_STDIO_HELPER=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-fixture", Version: "1"}, nil)
	// A race-instrumented test binary normally waits one second on exit. Leave
	// that runtime-owned grace intact before the transport can send SIGTERM.
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("stdio connect failed: %v %s", err, stderr.String())
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: experiment.ToolRecallMemory, Arguments: map[string]string{"query": "lease"}})
	if err != nil || result.IsError {
		t.Fatalf("stdio call failed %+v %v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("stdio cleanup failed: %v %s", err, stderr.String())
	}
	if command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatalf("owned MCP process did not exit successfully: %v %s", command.ProcessState, stderr.String())
	}
}

func TestDreamCatalogCopyPreservesExactSkillContentWithoutUsage(t *testing.T) {
	dir := t.TempDir()
	skills := []dreamSkill{{Name: "first", Description: "When \"quoted\" conditions apply: 包含中文。", Body: "Observe the precondition.\nThen verify the result."}, {Name: "second", Description: "For another subject.", Body: "Its independent procedure."}}
	if err := seedDreamSkills(dir, skills); err != nil {
		t.Fatal(err)
	}
	store := memory.New(dir, 2200)
	for _, expected := range skills {
		actual, err := store.ReadSkill(expected.Name)
		if err != nil || actual.Description != expected.Description || actual.Body != expected.Body || actual.Views != 0 || !actual.LastViewedAt.IsZero() {
			t.Fatalf("catalog copy changed content/usage: %+v %v", actual, err)
		}
		info, err := os.Stat(filepath.Join(dir, memory.SkillsDir, expected.Name, memory.SkillFile))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("copied skill was not private")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, memory.SkillsDir, "first", memory.SkillUsageFile)); !os.IsNotExist(err) {
		t.Fatal("catalog seeding fabricated use")
	}
	for _, invalid := range [][]dreamSkill{
		{{Name: "../escape", Description: "condition", Body: "procedure"}},
		{{Name: "first", Description: "condition", Body: "procedure"}, {Name: "first", Description: "different", Body: "another"}},
		{{Name: "first", Description: " ", Body: "procedure"}},
		{{Name: "first", Description: "condition", Body: " "}},
	} {
		target := filepath.Join(t.TempDir(), "new")
		if err := seedDreamSkills(target, invalid); err == nil {
			t.Fatalf("invalid catalog accepted: %+v", invalid)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("invalid catalog partially wrote before preflight finished")
		}
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seedDreamSkills(blocked, skills); err == nil {
		t.Fatal("catalog directory failure ignored")
	}
	blocked = t.TempDir()
	if err := os.MkdirAll(filepath.Join(blocked, memory.SkillsDir, "first", memory.SkillFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := seedDreamSkills(blocked, skills); err == nil {
		t.Fatal("catalog file write failure ignored")
	}
}

func TestDreamInputRequiresCanonicalIndividualNotes(t *testing.T) {
	for _, note := range []string{"", " leading", "trailing ", "first\n\nsecond", "first\r\n\r\nsecond"} {
		input := fixtureInput()
		input.Notes = []string{note}
		path := filepath.Join(t.TempDir(), "input.json")
		writeTestJSON(t, path, input)
		if _, err := readInput(path); err == nil {
			t.Fatalf("noncanonical note accepted: %q", note)
		}
	}
}

func TestDreamCannotTurnAnExistingSkillIntoAnAlwaysLoadedNote(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	input := fixtureInput()
	input.Skills = []dreamSkill{{Name: "project-recovery", Description: "Use when recovering the project engine.", Body: "Preserve the original engine identity and verify its presence before reconnecting."}}
	input.Sources[0].Text = input.Skills[0].Body
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		snap := requestFrozenNotes(t, body)
		proposal := experiment.DreamProposal{Revision: snap.Rev, Changes: []experiment.DreamChange{{Action: "add", NewText: input.Skills[0].Body, Evidence: []experiment.DreamCitation{{ThreadID: "original", Seq: 1, Quote: input.Sources[0].Text}}}}}
		raw, err := json.Marshal(proposal)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer endpoint.Close()
	configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "configured"})
	path := filepath.Join(dir, "input.json")
	writeTestJSON(t, path, input)
	if err := runDream(context.Background(), options{input: path, out: out, config: configPath, provider: "fixture", timeout: 2}); err == nil {
		t.Fatal("Dream bypassed actual note/skill overlap checks")
	}
	result := readDreamResult(t, out)
	if calls.Load() != 1 || result.Error == "" || result.FailureKind != "proposal_invalid" || !result.CostComplete || len(result.Notes) != 1 || result.Notes[0] != input.Notes[0] || len(result.Calls) != 1 || result.Calls[0].Total != 16 {
		t.Fatalf("overlap refusal lost original state/cost: %+v", result)
	}
	actual, err := memory.New(filepath.Join(out, "memory"), 2200).ReadSkill(input.Skills[0].Name)
	if err != nil || actual.Body != input.Skills[0].Body {
		t.Fatalf("overlap validation did not retain original skill: %+v %v", actual, err)
	}
}

func TestPrivateEvidenceDoesNotChangeSubprocessUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := saveJSON(path, map[string]string{"text": "sensitive-value"}, "sensitive-value"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || bytes.Contains(data, []byte("sensitive-value")) {
		t.Fatalf("unsafe evidence %v %s", info.Mode(), data)
	}
}

func TestDreamWithNoNotesOrChangesReturnsAnExplicitEmptyNotesArray(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		snap := requestFrozenNotes(t, body)
		proposal := experiment.DreamProposal{Revision: snap.Rev, Changes: []experiment.DreamChange{}}
		raw, err := json.Marshal(proposal)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer endpoint.Close()
	configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "configured"})
	dir := t.TempDir()
	input := fixtureInput()
	input.Notes = []string{}
	inputPath, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "out")
	writeTestJSON(t, inputPath, input)
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), []string{"dream", "--input", inputPath, "--out", out, "--config", configPath, "--provider", "fixture"}, &stdout, &stderr); code != 0 {
		t.Fatalf("valid empty proposal failed CLI: %s", stderr.String())
	}
	result := readDreamResult(t, out)
	if calls.Load() != 1 || !result.CostComplete || result.Error != "" || result.FailureKind != "" || result.Notes == nil || len(result.Notes) != 0 {
		t.Fatalf("empty memory lost its array contract: notes_nil=%t calls=%d cost_complete=%t error=%q kind=%q", result.Notes == nil, calls.Load(), result.CostComplete, result.Error, result.FailureKind)
	}
	raw, err := os.ReadFile(filepath.Join(out, "dream.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["notes"]) != "[]" {
		t.Fatalf("consumer requires an explicit empty notes array, got %s", wire["notes"])
	}
}
