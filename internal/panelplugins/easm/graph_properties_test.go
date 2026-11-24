package easm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// A sequence of edits must preserve saved evidence through both publication
// formats. The generator retains old detached records: requiring all stored
// nodes to be rooted would hide the compatibility boundary this rule protects.
func FuzzPortraitPublicationKeepsSavedSources(f *testing.F) {
	for edit := byte(0); edit < 11; edit++ {
		f.Add([]byte{edit, 3, 1, 4}, []byte{edit})
	}
	f.Add([]byte{1, 2, 3}, []byte{2, 4}) // Deleting first, then reparenting cannot bypass retention.
	f.Add([]byte{1, 2, 3}, []byte{0, 1, 6, 9, 2, 7, 6, 8})
	f.Add([]byte{0}, []byte{1, 10, 0, 10}) // Rejected downgrades cannot erase an earlier confirmation.
	f.Fuzz(func(t *testing.T, shape, edits []byte) {
		if len(edits) == 0 {
			return
		}
		if len(shape) > 16 {
			shape = shape[:16]
		}
		if len(edits) > 16 {
			edits = edits[:16]
		}
		definition := catalogDefinition(t)
		before := generatedPortrait(shape)
		if err := ValidateGraphUpdate(stateJSON(t, before), stateJSON(t, before)); err != nil {
			t.Fatalf("generated saved state cannot be retained: %v", err)
		}
		for step, edit := range edits {
			previous := stateJSON(t, before)
			original := bytes.Clone(previous)
			candidate, wantAllowed := portraitEdit(t, before, edit, step)
			next := stateJSON(t, candidate)
			operations, err := json.Marshal([]map[string]any{
				{"op": "replace", "path": "/title", "value": candidate.Title},
				{"op": "replace", "path": "/nodes", "value": candidate.Nodes},
				{"op": "replace", "path": "/edges", "value": candidate.Edges},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, update := range []taskpanel.Update{
				{Kind: "snapshot", State: next},
				{Kind: "patch", Operations: operations},
			} {
				update.ProtocolVersion = taskpanel.ProtocolVersion
				update.PanelID, update.OperationID = "portrait", "generated-transition"
				applied, err := taskpanel.Apply(definition, previous, update)
				if err != nil {
					t.Fatalf("edit %d/%d must pass the catalog schema via %s: %v", step, edit, update.Kind, err)
				}
				gotDigest, err := taskpanel.Digest(applied)
				wantDigest, wantErr := taskpanel.Digest(next)
				if err != nil || wantErr != nil || gotDigest != wantDigest {
					t.Fatalf("%s changed the candidate state: %v/%v", update.Kind, err, wantErr)
				}
				validationErr := ValidateGraphUpdate(previous, applied)
				if (validationErr == nil) != wantAllowed {
					t.Fatalf("edit %d/%d via %s: allowed=%v, want %v: %v\nbefore=%s\nafter=%s",
						step, edit, update.Kind, validationErr == nil, wantAllowed, validationErr, previous, applied)
				}
				if !bytes.Equal(previous, original) {
					t.Fatal("validating a publication changed its saved input")
				}
			}
			// Rejected edits never become the base of the next publication. In
			// particular, a failed deletion cannot prepare a later reparenting.
			if wantAllowed {
				before = candidate
			}
		}
	})
}

func generatedPortrait(shape []byte) State {
	state := State{
		SchemaVersion: 1, Title: "Subject",
		Collection: Collection{Status: statusIdle, Steps: []StepStatus{}},
		Nodes:      []Node{{ID: RootID, Kind: "root", Label: "Subject", Detail: &Detail{RegistryID: "subject-registry", RegistryName: "Subject"}}},
		Edges:      []Edge{}, Heat: []HeatPoint{}, Components: []Component{},
	}
	for i := 0; i < 1+len(shape)%8; i++ {
		parent := 0
		if len(shape) != 0 {
			parent = int(shape[i%len(shape)]) % len(state.Nodes)
		}
		node := Node{ID: fmt.Sprintf("company:clue-%d", i), Kind: "company", Label: fmt.Sprintf("Clue %d", i)}
		confirmed := len(shape) != 0 && shape[i%len(shape)]&2 != 0
		state.Edges = append(state.Edges, Edge{Source: state.Nodes[parent].ID, Target: node.ID, Confirmed: confirmed})
		state.Nodes = append(state.Nodes, node)
	}
	state.Nodes = append(state.Nodes, Node{ID: "company:unsourced", Kind: "company", Label: "Unsourced"})
	if len(shape) != 0 && shape[0]%2 != 0 {
		state.Nodes = append(state.Nodes,
			Node{ID: "company:detached-a", Kind: "company", Label: "Detached A"},
			Node{ID: "company:detached-b", Kind: "company", Label: "Detached B"},
			Node{ID: "company:missing-parent", Kind: "company", Label: "Missing parent"})
		confirmed := shape[0]&4 != 0
		state.Edges = append(state.Edges,
			Edge{Source: "company:detached-a", Target: "company:detached-b", Confirmed: confirmed},
			Edge{Source: "company:detached-b", Target: "company:detached-a", Confirmed: confirmed},
			Edge{Source: "company:absent", Target: "company:missing-parent", Confirmed: confirmed})
	}
	return state
}

func portraitEdit(t *testing.T, before State, edit byte, step int) (State, bool) {
	t.Helper()
	var after State
	if err := json.Unmarshal(stateJSON(t, before), &after); err != nil {
		t.Fatal(err)
	}
	root := slices.IndexFunc(after.Nodes, func(node Node) bool { return node.ID == RootID })
	index := int(edit/11) % len(after.Edges)
	switch edit % 11 {
	case 0: // Adding detail does not change the clue's original source.
		after.Nodes[1].Detail = &Detail{Description: fmt.Sprintf("Observation %d", step)}
	case 1: // A rooted edge may upgrade confirmation without changing identity.
		after.Edges[0].Confirmed = true
	case 2:
		after.Edges = slices.Delete(after.Edges, index, index+1)
		return after, false
	case 3:
		target := after.Edges[index].Target
		after.Nodes = slices.DeleteFunc(after.Nodes, func(node Node) bool { return node.ID == target })
		after.Edges = slices.DeleteFunc(after.Edges, func(edge Edge) bool { return edge.Target == target || edge.Source == target })
		return after, false
	case 4:
		parent := Node{ID: fmt.Sprintf("company:new-parent-%d", step), Kind: "company", Label: "Another source"}
		after.Nodes = append(after.Nodes, parent)
		after.Edges = append(after.Edges, Edge{Source: RootID, Target: parent.ID})
		after.Edges[index].Source = parent.ID
		return after, false
	case 5:
		after.Nodes, after.Edges = []Node{}, []Edge{}
		return after, false
	case 6: // A previously unsourced clue can acquire its first rooted source.
		target := "company:unsourced"
		if slices.ContainsFunc(after.Edges, func(edge Edge) bool { return edge.Target == target }) ||
			!slices.ContainsFunc(after.Nodes, func(node Node) bool { return node.ID == target }) {
			target = fmt.Sprintf("company:new-clue-%d", step)
			after.Nodes = append(after.Nodes, Node{ID: target, Kind: "company", Label: "New clue"})
		}
		after.Edges = append(after.Edges, Edge{Source: RootID, Target: target})
	case 7: // A different registry starts a separate portrait, even under an alias.
		after = generatedPortrait(nil)
		after.Title = fmt.Sprintf("Another subject %d", step)
		after.Nodes[0].Label = after.Title
		after.Nodes[0].Detail.RegistryID = fmt.Sprintf("another-registry-%d", step)
	case 8: // A new label cannot erase sources while registry identity is unchanged.
		after.Title = fmt.Sprintf("Subject alias %d", step)
		after.Nodes[root].Label = after.Title
		after.Edges = slices.Delete(after.Edges, index, index+1)
		return after, false
	case 9:
		slices.Reverse(after.Nodes)
		// Keep the first edge rooted so a following confirmation edit remains legal.
		slices.Reverse(after.Edges[1:])
	case 10:
		wasConfirmed := after.Edges[index].Confirmed
		after.Edges[index].Confirmed = false
		return after, !wasConfirmed
	}
	return after, true
}
