// Package taskpanel defines the versioned, presentation-independent task panel protocol.
package taskpanel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/google/jsonschema-go/jsonschema"
)

// ProtocolVersion is the supported message protocol major version.
const ProtocolVersion = 1

// Limits bound parsing and allocation independently of a producer's own result budget.
const (
	MaxUpdateBytes     = 64 << 20
	MaxStateBytes      = 64 << 20
	MaxDefinitionBytes = 64 << 10
	MaxPatchOperations = 256
)

// Limits carries a current host budget; zero leaves the protocol ceiling active.
type Limits struct {
	MaxStateBytes       int
	MaxUpdateBytes      int
	ValidateStateChange func(Definition, json.RawMessage, json.RawMessage) error
}

// Errors preserve input, optimistic concurrency and missing-owner categories.
var (
	ErrInvalid  = errors.New("task panel: invalid input")
	ErrConflict = errors.New("task panel: revision or operation conflict")
	ErrNotFound = errors.New("task panel: not found")
)

// SourceDefinition advertises an optional registered producer and its input contract.
type SourceDefinition struct {
	ID          string          `json:"id"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// RendererNative marks a definition drawn by a bundled application component.
const RendererNative = "native"

// Definition is a bundled plugin's immutable code and state contract.
type Definition struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Entry       string `json:"entry"`
	// Renderer "native" names a first-party component bundled with the app
	// instead of an iframe entry. Only repository code can register one.
	Renderer     string            `json:"renderer,omitempty"`
	Schema       json.RawMessage   `json:"schema"`
	InitialState json.RawMessage   `json:"initial_state,omitempty"`
	Source       *SourceDefinition `json:"source,omitempty"`
}

// Source is supplied by the trusted runtime, never by a producer's JSON arguments.
type Source struct {
	ThreadID   string `json:"thread_id"`
	TurnID     string `json:"turn_id"`
	AgentID    string `json:"agent_id"`
	ToolCallID string `json:"tool_call_id"`
	Generation int    `json:"generation,omitempty"`
}

// Panel is a durable instance scoped to one conversation.
type Panel struct {
	ID            string          `json:"id"`
	ThreadID      string          `json:"thread_id"`
	PluginID      string          `json:"plugin_id"`
	PluginVersion string          `json:"plugin_version"`
	Revision      int64           `json:"revision"`
	State         json.RawMessage `json:"state"`
	Definition    Definition      `json:"definition"`
	Source        Source          `json:"source"`
	SourceStatus  string          `json:"source_status,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Update replaces a complete state or applies RFC 6902 operations to a known version.
type Update struct {
	ProtocolVersion int             `json:"protocol_version"`
	PanelID         string          `json:"panel_id"`
	OperationID     string          `json:"operation_id"`
	BaseRevision    int64           `json:"base_revision"`
	Kind            string          `json:"kind"`
	State           json.RawMessage `json:"state,omitempty"`
	Operations      json.RawMessage `json:"operations,omitempty"`
}

// Change is a durable ordered delivery record. Its cursor is independent of chat seq.
type Change struct {
	SourceStatus string          `json:"source_status,omitempty"`
	Cursor       int64           `json:"cursor"`
	ThreadID     string          `json:"thread_id"`
	PanelID      string          `json:"panel_id"`
	Revision     int64           `json:"revision"`
	BaseRevision int64           `json:"base_revision"`
	Kind         string          `json:"kind"`
	State        json.RawMessage `json:"state,omitempty"`
	Operations   json.RawMessage `json:"operations,omitempty"`
}

// DefinitionsMatch compares pinned contracts independently of JSON key order.
func DefinitionsMatch(a, b Definition) bool {
	aDigest, aErr := Digest(a)
	bDigest, bErr := Digest(b)
	return aErr == nil && bErr == nil && aDigest == bDigest
}

// ValidateDefinition refuses invalid state contracts and executable path ambiguity.
func ValidateDefinition(d Definition) error {
	if d.Renderer != "" && d.Renderer != RendererNative || d.Renderer == RendererNative && d.Entry != "" {
		return fmt.Errorf("%w: definition renderer", ErrInvalid)
	}
	if d.ID == "" || len(d.ID) > 128 || strings.ContainsAny(d.ID, "/\\\x00") || d.Version == "" || d.Name == "" || d.Renderer == "" && (d.Entry == "" || path.IsAbs(d.Entry) || path.Clean(d.Entry) != d.Entry || strings.HasPrefix(d.Entry, "../") || strings.ContainsAny(d.Entry, "\\?#\x00")) {
		return fmt.Errorf("%w: definition identity or entry", ErrInvalid)
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > MaxDefinitionBytes {
		return fmt.Errorf("%w: definition size", ErrInvalid)
	}
	initial := d.InitialState
	if len(initial) == 0 {
		initial = json.RawMessage(`{}`)
	}
	return ValidateState(d, initial)
}

// ValidateState checks a complete state without remote schema loading.
func ValidateState(d Definition, state json.RawMessage) error {
	if len(state) > MaxStateBytes {
		return fmt.Errorf("%w: state size", ErrInvalid)
	}
	var value map[string]any
	if err := json.Unmarshal(state, &value); err != nil || value == nil {
		return fmt.Errorf("%w: state must be an object", ErrInvalid)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(d.Schema, &schema); err != nil {
		return fmt.Errorf("%w: schema: %v", ErrInvalid, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("%w: resolve schema: %v", ErrInvalid, err)
	}
	if err = resolved.Validate(value); err != nil {
		return fmt.Errorf("%w: state schema: %v", ErrInvalid, err)
	}
	return nil
}

// Apply validates and applies a bounded update without modifying its old state.
func Apply(d Definition, old json.RawMessage, u Update) (json.RawMessage, error) {
	if u.ProtocolVersion != ProtocolVersion || u.PanelID == "" || u.OperationID == "" || len(u.OperationID) > 128 || u.BaseRevision < 0 {
		return nil, fmt.Errorf("%w: update envelope", ErrInvalid)
	}
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > MaxUpdateBytes {
		return nil, fmt.Errorf("%w: update size", ErrInvalid)
	}
	var next []byte
	switch u.Kind {
	case "snapshot":
		if len(u.State) == 0 || len(u.Operations) > 0 {
			return nil, fmt.Errorf("%w: snapshot body", ErrInvalid)
		}
		next = bytes.Clone(u.State)
	case "patch":
		if len(u.State) > 0 || len(u.Operations) == 0 {
			return nil, fmt.Errorf("%w: patch body", ErrInvalid)
		}
		var ops []json.RawMessage
		if err = json.Unmarshal(u.Operations, &ops); err != nil || ops == nil || len(ops) > MaxPatchOperations {
			return nil, fmt.Errorf("%w: patch operations", ErrInvalid)
		}
		if err := ValidateState(d, old); err != nil {
			return nil, err
		}
		next = bytes.Clone(old)
		for _, op := range ops {
			if err := validateOperation(next, op); err != nil {
				return nil, err
			}
			// Apply individually so one copy cannot cause unbounded intermediate state.
			patch, decodeErr := jsonpatch.DecodePatch(append(append([]byte{'['}, op...), ']'))
			if decodeErr != nil {
				return nil, fmt.Errorf("%w: patch: %v", ErrInvalid, decodeErr)
			}
			next, err = patch.Apply(next)
			if err != nil {
				return nil, fmt.Errorf("%w: patch: %v", ErrInvalid, err)
			}
			if len(next) > MaxStateBytes {
				return nil, fmt.Errorf("%w: state size", ErrInvalid)
			}
		}
	default:
		return nil, fmt.Errorf("%w: update kind", ErrInvalid)
	}
	if err = ValidateState(d, next); err != nil {
		return nil, err
	}
	return next, nil
}

// Digest makes idempotency compare canonical JSON rather than formatting.
func Digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&normalized); err != nil {
		return "", err
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
