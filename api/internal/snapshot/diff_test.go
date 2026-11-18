package snapshot

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyPatchDocument(t *testing.T, oldDoc []byte, ops []PatchOperation) []byte {
	t.Helper()
	rawOps, err := json.Marshal(ops)
	require.NoError(t, err)

	var doc interface{}
	require.NoError(t, json.Unmarshal(oldDoc, &doc))

	var decoded []map[string]interface{}
	require.NoError(t, json.Unmarshal(rawOps, &decoded))
	for _, op := range decoded {
		switch op["op"] {
		case "add", "replace":
			setPatchValue(t, doc, op["path"].(string), op["value"])
		case "remove":
			removePatchValue(t, doc, op["path"].(string))
		default:
			t.Fatalf("unexpected op: %v", op)
		}
	}
	result, err := json.Marshal(doc)
	require.NoError(t, err)
	return result
}

func setPatchValue(t *testing.T, doc interface{}, path string, value interface{}) {
	t.Helper()
	if path == "" {
		// The caller replaces the root; this helper cannot reassign a caller's
		// interface, so root replacement is asserted directly in its test.
		t.Fatalf("root replacement is not supported by this helper")
	}
	parent, key := resolvePatchParent(t, doc, path)
	switch container := parent.(type) {
	case map[string]interface{}:
		container[key] = value
	default:
		t.Fatalf("unsupported parent for path %q", path)
	}
}

func removePatchValue(t *testing.T, doc interface{}, path string) {
	t.Helper()
	parent, key := resolvePatchParent(t, doc, path)
	container, ok := parent.(map[string]interface{})
	require.True(t, ok)
	delete(container, key)
}

func resolvePatchParent(t *testing.T, doc interface{}, path string) (interface{}, string) {
	t.Helper()
	require.Contains(t, path, "/")
	tokens := splitPointer(path)
	current := doc
	for _, token := range tokens[:len(tokens)-1] {
		m, ok := current.(map[string]interface{})
		require.True(t, ok)
		current = m[token]
	}
	return current, tokens[len(tokens)-1]
}

func splitPointer(path string) []string {
	var tokens []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			if i > start {
				tokens = append(tokens, unescapePointer(path[start:i]))
			}
			start = i + 1
		}
	}
	return tokens
}

func unescapePointer(token string) string {
	out := ""
	for i := 0; i < len(token); i++ {
		if token[i] == '~' && i+1 < len(token) {
			switch token[i+1] {
			case '0':
				out += "~"
				i++
				continue
			case '1':
				out += "/"
				i++
				continue
			}
		}
		out += string(token[i])
	}
	return out
}

func TestDiff_ObjectScalarsAndEmptyStrings(t *testing.T) {
	oldDoc := []byte(`{"name":"Board","description":"old","count":3,"draft":true}`)
	newDoc := []byte(`{"name":"","description":"new","count":4,"draft":false}`)

	ops, err := Diff(oldDoc, newDoc)
	require.NoError(t, err)
	require.Len(t, ops, 4)

	raw, err := json.Marshal(ops)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `{"op":"replace","path":"/name","value":""}`)
	assert.Contains(t, string(raw), `{"op":"replace","path":"/draft","value":false}`)

	assert.JSONEq(t, string(newDoc), string(applyPatchDocument(t, oldDoc, ops)))
}

func TestDiff_AddAndRemoveKeys(t *testing.T) {
	oldDoc := []byte(`{"a":1,"b":2}`)
	newDoc := []byte(`{"a":1,"c":3}`)

	ops, err := Diff(oldDoc, newDoc)
	require.NoError(t, err)
	require.Len(t, ops, 2)

	raw, err := json.Marshal(ops)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `{"op":"add","path":"/c","value":3}`)
	assert.Contains(t, string(raw), `{"op":"remove","path":"/b"}`)

	assert.JSONEq(t, string(newDoc), string(applyPatchDocument(t, oldDoc, ops)))
}

func TestDiff_ArraysAreReplacedWholesale(t *testing.T) {
	oldDoc := []byte(`{"tickets":[{"id":"a","n":1},{"id":"b","n":2}]}`)
	newDoc := []byte(`{"tickets":[{"id":"b","n":3}]}`)

	ops, err := Diff(oldDoc, newDoc)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "/tickets", ops[0].Path)
	assert.Equal(t, "replace", ops[0].Op)
	assert.JSONEq(t, string(newDoc), string(applyPatchDocument(t, oldDoc, ops)))
}

func TestDiff_RootReplacementUsesEmptyPointer(t *testing.T) {
	ops, err := Diff([]byte(`"old"`), []byte(`"new"`))
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "", ops[0].Path)
	assert.Equal(t, "replace", ops[0].Op)
	assert.Equal(t, "new", ops[0].Value)
}

func TestDiff_NoChanges(t *testing.T) {
	doc := []byte(`{"a":1,"b":[1,2,3]}`)
	ops, err := Diff(doc, doc)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestDiff_EscapesPointerTokens(t *testing.T) {
	oldDoc := []byte(`{"a/b":1,"c~d":2}`)
	newDoc := []byte(`{"a/b":3,"c~d":4}`)

	ops, err := Diff(oldDoc, newDoc)
	require.NoError(t, err)
	require.Len(t, ops, 2)

	raw, err := json.Marshal(ops)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "/a~1b")
	assert.Contains(t, string(raw), "/c~0d")
}
