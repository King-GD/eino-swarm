package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
)

func TestPanelPatchPreservesKeyedObjectsAndEscapesPaths(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"assets":{"a/b~c":{"status":"running"},"kept":{"x":2}},"obsolete":1}`, `{"assets":{"a/b~c":{"status":"responded"},"kept":{"x":2},"new":{"x":3}},"added":true}`},
		{`{"a":[1,2],"nullable":null}`, `{"a":[3],"nullable":{}}`},
		{`{"same":1}`, `{"same":1}`},
	} {
		raw, err := panelStatePatch([]byte(pair[0]), []byte(pair[1]))
		if err != nil {
			t.Fatal(err)
		}
		patch, err := jsonpatch.DecodePatch(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := patch.Apply([]byte(pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal([]byte(pair[1]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("patch=%s got=%s", raw, got)
		}
	}
	for _, pair := range [][2]string{{`{`, `{}`}, {`{}`, `{`}, {`{} {}`, `{}`}} {
		if _, err := panelStatePatch([]byte(pair[0]), []byte(pair[1])); err == nil {
			t.Fatal("accepted broken state")
		}
	}
	precise, err := panelStatePatch([]byte(`{"n":9007199254740992}`), []byte(`{"n":9007199254740993}`))
	if err != nil || !strings.Contains(string(precise), "9007199254740993") {
		t.Fatalf("large integer update lost: %s %v", precise, err)
	}
}
