package taskpanel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func definition() Definition {
	return Definition{ID: "test-panel", Version: "1", Name: "Test", Entry: "test/index.html", Schema: json.RawMessage(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer"}}}`), InitialState: json.RawMessage(`{"count":0}`)}
}
func update() Update {
	return Update{ProtocolVersion: 1, PanelID: "p", OperationID: "op", BaseRevision: 1, Kind: "snapshot", State: json.RawMessage(`{"count":1}`)}
}
func TestDefinitionRefusesUnsafeEntryAndInvalidState(t *testing.T) {
	good := definition()
	if err := ValidateDefinition(good); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"", "../a", "/a", "a/../b", "a\\b", "a?x", "a#x"} {
		t.Run(entry, func(t *testing.T) {
			d := good
			d.Entry = entry
			if !errors.Is(ValidateDefinition(d), ErrInvalid) {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
	for _, id := range []string{"", strings.Repeat("a", 129), "a/b", "a\\b"} {
		d := good
		d.ID = id
		if !errors.Is(ValidateDefinition(d), ErrInvalid) {
			t.Fatal("invalid id accepted")
		}
	}
	d := good
	d.Version = ""
	if ValidateDefinition(d) == nil {
		t.Fatal("empty version")
	}
	d = good
	d.Name = ""
	if ValidateDefinition(d) == nil {
		t.Fatal("empty name")
	}
	d = good
	d.Description = strings.Repeat("x", MaxDefinitionBytes)
	if ValidateDefinition(d) == nil {
		t.Fatal("size")
	}
	d = good
	d.Schema = json.RawMessage(`{"type":"object"}`)
	d.InitialState = nil
	if err := ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
}
func TestStateSchemaAndSizeAreEnforced(t *testing.T) {
	d := definition()
	for _, state := range []json.RawMessage{nil, []byte(`null`), []byte(`[]`), []byte(`{"count":"x"}`), []byte(`{}`), []byte(strings.Repeat(" ", MaxStateBytes+1))} {
		if !errors.Is(ValidateState(d, state), ErrInvalid) {
			t.Fatalf("state accepted (%d bytes)", len(state))
		}
	}
	for _, schema := range []string{"{", `{"$ref":"https://invalid.example/schema"}`, `{"type":"unknown"}`} {
		d.Schema = []byte(schema)
		if !errors.Is(ValidateState(d, []byte(`{"count":1}`)), ErrInvalid) {
			t.Fatal("schema accepted")
		}
	}
}
func TestSnapshotAndPatchKeepEquivalentState(t *testing.T) {
	d := definition()
	old := json.RawMessage(`{"count":0,"items":{"a":{"v":1}}}`)
	u := update()
	u.Kind = "patch"
	u.State = nil
	u.Operations = json.RawMessage(`[{"op":"replace","path":"/count","value":1},{"op":"copy","from":"/items/a","path":"/items/b"},{"op":"move","from":"/items/b","path":"/items/c"},{"op":"test","path":"/count","value":1},{"op":"add","path":"/done","value":true},{"op":"remove","path":"/items/c"}]`)
	result, err := Apply(d, old, u)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != `{"count":0,"items":{"a":{"v":1}}}` {
		t.Fatal("input mutated")
	}
	u = update()
	u.State = result
	snapshot, err := Apply(d, old, u)
	if err != nil || string(snapshot) != string(result) {
		t.Fatalf("snapshot=%s err=%v", snapshot, err)
	}
}
func TestInvalidUpdatesDoNotPartiallyApply(t *testing.T) {
	d := definition()
	old := d.InitialState
	cases := []Update{update(), update(), update(), update(), update(), update(), update(), update(), update(), update(), update(), update()}
	cases[0].ProtocolVersion = 2
	cases[1].PanelID = ""
	cases[2].OperationID = ""
	cases[3].OperationID = strings.Repeat("x", 129)
	cases[4].BaseRevision = -1
	cases[5].State = nil
	cases[6].Operations = []byte(`[]`)
	cases[7].Kind = "other"
	cases[8].State = []byte(`{`)
	cases[9].State = []byte(`{"count":"no"}`)
	cases[10].State = []byte(`{"x":"` + strings.Repeat("x", MaxUpdateBytes) + `"}`)
	cases[11].Kind = "patch"
	cases[11].State = nil
	cases[11].Operations = []byte(`[{}]`)
	for i, u := range cases {
		if _, err := Apply(d, old, u); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	for _, ops := range []string{"null", "{}", "[", `[{"op":"add","path":"/count","value":2},{"op":"test","path":"/count","value":9}]`, `[{"op":"bogus","path":"/count"}]`, `[{"op":"remove","path":"/missing"}]`, `[` + strings.TrimSuffix(strings.Repeat(`{"op":"test","path":"/count","value":0},`, MaxPatchOperations+1), ",") + `]`} {
		u := update()
		u.Kind = "patch"
		u.State = nil
		u.Operations = []byte(ops)
		if _, err := Apply(d, old, u); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ops %.100s: %v", ops, err)
		}
	}
	u := update()
	u.Kind = "patch"
	u.State = nil
	if _, err := Apply(d, old, u); err == nil {
		t.Fatal("missing operations")
	}
}
func TestOperationDigestPreservesIntegerIdentity(t *testing.T) {
	a, e := Digest(json.RawMessage(`{"n":9007199254740992}`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := Digest(json.RawMessage(`{"n":9007199254740993}`))
	if e != nil || a == b {
		t.Fatal("large integer identity lost")
	}
	c, _ := Digest(json.RawMessage("{\n \"n\":9007199254740992}"))
	if a != c {
		t.Fatal("formatting changed identity")
	}
	if _, err := Digest(make(chan int)); err == nil {
		t.Fatal("unencodable input")
	}
	if _, err := Digest(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid raw JSON")
	}
}

func TestPatchRejectsCorruptPreviousStateWithoutPanicking(t *testing.T) {
	u := update()
	u.Kind = "patch"
	u.State = nil
	u.Operations = []byte(`[]`)
	for _, old := range []json.RawMessage{nil, []byte(`{`), []byte(`null`)} {
		if _, e := Apply(definition(), old, u); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}

func TestPatchUsesStandardPointersAcrossHostAndPlugin(t *testing.T) {
	d := definition()
	old := json.RawMessage(`{"count":0,"items":[1,2],"a/b~c":1}`)
	for _, ops := range []string{`[null]`, `[{"op":3,"path":"/count"}]`, `[{"op":"add","path":3,"value":1}]`, `[{"op":"add","path":"invalid","value":1}]`, `[{"op":"add","path":"/a~2","value":1}]`, `[{"op":"add","path":"/a~","value":1}]`, `[{"op":"add","path":"/count"}]`, `[{"op":"copy","path":"/c"}]`, `[{"op":"move","from":"/items","path":"/items/x"}]`, `[{"op":"remove","path":"/items/-1"}]`, `[{"op":"remove","path":"/items/01"}]`, `[{"op":"remove","path":"/items/"}]`, `[{"op":"copy","from":"/items/-","path":"/copy"}]`, `[{"op":"remove","path":"/items/99999999999999999999999999999999"}]`, `[{"op":"add","path":"/count/x","value":1}]`, `[{"op":"replace","path":"/missing/x","value":1}]`, `[{"op":"remove","path":"/items/999"}]`} {
		u := update()
		u.Kind = "patch"
		u.State = nil
		u.Operations = []byte(ops)
		if _, e := Apply(d, old, u); !errors.Is(e, ErrInvalid) {
			t.Fatalf("invalid patch accepted: %s %v", ops, e)
		}
	}
	u := update()
	u.Kind = "patch"
	u.State = nil
	u.Operations = []byte(`[{"op":"add","path":"/items/-","value":3},{"op":"copy","from":"/items/0","path":"/items/3"},{"op":"replace","path":"/a~1b~0c","value":2}]`)
	if _, e := Apply(d, old, u); e != nil {
		t.Fatal(e)
	}
	if e := validatePointer([]byte(`{`), "/x", false); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if e := validateOperation(old, []byte(`{`)); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
