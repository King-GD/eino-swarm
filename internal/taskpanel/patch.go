package taskpanel

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// validateOperation excludes library extensions that a standard RFC 6902 view
// cannot replay, without mutating the dependency's process-global flags.
func validateOperation(document, raw []byte) error {
	var op map[string]json.RawMessage
	if err := json.Unmarshal(raw, &op); err != nil || op == nil {
		return fmt.Errorf("%w: patch operation object", ErrInvalid)
	}
	var kind, path string
	if err := json.Unmarshal(op["op"], &kind); err != nil {
		return fmt.Errorf("%w: patch operation name", ErrInvalid)
	}
	if err := json.Unmarshal(op["path"], &path); err != nil {
		return fmt.Errorf("%w: patch operation path", ErrInvalid)
	}
	appendAllowed := kind == "add" || kind == "copy" || kind == "move"
	if err := validatePointer(document, path, appendAllowed); err != nil {
		return err
	}
	if kind == "add" || kind == "replace" || kind == "test" {
		if _, ok := op["value"]; !ok {
			return fmt.Errorf("%w: patch operation value", ErrInvalid)
		}
	}
	if kind == "copy" || kind == "move" {
		var from string
		if err := json.Unmarshal(op["from"], &from); err != nil {
			return fmt.Errorf("%w: patch operation source", ErrInvalid)
		}
		if err := validatePointer(document, from, false); err != nil {
			return err
		}
		// A move cannot remove an ancestor before adding its child.
		if kind == "move" && strings.HasPrefix(path, from+"/") {
			return fmt.Errorf("%w: move into descendant", ErrInvalid)
		}
	}
	return nil
}

func validatePointer(document []byte, pointer string, appendAllowed bool) error {
	if pointer == "" {
		return nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("%w: JSON Pointer", ErrInvalid)
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			if i+1 == len(pointer) || (pointer[i+1] != '0' && pointer[i+1] != '1') {
				return fmt.Errorf("%w: JSON Pointer escape", ErrInvalid)
			}
			i++
		}
	}
	var current any
	if err := json.Unmarshal(document, &current); err != nil {
		return fmt.Errorf("%w: previous JSON", ErrInvalid)
	}
	tokens := strings.Split(pointer[1:], "/")
	for position, token := range tokens {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch value := current.(type) {
		case []any:
			if token == "-" && appendAllowed && position == len(tokens)-1 {
				return nil
			}
			if token == "" || (len(token) > 1 && token[0] == '0') {
				return fmt.Errorf("%w: array index", ErrInvalid)
			}
			for _, r := range token {
				if r < '0' || r > '9' {
					return fmt.Errorf("%w: array index", ErrInvalid)
				}
			}
			index, err := strconv.Atoi(token)
			if err != nil {
				return fmt.Errorf("%w: array index", ErrInvalid)
			}
			if index >= len(value) {
				return nil
			} // The patch library checks existence/bounds.
			current = value[index]
		case map[string]any:
			current = value[token]
		default:
			return nil // Required parent existence is checked by the executor.
		}
	}
	return nil
}
