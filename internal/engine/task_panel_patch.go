package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// panelStatePatch keeps keyed visual objects intact instead of replacing a
// whole asset map for each completed request. Arrays remain plugin-owned values.
func panelStatePatch(before, after json.RawMessage) (json.RawMessage, error) {
	var oldValue, newValue any
	decode := func(raw json.RawMessage, value *any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(value); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("panel state must contain one JSON value")
		}
		return nil
	}
	if err := decode(before, &oldValue); err != nil {
		return nil, err
	}
	if err := decode(after, &newValue); err != nil {
		return nil, err
	}
	ops := make([]map[string]any, 0)
	var visit func(string, any, any)
	visit = func(path string, old, new any) {
		if reflect.DeepEqual(old, new) {
			return
		}
		a, aok := old.(map[string]any)
		b, bok := new.(map[string]any)
		if !aok || !bok {
			ops = append(ops, map[string]any{"op": "replace", "path": path, "value": new})
			return
		}
		keys := make([]string, 0, len(a)+len(b))
		for k := range a {
			keys = append(keys, k)
		}
		for k := range b {
			if _, ok := a[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			av, existsA := a[key]
			bv, existsB := b[key]
			if !existsB {
				ops = append(ops, map[string]any{"op": "remove", "path": next})
			} else if !existsA {
				ops = append(ops, map[string]any{"op": "add", "path": next, "value": bv})
			} else {
				visit(next, av, bv)
			}
		}
	}
	visit("", oldValue, newValue)
	return json.Marshal(ops)
}
