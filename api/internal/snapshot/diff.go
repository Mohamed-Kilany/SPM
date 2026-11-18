package snapshot

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// PatchOperation is a single RFC 6902 JSON Patch operation.
type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// Diff computes an RFC 6902 JSON Patch document that transforms oldJSON into
// newJSON. Arrays are replaced wholesale so patch semantics stay stable and
// easy to verify. The document root is represented by the empty pointer "".
func Diff(oldJSON, newJSON []byte) ([]PatchOperation, error) {
	var oldDoc, newDoc interface{}
	if len(oldJSON) > 0 {
		if err := json.Unmarshal(oldJSON, &oldDoc); err != nil {
			return nil, fmt.Errorf("snapshot: decode old document: %w", err)
		}
	}
	if len(newJSON) > 0 {
		if err := json.Unmarshal(newJSON, &newDoc); err != nil {
			return nil, fmt.Errorf("snapshot: decode new document: %w", err)
		}
	}

	ops := make([]PatchOperation, 0, 8)
	diffValue("", oldDoc, newDoc, &ops)
	if len(ops) == 0 {
		return []PatchOperation{}, nil
	}
	return ops, nil
}

func (p PatchOperation) MarshalJSON() ([]byte, error) {
	if p.Op == "remove" {
		return json.Marshal(struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}{Op: p.Op, Path: p.Path})
	}
	return json.Marshal(struct {
		Op    string      `json:"op"`
		Path  string      `json:"path"`
		Value interface{} `json:"value"`
	}{Op: p.Op, Path: p.Path, Value: p.Value})
}

func diffValue(path string, oldVal, newVal interface{}, ops *[]PatchOperation) {
	if reflect.DeepEqual(oldVal, newVal) {
		return
	}

	oldMap, oldIsMap := oldVal.(map[string]interface{})
	newMap, newIsMap := newVal.(map[string]interface{})
	if oldIsMap && newIsMap {
		diffObject(path, oldMap, newMap, ops)
		return
	}

	*ops = append(*ops, PatchOperation{Op: "replace", Path: path, Value: newVal})
}

func diffObject(path string, oldMap, newMap map[string]interface{}, ops *[]PatchOperation) {
	for key, oldVal := range oldMap {
		newVal, ok := newMap[key]
		if !ok {
			*ops = append(*ops, PatchOperation{Op: "remove", Path: joinPointer(path, key)})
			continue
		}
		diffValue(joinPointer(path, key), oldVal, newVal, ops)
	}
	for key, newVal := range newMap {
		if _, ok := oldMap[key]; !ok {
			*ops = append(*ops, PatchOperation{Op: "add", Path: joinPointer(path, key), Value: newVal})
		}
	}
}

func joinPointer(parent, key string) string {
	return parent + "/" + escapePointerToken(key)
}

func escapePointerToken(key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")
	return key
}
