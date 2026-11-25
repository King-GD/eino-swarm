package memoryexperiment

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type dreamTestModel struct {
	calls    int
	inputs   []*schema.Message
	generate func(context.Context) (*schema.Message, error)
}

func (m *dreamTestModel) Generate(ctx context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	m.inputs = messages
	return m.generate(ctx)
}
func (m *dreamTestModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("dream candidate uses Generate only")
}

// These are actual Generate input/output boundary checks, without a network or
// a hand-written invocation of the validator standing in for model admission.
func TestDreamModelRequestReceivesOnlyFrozenSourcesAndProducesAnUnwrittenCandidate(t *testing.T) {
	store, notes, sources, options, proposal := dreamFixture(t)
	encoded, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	fake := &dreamTestModel{generate: func(context.Context) (*schema.Message, error) {
		return schema.AssistantMessage(string(encoded), nil), nil
	}}
	got, err := GenerateDream(context.Background(), fake, notes, sources, options)
	if err != nil || !reflect.DeepEqual(got, proposal) || fake.calls != 1 || len(fake.inputs) != 2 {
		t.Fatalf("generation: %+v %v calls=%d", got, err, fake.calls)
	}
	if fake.inputs[0].Role != schema.System || fake.inputs[1].Role != schema.User || !strings.Contains(fake.inputs[1].Content, sources[0].Text) {
		t.Fatalf("model inputs: %+v", fake.inputs)
	}
	after, err := store.Read()
	if err != nil || after.Rev != notes.Rev {
		t.Fatalf("model response wrote source: %+v %v", after, err)
	}
}

func TestDreamModelNeverRunsForFutureSourcesOrPreCancelledRequests(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	fake := &dreamTestModel{generate: func(context.Context) (*schema.Message, error) {
		t.Fatal("invalid input reached model")
		return nil, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GenerateDream(ctx, fake, notes, sources, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	sources[0].ObservedAt = options.Cutoff.Add(time.Second)
	if _, err := GenerateDream(context.Background(), fake, notes, sources, options); err == nil {
		t.Fatal("future source admitted")
	}
	if fake.calls != 0 {
		t.Fatalf("model called %d times", fake.calls)
	}
	sources[0].ObservedAt = options.Cutoff.Add(-time.Second)
	if _, err := GenerateDream(context.Background(), nil, notes, sources, options); err == nil {
		t.Fatal("nil model admitted")
	}
}

func TestDreamDoesNotRetryOrAcceptToolCallsAndMalformedResponses(t *testing.T) {
	_, notes, sources, options, proposal := dreamFixture(t)
	encoded, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(context.Context) (*schema.Message, error){
		"model error":  func(context.Context) (*schema.Message, error) { return nil, errors.New("transport failure") },
		"nil response": func(context.Context) (*schema.Message, error) { return nil, nil },
		"wrong role":   func(context.Context) (*schema.Message, error) { return schema.UserMessage(string(encoded)), nil },
		"tool call": func(context.Context) (*schema.Message, error) {
			return schema.AssistantMessage(string(encoded), []schema.ToolCall{{ID: "call"}}), nil
		},
		"malformed JSON": func(context.Context) (*schema.Message, error) { return schema.AssistantMessage("not JSON", nil), nil },
		"unsupported proposal": func(context.Context) (*schema.Message, error) {
			return schema.AssistantMessage(`{"revision":"invented","changes":[]}`, nil), nil
		},
	}
	for name, generate := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &dreamTestModel{generate: generate}
			if _, err := GenerateDream(context.Background(), fake, notes, sources, options); err == nil || fake.calls != 1 {
				t.Fatalf("response admission err=%v calls=%d", err, fake.calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	fake := &dreamTestModel{generate: func(context.Context) (*schema.Message, error) {
		cancel()
		return schema.AssistantMessage(string(encoded), nil), nil
	}}
	if _, err := GenerateDream(ctx, fake, notes, sources, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation=%v", err)
	}
}

func TestDreamProposalDecoderRefusesUnknownDuplicateAndTrailingJSON(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":        `{"revision":"r","changes":[],"extra":true}`,
		"unknown nested field": `{"revision":"r","changes":[{"action":"add","extra":true}]}`,
		"duplicate revision":   `{"revision":"a","revision":"b","changes":[]}`,
		"duplicate operation":  `{"revision":"r","changes":[{"action":"add","action":"remove"}]}`,
		"duplicate citation":   `{"revision":"r","changes":[{"evidence":[{"seq":1,"seq":2}]}]}`,
		"extra JSON":           `{"revision":"r","changes":[]} {}`,
		"trailing garbage":     `{"revision":"r","changes":[]} nope`,
		"wrong root type":      `[]`,
		"wrong field type":     `{"revision":4,"changes":[]}`,
		"truncated object":     `{"revision":`,
		"truncated array":      `{"changes":[`,
		"broken key":           `{1:2}`,
		"empty":                ``,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeDreamProposal(body); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
	if _, err := decodeDreamProposal(`{"revision":"r","changes":[]}`); err != nil {
		t.Fatal(err)
	}
}
