// Command memory_experiment supplies opt-in candidates without changing the engine.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	experiment "github.com/LubyRuffy/eino-swarm/internal/memoryexperiment"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

type options struct {
	command, memoryDir, input, out, config, provider, model string
	limit, maxChars                                         int
	timeout                                                 float64
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var o options
	if len(args) == 0 || (args[0] != "serve" && args[0] != "dream") {
		return o, errors.New("expected serve or dream")
	}
	o.command = args[0]
	f := flag.NewFlagSet(o.command, flag.ContinueOnError)
	f.SetOutput(stderr)
	if o.command == "serve" {
		f.StringVar(&o.memoryDir, "memory-dir", "", "explicit private candidate memory directory")
		f.IntVar(&o.limit, "limit", 8, "maximum matching skills")
		f.IntVar(&o.maxChars, "max-chars", 6000, "complete recall JSON character budget")
	} else {
		f.StringVar(&o.input, "input", "", "frozen notes and past sources JSON; no target question")
		f.StringVar(&o.out, "out", "", "new private evidence directory")
		f.StringVar(&o.config, "config", "", "read-only provider YAML")
		f.StringVar(&o.provider, "provider", "", "selected provider id")
		f.StringVar(&o.model, "model", "", "optional configured-provider model override")
		f.Float64Var(&o.timeout, "timeout-seconds", 120, "whole Dream deadline")
	}
	if err := f.Parse(args[1:]); err != nil {
		return o, err
	}
	if len(f.Args()) != 0 || (o.command == "serve" && (o.memoryDir == "" || o.limit <= 0 || o.maxChars < 256)) ||
		(o.command == "dream" && (o.input == "" || o.out == "" || o.config == "" || o.provider == "" || o.timeout <= 0 || math.IsNaN(o.timeout) || math.IsInf(o.timeout, 0) || o.timeout > 3600)) {
		return o, errors.New("invalid experiment paths or budgets")
	}
	return o, nil
}

// recallServer exposes only metadata discovery. skill_view remains the engine's tool.
func recallServer(dir string, limit, maxChars int) (*mcp.Server, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("memory directory required")
	}
	recall, err := experiment.NewRecallTool(memory.New(dir, config.DefaultMemoryCharLimit), limit, maxChars)
	if err != nil {
		return nil, err
	}
	schema, err := recall.Info(context.Background())
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "memory-experiment", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: schema.Name, Description: schema.Desc, InputSchema: map[string]any{
		"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string"}}, "additionalProperties": false,
	}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := recall.InvokableRun(ctx, string(request.Params.Arguments))
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result}}}, nil
	})
	return server, nil
}

type dreamInput struct {
	ProjectID string                   `json:"project_id"`
	Cutoff    time.Time                `json:"cutoff"`
	Notes     []string                 `json:"notes"`
	Sources   []experiment.DreamSource `json:"sources"`
	CharLimit int                      `json:"char_limit,omitempty"`
	EntryMax  int                      `json:"entry_max,omitempty"`
	Skills    []dreamSkill             `json:"skills,omitempty"`
}

type dreamSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// The private target retains the original catalog so note/skill overlap checks
// see the same procedures as the baseline. This copies content, never usage.
func seedDreamSkills(dir string, skills []dreamSkill) error {
	seen := map[string]bool{}
	for _, skill := range skills {
		if memory.ValidSkillName(skill.Name) != nil || seen[skill.Name] || strings.TrimSpace(skill.Description) == "" || strings.TrimSpace(skill.Body) == "" || strings.ContainsAny(skill.Description, "\r\n") {
			return errors.New("invalid frozen Dream skill catalog")
		}
		seen[skill.Name] = true
	}
	for _, skill := range skills {
		folder := filepath.Join(dir, memory.SkillsDir, skill.Name)
		if err := os.MkdirAll(folder, 0700); err != nil {
			return err
		}
		// Match the product's small front-matter reader: it decodes only
		// quotes/backslashes, not JSON's HTML, control or Unicode escapes.
		description := `"` + strings.ReplaceAll(strings.ReplaceAll(skill.Description, `\`, `\\`), `"`, `\"`) + `"`
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", skill.Name, description, skill.Body)
		if err := os.WriteFile(filepath.Join(folder, memory.SkillFile), []byte(body), 0600); err != nil {
			return err
		}
		actual, err := memory.New(dir, config.DefaultMemoryCharLimit).ReadSkill(skill.Name)
		if err != nil {
			return err
		}
		if actual.Name != skill.Name || actual.Description != skill.Description || actual.Body != skill.Body {
			return errors.New("frozen Dream skill catalog did not round-trip through the product reader")
		}
	}
	return nil
}

type callUsage struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Prompt     int    `json:"prompt_tokens"`
	Completion int    `json:"completion_tokens"`
	Total      int    `json:"total_tokens"`
	Cached     int    `json:"cached_tokens"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type dreamResult struct {
	Proposal     experiment.DreamProposal `json:"proposal"`
	Notes        []string                 `json:"notes"`
	Calls        []callUsage              `json:"calls"`
	CostComplete bool                     `json:"cost_complete"`
	Error        string                   `json:"error,omitempty"`
	FailureKind  string                   `json:"failure_kind,omitempty"`
	DurationMS   int64                    `json:"duration_ms"`
	ModelInput   json.RawMessage          `json:"model_input,omitempty"`
	ModelOutput  *schema.Message          `json:"model_output,omitempty"`
}

// Capture before parsing so a refused or malformed proposal remains auditable.
// Embedding delegates unused Stream; Dream deliberately uses one Generate call.
type dreamAuditModel struct {
	model.BaseChatModel
	result *dreamResult
}

func (m *dreamAuditModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	m.result.ModelInput = raw
	output, err := m.BaseChatModel.Generate(ctx, input, opts...)
	m.result.ModelOutput = output
	return output, err
}

func readInput(path string) (dreamInput, error) {
	var input dreamInput
	raw, err := os.ReadFile(path)
	if err != nil {
		return input, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return input, errors.New("expected one input object")
	}
	if input.ProjectID == "" || input.Cutoff.IsZero() || input.CharLimit < 0 || input.EntryMax < 0 {
		return input, errors.New("invalid Dream scope or limits")
	}
	if input.CharLimit == 0 {
		input.CharLimit = config.DefaultMemoryCharLimit
	}
	if input.EntryMax == 0 {
		input.EntryMax = config.DefaultMemoryEntryMax
	}
	if input.EntryMax > input.CharLimit {
		return input, errors.New("entry cap exceeds total budget")
	}
	for _, note := range input.Notes {
		if strings.TrimSpace(note) != note || note == "" || strings.Contains(strings.ReplaceAll(note, "\r\n", "\n"), "\n\n") {
			return input, errors.New("frozen notes must be nonempty canonical single entries")
		}
	}
	return input, nil
}

func loadProvider(o options) (*config.Config, string, error) {
	raw, err := os.ReadFile(o.config)
	if err != nil {
		return nil, "", err
	}
	cfg := config.Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, "", err
	}
	selected, ok := cfg.Provider(o.provider)
	if !ok {
		return nil, "", errors.New("selected provider absent")
	}
	if o.model != "" {
		selected.Model = o.model
	}
	selected.BuiltinTools = nil
	cfg.Models.Default = o.provider
	cfg.Models.Providers = []config.Provider{selected}
	return cfg, selected.APIKey, nil
}

func saveJSON(path string, value any, secret string) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if secret != "" {
		// Decode only our successful encoding, preserving numeric spelling. Raw
		// replacement would miss escaped keys or corrupt JSON with a short key.
		var native any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		// The bytes were just encoded above; the resulting JSON-native values
		// (including decoder-validated numbers) cannot introduce marshal errors.
		_ = decoder.Decode(&native)
		raw, _ = json.MarshalIndent(redactValues(native, secret), "", "  ")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		return errors.Join(err, file.Close())
	}
	_, err = file.Write(append(raw, '\n'))
	return errors.Join(err, file.Close())
}

func redactValues(value any, secret string) any {
	switch v := value.(type) {
	case string:
		return strings.ReplaceAll(v, secret, "<redacted>")
	case map[string]any:
		for key, item := range v {
			v[key] = redactValues(item, secret)
		}
	case []any:
		for i, item := range v {
			v[i] = redactValues(item, secret)
		}
	}
	return value
}

func runDream(ctx context.Context, o options) error {
	input, err := readInput(o.input)
	if err != nil {
		return err
	}
	cfg, secret, err := loadProvider(o)
	if err != nil {
		return err
	}
	if err := os.Mkdir(o.out, 0700); err != nil {
		return err
	}
	target := memory.NewLimited(filepath.Join(o.out, "memory"), input.CharLimit, input.EntryMax)
	if err := seedDreamSkills(target.Dir(), input.Skills); err != nil {
		return err
	}
	snap, err := target.Overwrite(strings.Join(input.Notes, "\n\n"))
	if err != nil {
		return err
	}
	opts := experiment.DreamOptions{ProjectID: input.ProjectID, Cutoff: input.Cutoff, MaxSourceChars: 24000, MaxChanges: 16, EntryMax: input.EntryMax}
	result := dreamResult{Notes: append([]string{}, snap.Entries...), Calls: []callUsage{}, CostComplete: true}
	started := time.Now()
	var mu sync.Mutex
	record := func(call provider.CallRecord) {
		if call.Backoff != nil {
			return
		}
		entry := callUsage{ProviderID: call.ProviderID, Model: call.Model, Prompt: call.PromptTokens, Completion: call.CompletionTokens, Total: call.TotalTokens, Cached: call.CachedTokens, DurationMS: call.Duration.Milliseconds()}
		if call.Err != nil {
			entry.Error = call.Err.Error()
		}
		mu.Lock()
		defer mu.Unlock()
		result.Calls = append(result.Calls, entry)
		if call.Err != nil || call.TotalTokens <= 0 {
			result.CostComplete = false
		}
	}
	// The experiment owns its single request: no shared cooldown/header
	// replay or compatibility retry may silently spend a second HTTP call.
	ctx, cancel := context.WithTimeout(provider.WithIsolatedRecovery(ctx), time.Duration(o.timeout*float64(time.Second)))
	defer cancel()
	build, runErr := provider.New(cfg).ModelBuilder(ctx, o.provider, o.model, "", record)
	if runErr == nil {
		chat := &dreamAuditModel{BaseChatModel: provider.WithoutBuiltinTools(build("memory-dream", "memory-dream")), result: &result}
		result.Proposal, runErr = experiment.GenerateDream(ctx, chat, snap, input.Sources, opts)
	}
	if runErr == nil {
		var final memory.Snapshot
		final, _, runErr = experiment.ApplyDream(target, result.Proposal, input.Sources, opts)
		if runErr == nil {
			result.Notes = append([]string{}, final.Entries...)
		}
	}
	result.DurationMS = time.Since(started).Milliseconds()
	if runErr != nil {
		result.Error = runErr.Error()
		switch {
		case errors.Is(runErr, experiment.ErrDreamOutputLimit):
			result.FailureKind = "output_limit"
		case result.CostComplete && len(result.Calls) > 0:
			result.FailureKind = "proposal_invalid"
		default:
			result.FailureKind = "input_or_provider_error"
		}
	}
	if len(result.Calls) == 0 {
		result.CostComplete = false
	}
	if err := saveJSON(filepath.Join(o.out, "dream.json"), result, secret); err != nil {
		return err
	}
	if runErr != nil {
		return errors.New("Dream failed; see private dream.json")
	}
	return nil
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parseOptions(args, stderr)
	if err == nil {
		if o.command == "serve" {
			var server *mcp.Server
			server, err = recallServer(o.memoryDir, o.limit, o.maxChars)
			if err == nil {
				err = server.Run(ctx, &mcp.StdioTransport{})
			}
		} else {
			err = runDream(ctx, o)
			if err == nil {
				fmt.Fprintln(stdout, filepath.Join(o.out, "dream.json"))
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
