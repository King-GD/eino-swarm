package engine

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestEventKindScanDistinguishesPanelAndOtherKindFields(t *testing.T) {
	source := `package engine
import (
 storage "github.com/LubyRuffy/eino-swarm/internal/store"
 panel "github.com/LubyRuffy/eino-swarm/internal/taskpanel"
 other "example.invalid/events"
)
const KindInternal="known_internal"
type LocalEvent=storage.Event
func example(){
 _=storage.Event{Kind:"unknown_stored_event"}
 _=&storage.Event{Kind:"pointer_event"}
 _=[]storage.Event{{Kind:"slice_event"}}
 _=[]*storage.Event{{Kind:"pointer_slice_event"}}
 _=map[string]storage.Event{"a":{Kind:"map_event"}}
 _=map[storage.Event]string{{Kind:"map_key_event"}:"a"}
 _=[]LocalEvent{{Kind:"alias_event"}}
 _=panel.Update{Kind:"patch"}
 _=panel.Change{Kind:"snapshot"}
 _=storage.TaskPanelChangeRow{Kind:"panel_row"}
 _=other.Event{Kind:"other_package_event"}
 _=struct{Kind string}{Kind:"unrelated_struct"}
 _=[]panel.Change{{Kind:"elided_panel"}}
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := eventKindsInFile(fset, file)
	for _, kind := range []string{"known_internal", "unknown_stored_event", "pointer_event", "slice_event", "pointer_slice_event", "map_event", "map_key_event", "alias_event"} {
		if _, ok := kinds[kind]; !ok {
			t.Errorf("missed persisted Event literal %q", kind)
		}
	}
	for _, kind := range []string{"patch", "snapshot", "panel_row", "other_package_event", "unrelated_struct", "elided_panel"} {
		if origin, ok := kinds[kind]; ok {
			t.Errorf("unrelated Kind classified as chat: %s %s", kind, origin)
		}
	}
}

func TestEventKindScanIncludesStoreLocalEventLiterals(t *testing.T) {
	for _, source := range []string{`package store
func example(){_=Event{Kind:"store_literal"};_=[]Event{{Kind:"store_slice"}}}`, `package engine
import . "github.com/LubyRuffy/eino-swarm/internal/store"
func example(){_=Event{Kind:"store_literal"};_=[]Event{{Kind:"store_slice"}}}`} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "fixture.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		kinds := eventKindsInFile(fset, file)
		for _, kind := range []string{"store_literal", "store_slice"} {
			if _, ok := kinds[kind]; !ok {
				t.Error("lost store kind", kind)
			}
		}
	}
}
