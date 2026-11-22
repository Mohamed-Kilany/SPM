package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const openAPISpecPath = "../../../docs/openapi.yaml"
const uiClientPath = "../../../ui/src/api/client.ts"

type openAPIDoc struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Parameters map[string]map[string]any `yaml:"parameters"`
	} `yaml:"components"`
}

func loadOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	raw, err := os.ReadFile(openAPISpecPath)
	if err != nil {
		t.Fatalf("read %s: %v", openAPISpecPath, err)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", openAPISpecPath, err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("OpenAPI document has no paths")
	}
	return doc
}

func clearPath(path string) string {
	return normalizePath(path)
}

// TestOpenAPIContractMatchesRouter proves every registered method/path pair is
// documented and every documented operation still exists.
func TestOpenAPIContractMatchesRouter(t *testing.T) {
	doc := loadOpenAPI(t)

	documented := make(map[string]bool)
	for path, item := range doc.Paths {
		for method := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			documented[strings.ToUpper(method)+" "+clearPath(path)] = true
		}
	}

	registered := make(map[string]bool)
	for _, route := range Routes() {
		key := route.Method + " " + normalizePath(ginToOpenAPI(route.Path))
		registered[key] = true
		if !documented[key] {
			t.Errorf("registered route missing from OpenAPI: %s", key)
		}
	}
	for key := range documented {
		if !registered[key] {
			t.Errorf("documented operation has no registered route: %s", key)
		}
	}
}

// TestOpenAPIOperationsAreComplete enforces the contract fields the pipeline
// depends on: auth, security, a success response, an error envelope, path
// parameters and request bodies for write methods.
func TestOpenAPIOperationsAreComplete(t *testing.T) {
	doc := loadOpenAPI(t)
	routes := routeSet(Routes())

	for path, item := range doc.Paths {
		pathParams := parameterNames(item["parameters"], doc.Components.Parameters)
		for rawMethod, rawOp := range item {
			method := strings.ToUpper(rawMethod)
			if !httpMethods[strings.ToLower(rawMethod)] {
				continue
			}
			op, ok := rawOp.(map[string]any)
			if !ok {
				t.Errorf("%s %s: operation is not a mapping", method, path)
				continue
			}
			key := method + " " + clearPath(path)
			route, ok := routes[key]
			if !ok {
				continue // covered by the exact-set test
			}
			for name := range parameterNames(op["parameters"], doc.Components.Parameters) {
				pathParams[name] = true
			}

			if got := stringValue(op["x-spm-auth"]); got != string(route.Auth) {
				t.Errorf("%s: x-spm-auth = %q, want %q", key, got, route.Auth)
			}
			security, ok := op["security"].([]any)
			if !ok {
				t.Errorf("%s: security must be present", key)
			} else if route.Auth == AuthPublic && len(security) != 0 {
				t.Errorf("%s: public route must have empty security", key)
			} else if route.Auth != AuthPublic && len(security) == 0 {
				t.Errorf("%s: authenticated route must declare a security scheme", key)
			}

			responses := mapValue(op["responses"])
			if responses == nil {
				t.Errorf("%s: responses missing", key)
				continue
			}
			if !hasSuccess(responses) {
				t.Errorf("%s: no 2xx response", key)
			}
			if responses["500"] == nil {
				t.Errorf("%s: no 500 error envelope", key)
			}
			if route.Auth != AuthPublic && responses["401"] == nil {
				t.Errorf("%s: no 401 response", key)
			}
			if routeNeedsBody(route) && op["requestBody"] == nil {
				t.Errorf("%s: write operation has no requestBody", key)
			}
			for _, param := range pathParameterNames(path) {
				if !pathParams[param] {
					t.Errorf("%s: path parameter %q is not documented", key, param)
				}
			}
		}
	}
}

// TestUIClientPathsExist proves the hand-written UI client still targets routes
// the API actually serves.
func TestUIClientPathsExist(t *testing.T) {
	doc := loadOpenAPI(t)
	paths := make(map[string]bool, len(doc.Paths))
	for path := range doc.Paths {
		paths[clearPath(path)] = true
	}

	raw, err := os.ReadFile(filepath.FromSlash(uiClientPath))
	if err != nil {
		t.Fatalf("read %s: %v", uiClientPath, err)
	}
	clientPaths := extractClientPaths(string(raw))
	if len(clientPaths) == 0 {
		t.Fatal("no API paths found in the UI client")
	}
	sort.Strings(clientPaths)
	for _, path := range clientPaths {
		if !paths[path] {
			t.Errorf("UI client targets undocumented path: %s", path)
		}
	}
}

func routeNeedsBody(route Route) bool {
	switch strings.ToUpper(route.Method) {
	case "POST", "PUT", "PATCH":
		return !strings.HasSuffix(route.Path, "/provision") &&
			!strings.HasSuffix(route.Path, "/auth/stream-ticket") &&
			!strings.HasSuffix(route.Path, "/auth/logout") &&
			!strings.HasSuffix(route.Path, "/auth/sessions/revoke-others")
	default:
		return false
	}
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

func mapValue(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func hasSuccess(responses map[string]any) bool {
	for code := range responses {
		if strings.HasPrefix(code, "2") {
			return true
		}
	}
	return false
}

func parameterNames(value any, components map[string]map[string]any) map[string]bool {
	names := map[string]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		m := mapValue(item)
		if name := stringValue(m["name"]); name != "" {
			names[name] = true
			continue
		}
		ref := stringValue(m["$ref"])
		if ref == "" {
			continue
		}
		const prefix = "#/components/parameters/"
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		if component, ok := components[strings.TrimPrefix(ref, prefix)]; ok {
			if name := stringValue(component["name"]); name != "" {
				names[name] = true
			}
		}
	}
	return names
}

func pathParameterNames(path string) []string {
	var names []string
	for _, match := range docParam.FindAllString(path, -1) {
		names = append(names, strings.Trim(match, "{}"))
	}
	return names
}

// extractClientPaths scans the TypeScript client for string literals that
// contain an API path. Template expressions are handled explicitly: an
// expression wrapping encodeURIComponent becomes a path parameter, and any
// other expression (a query suffix or conditional) is dropped.
func extractClientPaths(src string) []string {
	seen := map[string]bool{}
	var result []string
	const marker = "/api/v1"
	for i := 0; i+len(marker) <= len(src); i++ {
		if !strings.HasPrefix(src[i:], marker) {
			continue
		}
		var b strings.Builder
		j := i
		for j < len(src) {
			c := src[j]
			if isPathTerminator(c) {
				break
			}
			if c == '$' && j+1 < len(src) && src[j+1] == '{' {
				exprStart := j + 2
				k := exprStart
				depth := 0
				for k < len(src) {
					switch src[k] {
					case '{':
						depth++
					case '}':
						if depth == 0 {
							goto closed
						}
						depth--
					}
					k++
				}
			closed:
				if strings.Contains(src[exprStart:k], "encodeURIComponent") {
					b.WriteString("{}")
				}
				j = k + 1
				continue
			}
			b.WriteByte(c)
			j++
		}
		path := strings.TrimSuffix(b.String(), "/")
		if path == "" {
			continue
		}
		path = clearPath(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func isPathTerminator(c byte) bool {
	switch c {
	case '?', '`', '\'', '"', ' ', '\t', '\n', '\r', ',', ')', '}':
		return true
	default:
		return false
	}
}
