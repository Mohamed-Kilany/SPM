// Command openapi keeps docs/openapi.yaml aligned with the routes registered by
// internal/server. Existing hand-written operations are preserved; missing
// operations are added with method, auth, request, success and error responses;
// operations that no longer exist in the router are removed.
//
// Usage (from api/):
//
//	go run ./cmd/openapi -write
//	go run ./cmd/openapi -check
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"spm-platform/api/internal/server"
)

var (
	openAPIParam = regexp.MustCompile(`\{([^}]+)\}`)
	ginParam     = regexp.MustCompile(`:([A-Za-z0-9_]+)`)
)

const banner = `# SPM Platform API contract.
#
# Route coverage is enforced by api/internal/server tests. Regenerate with:
#   cd api && go run ./cmd/openapi -write
# Validate without writing with:
#   cd api && go run ./cmd/openapi -check
`

func main() {
	specPath := flag.String("spec", filepath.Join("..", "docs", "openapi.yaml"), "path to the OpenAPI document")
	write := flag.Bool("write", false, "rewrite the spec")
	check := flag.Bool("check", false, "exit non-zero when the spec is out of date")
	flag.Parse()

	current, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err)
	}
	updated, err := render(current)
	if err != nil {
		fatal(err)
	}

	if *check {
		if !bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(updated, []byte("\r\n"), []byte("\n"))) {
			fmt.Fprintln(os.Stderr, "docs/openapi.yaml is out of date; run: cd api && go run ./cmd/openapi -write")
			os.Exit(1)
		}
		fmt.Println("OpenAPI contract is up to date")
		return
	}
	if !*write {
		fmt.Print(string(updated))
		return
	}
	if err := os.WriteFile(*specPath, updated, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", *specPath)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func render(input []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(input, &doc); err != nil {
		return nil, fmt.Errorf("parse OpenAPI: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("empty OpenAPI document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("OpenAPI document root is not a mapping")
	}

	ensureComponents(root)
	updateInfo(root)
	updateTags(root)
	paths := buildPaths()
	setMap(root, "paths", paths)
	pruneComponents(root, paths)

	doc.HeadComment = strings.TrimSuffix(banner, "\n")
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildPaths renders every registered route. Operations are generated, not
// merged, so the document is byte-for-byte deterministic and cannot retain a
// stale operation. Hand-maintained schemas and reusable responses live under
// components and are preserved.
func buildPaths() *yaml.Node {
	byPath := make(map[string][]server.Route)
	var order []string
	for _, route := range server.Routes() {
		path := ginToOpenAPI(route.Path)
		if _, seen := byPath[path]; !seen {
			order = append(order, path)
		}
		byPath[path] = append(byPath[path], route)
	}
	sort.Strings(order)

	paths := mappingNode()
	for _, path := range order {
		routes := byPath[path]
		sort.Slice(routes, func(i, j int) bool {
			return strings.ToLower(routes[i].Method) < strings.ToLower(routes[j].Method)
		})
		item := mappingNode()
		for _, route := range routes {
			operation := nodeFromYAML(operationYAML(route, path))
			ensurePathParameters(operation, path)
			appendMap(item, strings.ToLower(route.Method), operation)
		}
		appendMap(paths, path, item)
	}
	return paths
}

func updateInfo(root *yaml.Node) {
	info := mapGet(root, "info")
	if info == nil {
		return
	}
	if version := mapGet(info, "version"); version != nil {
		version.Value = "2.0.0"
	}
	if description := mapGet(info, "description"); description != nil {
		description.Value = "Offline-first software delivery platform API. The normalized workspace\n" +
			"aggregate is the source of truth for SDLC/STLC artifacts. Mutations emit a\n" +
			"`workspace.changed` event and bump the project SSE revision.\n\n" +
			"This contract is generated from the Go router by `api/cmd/openapi` and\n" +
			"checked by the pull-request gate. Do not edit generated operations by hand;\n" +
			"update the route metadata in `api/internal/server` instead."
	}
	setMap(info, "license", nodeFromYAML("name: Proprietary\nidentifier: LicenseRef-Proprietary"))
}

var tagDescriptions = map[string]string{
	"AI":        "Retrieval, answers and delivery insights derived from authorized project context.",
	"Admin":     "User administration and global role management.",
	"Auth":      "Authentication, profile updates and single-use stream tickets.",
	"Code":      "Repositories, branches, pull requests and review evidence.",
	"Delivery":  "Environments, artifact links and delivery configuration.",
	"Planning":  "PRDs, epics, features, work items, sprints, releases and tickets.",
	"Projects":  "Project lifecycle, settings and membership.",
	"Reports":   "Reports, exports and release readiness.",
	"Search":    "JQL-style cross-artifact search.",
	"System":    "Liveness and readiness probes.",
	"Tests":     "Test cases, runs, results and bug creation.",
	"Webhooks":  "Inbound Gitea, Argo CD and generic delivery events.",
	"Workspace": "The normalized project workspace aggregate and activity.",
}

func updateTags(root *yaml.Node) {
	used := map[string]bool{}
	for _, route := range server.Routes() {
		used[tagFor(ginToOpenAPI(route.Path))] = true
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, name := range names {
		item := mappingNode()
		appendMap(item, "name", scalarNode(name))
		if description := tagDescriptions[name]; description != "" {
			appendMap(item, "description", scalarNode(description))
		}
		seq.Content = append(seq.Content, item)
	}
	setMap(root, "tags", seq)
}

// pruneComponents removes component entries that no operation can reach. The
// generated contract should not accumulate dead schemas from removed routes.
func pruneComponents(root, paths *yaml.Node) {
	components := mapGet(root, "components")
	if components == nil {
		return
	}
	refs := map[string]bool{}
	collectRefs(paths, refs)

	sections := []string{"schemas", "responses", "parameters", "requestBodies", "headers"}
	visited := map[string]bool{}
	for {
		progressed := false
		for _, section := range sections {
			node := mapGet(components, section)
			if node == nil || node.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := section + "/" + node.Content[i].Value
				if refs[key] && !visited[key] {
					visited[key] = true
					progressed = true
					collectRefs(node.Content[i+1], refs)
				}
			}
		}
		if !progressed {
			break
		}
	}

	for _, section := range sections {
		node := mapGet(components, section)
		if node == nil || node.Kind != yaml.MappingNode {
			continue
		}
		kept := mappingNode()
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if refs[section+"/"+key] {
				appendMap(kept, key, node.Content[i+1])
			}
		}
		if len(kept.Content) == 0 {
			removeMap(components, section)
			continue
		}
		setMap(components, section, kept)
	}
}

var componentRef = regexp.MustCompile(`^#/components/((?:schemas|responses|parameters|requestBodies|headers)/[^/]+)$`)

func collectRefs(node *yaml.Node, refs map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode && node.Value != "" {
		if match := componentRef.FindStringSubmatch(node.Value); match != nil {
			refs[match[1]] = true
		}
	}
	for _, child := range node.Content {
		collectRefs(child, refs)
	}
}

func ensureComponents(root *yaml.Node) {
	components := mapGet(root, "components")
	if components == nil {
		components = mappingNode()
		appendMap(root, "components", components)
	}

	schemes := mapGet(components, "securitySchemes")
	if schemes == nil {
		schemes = mappingNode()
		appendMap(components, "securitySchemes", schemes)
	}
	ensureMapEntry(schemes, "bearerAuth", nodeFromYAML(`type: http
scheme: bearer
bearerFormat: JWT`))
	setMap(schemes, "streamTicket", nodeFromYAML(`type: apiKey
in: query
name: ticket
description: Single-use ticket issued by POST /api/v1/auth/stream-ticket.`))

	responses := mapGet(components, "responses")
	if responses == nil {
		responses = mappingNode()
		appendMap(components, "responses", responses)
	}
	setMap(responses, "Forbidden", errorResponse("The caller is authenticated but not authorized"))
	setMap(responses, "Conflict", errorResponse("The resource already exists or conflicts with current state"))
	setMap(responses, "PreconditionRequired", errorResponse("A mutation of a revisioned planning resource requires the current revision in If-Match or expected_version"))
	setMap(responses, "RevisionConflict", nodeFromYAML(`description: "The revision is stale; current_version is the value to retry from"
content:
  application/json:
    schema:
      $ref: '#/components/schemas/RevisionConflict'`))
	setMap(responses, "UnprocessableEntity", errorResponse("The request is valid but violates a business rule"))
	setMap(responses, "TooManyRequests", errorResponse("Rate limit exceeded"))
	setMap(responses, "InternalError", errorResponse("Unexpected server error"))

	schemas := mapGet(components, "schemas")
	if schemas == nil {
		schemas = mappingNode()
		appendMap(components, "schemas", schemas)
	}
	for _, name := range sortedSchemaNames() {
		setMap(schemas, name, nodeFromYAML(schemaBodies[name]))
	}
}

func errorResponse(description string) *yaml.Node {
	return nodeFromYAML(fmt.Sprintf(`description: %q
content:
  application/json:
    schema:
      $ref: '#/components/schemas/Error'`, description))
}

var schemaBodies = map[string]string{
	"LoginInput": `type: object
required: [username, password]
properties:
  username: { type: string }
  password: { type: string, format: password }`,
	"WebhookPayload": `type: object
additionalProperties: true
description: Provider-specific webhook payload. Authentication uses the provider signature or token.`,
	"ProjectInput": `type: object
required: [name]
properties:
  name: { type: string, maxLength: 200 }
  description: { type: string }`,
	"ProjectUpdateInput": `type: object
properties:
  name: { type: string, maxLength: 200 }
  description: { type: string }
  key_prefix: { type: string, maxLength: 12 }
  default_branch: { type: string }
  status: { type: string }`,
	"ProjectLabelInput": `type: object
required: [name]
properties:
  name: { type: string, maxLength: 24, description: 'Lowercase; letters, digits, spaces, hyphens and underscores.' }
  color: { type: string, enum: [slate, blue, green, amber, red, purple, pink, teal], default: slate }`,
	"ProjectLabel": `type: object
required: [id, project_id, name, color, created_at, updated_at]
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  name: { type: string }
  color: { type: string, enum: [slate, blue, green, amber, red, purple, pink, teal] }
  created_by: { type: [string, "null"], format: uuid }
  created_at: { type: string, format: date-time }
  updated_at: { type: string, format: date-time }`,
	"ProjectLabelList": `type: array
items:
  $ref: '#/components/schemas/ProjectLabel'`,
	"CustomFieldInput": `type: object
required: [name, type]
properties:
  key: { type: string, maxLength: 40, description: 'Generated from the name when omitted. Lowercase letters, digits and underscores.' }
  name: { type: string, maxLength: 80 }
  type: { type: string, enum: [text, number, select, date, user] }
  description: { type: string }
  options:
    type: array
    maxItems: 50
    description: Required for select fields; forbidden for other types.
    items:
      type: object
      required: [value, label]
      properties:
        value: { type: string, pattern: '^[a-z0-9][a-z0-9_-]{0,39}$' }
        label: { type: string }
        color: { type: string }
  config:
    type: object
    description: Number fields accept min and max.
    additionalProperties: true
  required: { type: boolean, default: false }
  position: { type: integer, minimum: 0 }
  archived: { type: boolean, description: Set false to restore an archived field. }`,
	"CustomField": `type: object
required: [id, project_id, key, name, type, options, required, position, created_at, updated_at]
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  key: { type: string }
  name: { type: string }
  type: { type: string, enum: [text, number, select, date, user] }
  description: { type: string }
  options:
    type: array
    items:
      type: object
      required: [value, label]
      properties:
        value: { type: string }
        label: { type: string }
        color: { type: string }
  config: { type: object, additionalProperties: true }
  required: { type: boolean }
  position: { type: integer }
  archived_at: { type: [string, "null"], format: date-time }
  created_by: { type: [string, "null"], format: uuid }
  created_at: { type: string, format: date-time }
  updated_at: { type: string, format: date-time }`,
	"CustomFieldList": `type: array
items:
  $ref: '#/components/schemas/CustomField'`,
	"CustomFieldValuesInput": `type: object
description: Replaces every custom field value for the work item. An omitted field is cleared.
properties:
  values:
    type: object
    additionalProperties: true
    description: Map of custom field ID to a typed value (string, number, YYYY-MM-DD date or user UUID).`,
	"CustomFieldValue": `type: object
required: [id, project_id, field_id, entity_type, entity_id, value, updated_at]
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  field_id: { type: string, format: uuid }
  entity_type: { type: string, enum: [work_item, test_case] }
  entity_id: { type: string, format: uuid }
  value: { description: Typed value matching the field definition. }
  updated_at: { type: string, format: date-time }`,
	"CustomFieldValueList": `type: array
items:
  $ref: '#/components/schemas/CustomFieldValue'`,
	"SavedFilterInput": `type: object
required: [name, scope]
properties:
  name: { type: string, maxLength: 80 }
  scope: { type: string, enum: [planning, board, test] }
  visibility: { type: string, enum: [private, shared], default: private }
  filters: { type: object, additionalProperties: true }
  sort: { type: object, additionalProperties: true }
  columns:
    type: array
    maxItems: 20
    items: { type: string, maxLength: 40 }
  pinned: { type: boolean, default: false }
  is_default: { type: boolean, default: false }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"SavedFilter": `type: object
required: [id, project_id, name, scope, visibility, owner_id, filters, sort, columns, pinned, is_default, created_at, updated_at, revision]
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  name: { type: string }
  scope: { type: string, enum: [planning, board, test] }
  visibility: { type: string, enum: [private, shared] }
  owner_id: { type: string, format: uuid }
  filters: { type: object, additionalProperties: true }
  sort: { type: object, additionalProperties: true }
  columns:
    type: array
    items: { type: string }
  pinned: { type: boolean }
  is_default: { type: boolean }
  created_at: { type: string, format: date-time }
  updated_at: { type: string, format: date-time }
  revision: { type: integer, format: int64, minimum: 1 }`,
	"SavedFilterList": `type: array
items:
  $ref: '#/components/schemas/SavedFilter'`,
	"ProfileUpdateInput": `type: object
properties:
  display_name: { type: string, maxLength: 255 }
  email: { type: string, format: email, maxLength: 255 }
  avatar_url: { type: string, maxLength: 2048 }`,
	"ChangePasswordInput": `type: object
required: [current_password, new_password]
properties:
  current_password: { type: string, format: password }
  new_password: { type: string, format: password, minLength: 10, maxLength: 128 }`,
	"AdminUserInput": `type: object
properties:
  username: { type: string, maxLength: 100 }
  email: { type: string, format: email, maxLength: 255 }
  display_name: { type: string, maxLength: 255 }
  avatar_url: { type: string, maxLength: 2048 }
  role: { type: string, enum: [admin, member, viewer] }
  is_active: { type: boolean }
  password: { type: string, format: password, minLength: 10, maxLength: 128 }`,
	"MemberInput": `type: object
required: [user_id, role]
properties:
  user_id: { type: string, format: uuid }
  role: { type: string, enum: [owner, maintainer, developer, tester, viewer] }`,
	"TicketInput": `type: object
required: [title]
properties:
  title: { type: string }
  description: { type: string }
  state: { type: string }
  assignee_id: { type: [string, "null"], format: uuid }
  priority: { type: string }`,
	"StateInput": `type: object
required: [state]
properties:
  state: { type: string }`,
	"StatusInput": `type: object
required: [status]
properties:
  status: { type: string }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"SprintInput": `type: object
required: [name]
properties:
  name: { type: string }
  goal: { type: string }
  status: { type: string }
  start_date: { type: [string, "null"], format: date-time }
  end_date: { type: [string, "null"], format: date-time }
  capacity: { type: [number, "null"] }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"ReleaseInput": `type: object
required: [name, version]
properties:
  name: { type: string }
  version: { type: string }
  status: { type: string }
  planned_at: { type: [string, "null"], format: date-time }
  milestone_at: { type: [string, "null"], format: date-time }
  notes: { type: string }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"TestResultInput": `type: object
properties:
  status: { type: string }
  actual_result: { type: string }
  duration_ms: { type: integer }
  notes: { type: string }`,
	"TestStepResultInput": `type: object
properties:
  status: { type: string }
  actual_result: { type: string }
  evidence_url: { type: string, maxLength: 2048 }`,
	"BugFromResultInput": `type: object
description: Creates a bug from one executed result. Traceability context (run, case, step, environment, build, deployment) is appended to the description and linked automatically.
properties:
  title: { type: string, description: Defaults to the case and failed step when omitted. }
  description: { type: string }
  severity: { type: [string, "null"], enum: [blocker, critical, major, minor, trivial, null], description: Defaults to major in the UI. }
  priority: { type: string, enum: [low, medium, high, critical], default: high }
  assignee_id: { type: [string, "null"], format: uuid }
  reproduction_steps: { type: string, description: Defaults to the executed step actions. }
  expected_result: { type: string, description: Defaults to the failed step expectation. }
  actual_result: { type: string, description: Defaults to the failed step or result actual value. }
  step_result_id: { type: [string, "null"], format: uuid, description: Must belong to the result. Defaults to the first failed step. }
  force: { type: boolean, default: false, description: Create even when an open duplicate is reported. }
`,
	"BulkBugInput": `type: object
description: Creates defects from failed results in one run. Already linked results and results without a failure are reported as skipped; duplicates are reported per item unless force is true.
properties:
  result_ids:
    type: array
    description: Selected results. Omit to target every failed result in the run.
    items: { type: string, format: uuid }
  force: { type: boolean, default: false, description: Create bugs even when an open duplicate is reported. }
`,
	"BugDuplicate": `type: object
required: [id, key, title, status, match_on]
properties:
  id: { type: string, format: uuid }
  key: { type: string }
  title: { type: string }
  status: { type: string }
  match_on: { type: string, enum: [title, test_result], description: test_result means the result is already linked or an artifact link exists. }
`,
	"BugCreateResult": `type: object
required: [id, key, revision, links]
properties:
  id: { type: string, format: uuid }
  key: { type: string }
  revision: { type: integer, format: int64, minimum: 1 }
  step_result_id: { type: [string, "null"], format: uuid }
  links:
    type: array
    description: Artifact links written with the defect.
    items:
      type: object
      required: [source_type, source_id, target_type, target_id, link_type]
      properties:
        source_type: { type: string, enum: [test_result, test_run, test_case, build, deployment] }
        source_id: { type: string, format: uuid }
        target_type: { type: string, enum: [work_item] }
        target_id: { type: string, format: uuid }
        link_type: { type: string, enum: [found_bug] }
`,
	"BulkBugItem": `type: object
required: [result_id, status]
properties:
  result_id: { type: string, format: uuid }
  status: { type: string, enum: [created, duplicate, skipped, failed] }
  reason: { type: string, enum: [already_linked, not_failed, open_bug_exists, load_failed, duplicate_check_failed, create_failed] }
  bug_id: { type: string, format: uuid }
  key: { type: string }
  step_result_id: { type: string, format: uuid }
  duplicates:
    type: array
    items:
      $ref: '#/components/schemas/BugDuplicate'
`,
	"BulkBugResult": `type: object
required: [run_id, requested, created, duplicates, skipped, failed, items]
properties:
  run_id: { type: string, format: uuid }
  requested: { type: integer, minimum: 0 }
  created: { type: integer, minimum: 0 }
  duplicates: { type: integer, minimum: 0 }
  skipped: { type: integer, minimum: 0 }
  failed: { type: integer, minimum: 0 }
  items:
    type: array
    items:
      $ref: '#/components/schemas/BulkBugItem'
`,
	"RepositoryInput": `type: object
required: [name]
properties:
  name: { type: string }
  provider: { type: string, enum: [gitea, github, gitlab, bitbucket] }
  default_branch: { type: string }
  description: { type: string }`,
	"EnvironmentInput": `type: object
required: [name, type]
properties:
  name: { type: string }
  type: { type: string, enum: [development, staging, production] }
  url: { type: string, maxLength: 2048 }`,
	"LinkInput": `type: object
required: [source_type, source_id, target_type, target_id]
properties:
  source_type: { type: string }
  source_id: { type: string, format: uuid }
  target_type: { type: string }
  target_id: { type: string, format: uuid }
  link_type: { type: string }`,
	"MergeInput": `type: object
properties:
  commit_message: { type: string }
  strategy: { type: string, enum: [merge, squash, rebase] }`,
	"CommentInput": `type: object
required: [body]
properties:
  body:
    type: string
    minLength: 1
    maxLength: 10000
    description: 'Plain text. Control characters are stripped and clients must render it escaped, never as HTML.'`,
	"WorkItemLinkInput": `type: object
required: [target_type, target_id]
properties:
  target_type: { type: string, enum: [prd, epic, feature, work_item, test_case, sprint, release] }
  target_id: { type: string, format: uuid }
  link_type: { type: string, maxLength: 40, default: related, description: 'Lower-case letters, digits, hyphens and underscores.' }`,
	"Mention": `type: object
properties:
  user_id: { type: string, format: uuid }
  username: { type: string }
  display_name: { type: string }`,
	"Comment": `type: object
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  entity_type: { type: string }
  entity_id: { type: string, format: uuid }
  author_id: { type: [string, "null"], format: uuid }
  author_name: { type: string }
  body: { type: string, description: 'Plain text; render escaped.' }
  mentions: { type: array, items: { $ref: '#/components/schemas/Mention' } }
  created_at: { type: string, format: date-time }
  updated_at: { type: string, format: date-time }
  edited_at: { type: [string, "null"], format: date-time }`,
	"Attachment": `type: object
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  entity_type: { type: string }
  entity_id: { type: string, format: uuid }
  filename: { type: string }
  mime_type: { type: string }
  size_bytes: { type: integer, format: int64 }
  checksum_sha256: { type: string, pattern: '^[0-9a-f]{64}$' }
  scan_status: { type: string, enum: [pending, clean, rejected] }
  scan_engine: { type: string }
  scan_detail: { type: string }
  uploaded_by: { type: [string, "null"], format: uuid }
  uploader_name: { type: string }
  created_at: { type: string, format: date-time }
  download_url: { type: string }`,
	"LinkedArtifact": `type: object
properties:
  link_id: { type: string, format: uuid }
  link_type: { type: string }
  direction: { type: string, enum: [inbound, outbound] }
  artifact_type: { type: string, enum: [prd, epic, feature, work_item, test_case, sprint, release] }
  artifact_id: { type: string, format: uuid }
  artifact_key: { type: string }
  title: { type: string }
  status: { type: string }`,
	"WatchState": `type: object
properties:
  watching: { type: boolean }`,
	"WorkItemDetail": `type: object
description: A work item plus the collaboration collections rendered by the detail drawer.
additionalProperties: true
properties:
  id: { type: string, format: uuid }
  project_id: { type: string, format: uuid }
  key: { type: string }
  item_number: { type: integer, format: int64 }
  type: { type: string }
  title: { type: string }
  description: { type: string }
  acceptance_criteria: { type: string }
  status: { type: string }
  priority: { type: string }
  labels: { type: array, items: { type: string } }
  revision: { type: integer, format: int64 }
  children: { type: array, items: { type: object, additionalProperties: true } }
  comments: { type: array, items: { $ref: '#/components/schemas/Comment' } }
  attachments: { type: array, items: { $ref: '#/components/schemas/Attachment' } }
  watchers: { type: array, items: { type: object, additionalProperties: true } }
  watching: { type: boolean }
  links: { type: array, items: { $ref: '#/components/schemas/LinkedArtifact' } }
  activity: { type: array, items: { type: object, additionalProperties: true } }
  branches: { type: array, items: { type: object, additionalProperties: true } }
  pull_requests: { type: array, items: { type: object, additionalProperties: true } }
  child_count: { type: integer }
  comment_count: { type: integer }
  attachment_count: { type: integer }
  watcher_count: { type: integer }`,
	"AIAskInput": `type: object
required: [question]
properties:
  question: { type: string, maxLength: 4000 }`,
	"SearchResponse": `type: object
properties:
  work_items: { type: array, items: { type: object } }
  features: { type: array, items: { type: object } }
  test_cases: { type: array, items: { type: object } }
  projects: { type: array, items: { type: object } }
  total: { type: integer }`,
	"Member": `type: object
properties:
  user_id: { type: string, format: uuid }
  username: { type: string }
  email: { type: string }
  role: { type: string }`,
	"ProjectAccess": `type: object
properties:
  project_id: { type: string, format: uuid }
  role: { type: string, enum: [owner, maintainer, developer, tester, viewer, admin] }
  capabilities:
    type: array
    items: { type: string }`,
	"User": `type: object
properties:
  id: { type: string, format: uuid }
  username: { type: string }
  email: { type: string }
  display_name: { type: string }
  avatar_url: { type: string }
  role: { type: string, enum: [admin, member, viewer] }
  project_ids:
    type: array
    items: { type: string, format: uuid }
  projects:
    type: array
    items:
      $ref: '#/components/schemas/ProjectAccess'`,
	"LoginResponse": `type: object
properties:
  token: { type: string }
  expires_in: { type: integer }
  user:
    $ref: '#/components/schemas/User'`,
	"AuditEvent": `type: object
properties:
  id: { type: string, format: uuid }
  occurred_at: { type: string, format: date-time }
  actor_id: { type: [string, "null"], format: uuid }
  actor_username: { type: string }
  actor_role: { type: string }
  action: { type: string }
  target_type: { type: string }
  target_id: { type: string }
  project_id: { type: [string, "null"], format: uuid }
  project_name: { type: string }
  result: { type: string, enum: [success, failure, denied] }
  reason: { type: string }
  ip: { type: string }
  user_agent: { type: string }
  request_id: { type: string }
  before: { type: object, additionalProperties: true }
  after: { type: object, additionalProperties: true }
  prev_hash: { type: string }
  hash: { type: string }`,
	"AuditPage": `type: object
properties:
  events:
    type: array
    items:
      $ref: '#/components/schemas/AuditEvent'
  next_cursor: { type: string }`,
	"AuditVerification": `type: object
required: [ok, checked, checked_at]
properties:
  ok: { type: boolean }
  checked: { type: integer, format: int64 }
  first_bad_id: { type: string }
  first_bad_cause: { type: string, enum: [missing_anchor, broken_link, hash_mismatch] }
  checked_at: { type: string, format: date-time }`,
	"RevisionResult": `type: object
required: [id, revision]
description: Result of a planning mutation. revision is the new concurrency token and is also returned as the ETag on create and update.
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  deleted: { type: boolean, description: Present and true on a successful delete. }`,
	"RevisionConflict": `type: object
required: [error, current_version]
properties:
  error:
    $ref: '#/components/schemas/Error'
  current_version: { type: integer, format: int64, minimum: 1, description: Revision to send on retry; also returned as the ETag. }`,
	"SprintStartInput": `type: object
properties:
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"SprintCloseInput": `type: object
properties:
  policy: { type: string, enum: [backlog, next_sprint], default: backlog, description: Where incomplete work moves when the sprint closes. }
  next_sprint_id: { type: [string, "null"], format: uuid, description: Required when policy is next_sprint; must be a planned or active sprint in the same project. }
  note: { type: string, maxLength: 1000, description: Optional audit note for the close. }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"ReleaseFreezeInput": `type: object
required: [reason]
properties:
  reason: { type: string, minLength: 1, maxLength: 1000 }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"ReleaseExceptionInput": `type: object
required: [entity_type, reason]
properties:
  entity_type: { type: string, enum: [work_item, feature, epic] }
  entity_id: { type: [string, "null"], format: uuid, description: Omit to authorize any scope change of this entity type while the release is frozen. }
  reason: { type: string, minLength: 1, maxLength: 2000 }`,
	"ReleaseSignOffInput": `type: object
required: [evidence]
properties:
  evidence: { type: string, minLength: 10, maxLength: 5000, description: Sign-off evidence. Its SHA-256 digest is stored in the audit trail. }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"SprintMetrics": `type: object
properties:
  sprint_id: { type: string, format: uuid }
  name: { type: string }
  status: { type: string, enum: [planned, active, completed, cancelled] }
  time_zone: { type: string }
  start_date: { type: [string, "null"], format: date-time }
  end_date: { type: [string, "null"], format: date-time }
  capacity: { type: [number, "null"] }
  committed_points: { type: number }
  completed_points: { type: number }
  remaining_points: { type: number }
  completed_items: { type: integer }
  total_items: { type: integer }
  remaining_items: { type: integer }
  velocity_points: { type: [number, "null"], description: Mean completed points of the latest three completed sprints. }
  velocity_sample_size: { type: integer }
  capacity_used_percent: { type: [number, "null"] }
  days_total: { type: integer }
  days_elapsed: { type: integer }
  days_remaining: { type: integer }`,
	"SprintClosePreview": `type: object
properties:
  sprint_id: { type: string, format: uuid }
  sprint_name: { type: string }
  status: { type: string }
  policy: { type: string, enum: [backlog, next_sprint] }
  next_sprint_id: { type: [string, "null"], format: uuid }
  next_sprint_name: { type: string }
  incomplete_items:
    type: array
    items:
      type: object
      properties:
        id: { type: string, format: uuid }
        key: { type: string }
        title: { type: string }
        status: { type: string }
        estimate_points: { type: [number, "null"] }
  incomplete_count: { type: integer }
  incomplete_points: { type: number }
  completed_count: { type: integer }
  completed_points: { type: number }`,
	"SprintLifecycleResult": `type: object
required: [id, revision, status]
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  status: { type: string }
  activated_at: { type: string, format: date-time }`,
	"SprintCloseResult": `type: object
required: [id, revision, status]
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  status: { type: string, enum: [completed] }
  policy: { type: string, enum: [backlog, next_sprint] }
  moved_items: { type: integer }
  moved_points: { type: number }
  next_sprint_id: { type: [string, "null"], format: uuid }
  completed_at: { type: string, format: date-time }`,
	"ReleaseFreezeResult": `type: object
required: [id, revision, frozen_at]
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  frozen_at: { type: [string, "null"], format: date-time }
  freeze_reason: { type: string }`,
	"ReleaseScopeException": `type: object
required: [id, release_id, entity_type, reason, created_at]
properties:
  id: { type: string, format: uuid }
  release_id: { type: string, format: uuid }
  entity_type: { type: string, enum: [work_item, feature, epic] }
  entity_id: { type: [string, "null"], format: uuid }
  reason: { type: string }
  created_by: { type: [string, "null"], format: uuid }
  created_at: { type: string, format: date-time }`,
	"ReleaseExceptionList": `type: object
properties:
  exceptions:
    type: array
    items:
      $ref: '#/components/schemas/ReleaseScopeException'`,
	"ReleaseSignOffResult": `type: object
required: [id, revision, signed_off_at]
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  signed_off_at: { type: string, format: date-time }
  signed_off_by: { type: [string, "null"], format: uuid }
  sign_off_evidence: { type: string }`,
	"TestPlanInput": `type: object
required: [name]
properties:
  name: { type: string, maxLength: 255 }
  description: { type: string }
  status: { type: string, enum: [draft, active, archived], default: draft, description: Archived is terminal and cannot be reactivated. }
  owner_id: { type: [string, "null"], format: uuid, description: Must be a project member. }
  start_date: { type: [string, "null"], format: date }
  end_date: { type: [string, "null"], format: date, description: Must not be before start_date. }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"TestSuiteInput": `type: object
required: [name]
properties:
  name: { type: string, maxLength: 255 }
  description: { type: string }
  assignee_id: { type: [string, "null"], format: uuid, description: Must be a project member. }
  position: { type: [integer, "null"], minimum: 0, description: Omit on create to append. }
  archived: { type: boolean, default: false }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"SuiteOrderInput": `type: object
required: [suite_ids]
properties:
  suite_ids:
    type: array
    description: Must list every active suite in the plan exactly once; archived suites are omitted.
    items: { type: string, format: uuid }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current plan revision; alternative to If-Match and the query parameter. }`,
	"TestSuiteFilter": `type: object
description: Empty fields are ignored. Supplied fields are combined with AND.
properties:
  feature_id: { type: [string, "null"], format: uuid }
  type: { type: string, enum: [manual, automated] }
  priority: { type: string, enum: [low, medium, high, critical] }
  status: { type: string, enum: [draft, ready, deprecated] }`,
	"SuiteCasesInput": `type: object
description: Provide at least one of test_case_ids, filter or csv. Results from all supplied selectors are merged, deduplicated and appended in title order.
properties:
  test_case_ids:
    type: array
    items: { type: string, format: uuid }
  filter:
    $ref: '#/components/schemas/TestSuiteFilter'
  csv:
    type: string
    maxLength: 100000
    description: One exact case title per line. A leading title/case/name header is ignored. Any unknown title rejects the whole request.
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current suite revision; alternative to If-Match and the query parameter. }`,
	"SuiteCasesResult": `type: object
required: [id, revision, added, skipped, requested]
properties:
  id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  added: { type: integer, minimum: 0 }
  skipped: { type: integer, minimum: 0, description: Cases already present in the suite. }
  requested: { type: integer, minimum: 0 }`,
	"TestRunInput": `type: object
description: Runs snapshot plan and suite names plus each result's case revision. Precedence is test_case_ids, then filter, then suite_id, then plan_id, then every case in the project.
properties:
  name: { type: string }
  status: { type: string, default: planned }
  release_id: { type: [string, "null"], format: uuid }
  sprint_id: { type: [string, "null"], format: uuid }
  environment_id: { type: string, format: uuid, description: Required; must belong to the project. }
  build_id: { type: [string, "null"], format: uuid }
  plan_id: { type: [string, "null"], format: uuid, description: Archived plans cannot start a run. }
  suite_id: { type: [string, "null"], format: uuid, description: Archived suites cannot start a run; must belong to plan_id when both are supplied. }
  filter:
    $ref: '#/components/schemas/TestSuiteFilter'
  test_case_ids:
    type: array
    items: { type: string, format: uuid }
  executed_by: { type: [string, "null"], format: uuid }`,
	"TestRunFinishInput": `type: object
description: Finishing a run with not_run cases requires force=true and is recorded as an incomplete outcome.
properties:
  force: { type: boolean, default: false, description: Complete even when cases are still not run. }`,
	"TestRunFinishResult": `type: object
description: Deterministic finish summary. Repeating finish on a completed run returns the same counts with already_completed=true.
required: [id, status, outcome, total, passed, failed, blocked, skipped, not_run, already_completed]
properties:
  id: { type: string, format: uuid }
  status: { type: string, enum: [completed] }
  outcome: { type: string, enum: [passed, failed, incomplete], description: incomplete when forced with not_run cases; failed when any case failed. }
  total: { type: integer, minimum: 0 }
  passed: { type: integer, minimum: 0 }
  failed: { type: integer, minimum: 0 }
  blocked: { type: integer, minimum: 0 }
  skipped: { type: integer, minimum: 0 }
  not_run: { type: integer, minimum: 0 }
  completed_at: { type: [string, "null"], format: date-time }
  already_completed: { type: boolean }`,
	"TestStepEvidence": `type: object
description: A scanned attachment linked to one test step result through evidence_url.
required: [id, test_step_result_id, filename, evidence_url]
properties:
  id: { type: string, format: uuid }
  test_step_result_id: { type: string, format: uuid }
  filename: { type: string }
  mime_type: { type: string }
  size_bytes: { type: integer, format: int64 }
  checksum_sha256: { type: string }
  scan_status: { type: string, description: "clean, skipped or infected. Downloads require clean." }
  scan_engine: { type: string }
  scan_detail: { type: string }
  uploaded_by: { type: [string, "null"], format: uuid }
  evidence_url: { type: string, description: Project-scoped download URL stored on the step result. }
  created_at: { type: string, format: date-time }`,
	"TestCaseInput": `type: object
description: Creating or editing a case stores an immutable version snapshot. Steps are replaced as a set and each needs an action and an expected result.
required: [title, feature_id, steps]
properties:
  feature_id: { type: string, format: uuid }
  title: { type: string, maxLength: 255 }
  description: { type: string }
  preconditions: { type: string }
  type: { type: string, enum: [manual, automated], default: manual }
  priority: { type: string, enum: [low, medium, high, critical], default: medium }
  status: { type: string, enum: [draft, ready, deprecated], default: draft }
  automation_ref: { type: string, maxLength: 1024 }
  owner_id: { type: [string, "null"], format: uuid, description: Must be a project member. }
  tags:
    type: array
    items: { type: string }
  steps:
    type: array
    minItems: 1
    maxItems: 200
    items:
      type: object
      required: [action, expected_result]
      properties:
        shared_step_id: { type: [string, "null"], format: uuid, description: Optional template this step was copied from; the text is still stored on the case. }
        action: { type: string }
        expected_result: { type: string }
        test_data: { type: string }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
	"TestCaseVersionStep": `type: object
required: [position, action, expected_result]
properties:
  position: { type: integer, minimum: 1 }
  action: { type: string }
  expected_result: { type: string }
  test_data: { type: string }`,
	"TestCaseVersion": `type: object
description: Immutable snapshot written by every case create and edit. Runs record the revision they executed.
required: [id, test_case_id, revision, title, changed_by_name, created_at, steps]
properties:
  id: { type: string, format: uuid }
  test_case_id: { type: string, format: uuid }
  revision: { type: integer, format: int64, minimum: 1 }
  feature_id: { type: [string, "null"], format: uuid }
  feature_title: { type: string }
  title: { type: string }
  description: { type: string }
  preconditions: { type: string }
  type: { type: string, enum: [manual, automated] }
  priority: { type: string, enum: [low, medium, high, critical] }
  status: { type: string, enum: [draft, ready, deprecated] }
  automation_ref: { type: string }
  owner_id: { type: [string, "null"], format: uuid }
  tags:
    type: array
    items: { type: string }
  changed_by: { type: [string, "null"], format: uuid }
  changed_by_name: { type: string, description: Display name captured with the change so history survives a rename. }
  created_at: { type: string, format: date-time }
  steps:
    type: array
    items:
      $ref: '#/components/schemas/TestCaseVersionStep'`,
	"TestCaseVersionList": `type: object
required: [test_case_id, total, versions]
properties:
  test_case_id: { type: string, format: uuid }
  total: { type: integer, minimum: 0 }
  versions:
    type: array
    description: Newest revision first.
    items:
      $ref: '#/components/schemas/TestCaseVersion'`,
	"TestCaseExport": `type: object
description: Portable JSON document accepted by the import endpoint in the same project.
required: [format, exported_at, project_id, cases]
properties:
  format: { type: string, enum: [spm.test-cases.v1] }
  exported_at: { type: string, format: date-time }
  project_id: { type: string, format: uuid }
  cases:
    type: array
    items:
      type: object
      required: [title, steps]
      properties:
        feature_id: { type: [string, "null"], format: uuid }
        feature_title: { type: string }
        title: { type: string }
        description: { type: string }
        preconditions: { type: string }
        type: { type: string, enum: [manual, automated] }
        priority: { type: string, enum: [low, medium, high, critical] }
        status: { type: string, enum: [draft, ready, deprecated] }
        automation_ref: { type: string }
        owner_id: { type: [string, "null"], format: uuid }
        tags:
          type: array
          items: { type: string }
        steps:
          type: array
          items:
            type: object
            required: [action, expected_result]
            properties:
              action: { type: string }
              expected_result: { type: string }
              test_data: { type: string }`,
	"TestCaseImportInput": `type: object
description: Every case is validated before the first insert, and all cases are written in one transaction. A case without feature_id is matched by feature_title within the project.
required: [cases]
properties:
  format: { type: string, enum: [spm.test-cases.v1] }
  cases:
    type: array
    minItems: 1
    items:
      type: object
      required: [title, steps]
      properties:
        feature_id: { type: [string, "null"], format: uuid }
        feature_title: { type: string, description: Used when feature_id is absent; matched case-insensitively in the project. }
        title: { type: string }
        description: { type: string }
        preconditions: { type: string }
        type: { type: string, enum: [manual, automated] }
        priority: { type: string, enum: [low, medium, high, critical] }
        status: { type: string, enum: [draft, ready, deprecated] }
        automation_ref: { type: string }
        owner_id: { type: [string, "null"], format: uuid }
        tags:
          type: array
          items: { type: string }
        steps:
          type: array
          minItems: 1
          maxItems: 200
          items:
            type: object
            required: [action, expected_result]
            properties:
              action: { type: string }
              expected_result: { type: string }
              test_data: { type: string }`,
	"TestCaseImportResult": `type: object
required: [format, imported, cases]
properties:
  format: { type: string, enum: [spm.test-cases.v1] }
  imported: { type: integer, minimum: 0 }
  cases:
    type: array
    items:
      type: object
      required: [id, title, revision]
      properties:
        id: { type: string, format: uuid }
        title: { type: string }
        revision: { type: integer, format: int64, minimum: 1 }`,
	"TestSharedStepInput": `type: object
description: A reusable step template. Values are copied into a case when inserted, so editing or deleting a template never rewrites existing cases.
required: [name, action, expected_result]
properties:
  name: { type: string, maxLength: 255 }
  action: { type: string }
  expected_result: { type: string }
  test_data: { type: string }
  expected_version: { type: [integer, "null"], format: int64, minimum: 1, description: Current revision; alternative to If-Match and the query parameter. }`,
}

func sortedSchemaNames() []string {
	names := make([]string, 0, len(schemaBodies))
	for name := range schemaBodies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func operationYAML(route server.Route, openAPIPath string) string {
	tag := tagFor(openAPIPath)
	summary := summaryFor(route.Method, openAPIPath)
	security := securityFor(route.Auth)

	var b strings.Builder
	fmt.Fprintf(&b, "tags: [%q]\n", tag)
	fmt.Fprintf(&b, "summary: %q\n", summary)
	fmt.Fprintf(&b, "operationId: %s\n", operationID(route.Method, openAPIPath))
	fmt.Fprintf(&b, "x-spm-auth: %s\n", route.Auth)
	if route.Capability != "" {
		fmt.Fprintf(&b, "x-spm-capability: %s\n", route.Capability)
	}
	b.WriteString(security)
	b.WriteString("responses:\n")

	// Success response.
	code := successCode(route.Method, openAPIPath)
	switch code {
	case "204":
		b.WriteString("  '204':\n    description: Deleted\n")
	case "201":
		fmt.Fprintf(&b, "  '201':\n    description: Created\n")
		if schema := successSchema(route.Method, openAPIPath); schema != "" {
			fmt.Fprintf(&b, "    content:\n      application/json:\n        schema:\n          $ref: '#/components/schemas/%s'\n", schema)
		} else {
			b.WriteString("    content:\n      application/json:\n        schema: { type: object, additionalProperties: true }\n")
		}
		b.WriteString(revisionHeaders(route.Method, openAPIPath))
	default:
		fmt.Fprintf(&b, "  '200':\n    description: OK\n")
		if isAttachmentDownload(route.Method, openAPIPath) {
			b.WriteString("    content:\n      application/octet-stream:\n        schema:\n          type: string\n          format: binary\n    headers:\n      Content-Disposition:\n        description: Attachment filename.\n        schema: { type: string }\n      ETag:\n        description: SHA-256 checksum of the stored bytes.\n        schema: { type: string }\n")
		} else if strings.HasSuffix(openAPIPath, "/admin/audit/export") {
			b.WriteString("    content:\n      text/csv:\n        schema: { type: string }\n      application/json:\n        schema:\n          type: array\n          items:\n            $ref: '#/components/schemas/AuditEvent'\n")
		} else if schema := successSchema(route.Method, openAPIPath); schema != "" {
			fmt.Fprintf(&b, "    content:\n      application/json:\n        schema:\n          $ref: '#/components/schemas/%s'\n", schema)
		} else {
			b.WriteString("    content:\n      application/json:\n        schema: { type: object, additionalProperties: true }\n")
		}
		b.WriteString(revisionHeaders(route.Method, openAPIPath))
	}

	// Error envelope.
	for _, code := range errorCodes(route, openAPIPath) {
		ref := map[string]string{
			"400": "BadRequest", "401": "Unauthorized", "403": "Forbidden",
			"404": "NotFound", "409": "Conflict", "422": "UnprocessableEntity",
			"428": "PreconditionRequired", "429": "TooManyRequests", "500": "InternalError",
		}[code]
		if code == "409" && isRevisionedMutation(route.Method, openAPIPath) {
			ref = "RevisionConflict"
		}
		fmt.Fprintf(&b, "  '%s': { $ref: '#/components/responses/%s' }\n", code, ref)
	}

	// Request body.
	if isFileUpload(route.Method, openAPIPath) {
		b.WriteString("requestBody:\n  required: true\n  content:\n    multipart/form-data:\n      schema:\n        type: object\n        required: [file]\n        properties:\n          file: { type: string, format: binary }\n")
	} else if strings.EqualFold(route.Method, "POST") && strings.HasSuffix(openAPIPath, "/watch") {
		b.WriteString("requestBody:\n  required: false\n  content:\n    application/json:\n      schema: { type: object, additionalProperties: false }\n")
	} else if schema := requestSchema(route.Method, openAPIPath); schema != "" {
		required := "true"
		if strings.HasSuffix(openAPIPath, "/merge") {
			required = "false"
		}
		fmt.Fprintf(&b, "requestBody:\n  required: %s\n  content:\n    application/json:\n      schema:\n", required)
		fmt.Fprintf(&b, "        $ref: '#/components/schemas/%s'\n", schema)
	}

	// Path parameters are added separately by ensurePathParameters.
	if params := auditQueryParameters(openAPIPath); params != "" {
		b.WriteString(params)
	}
	if params := revisionParameters(route.Method, openAPIPath); params != "" {
		b.WriteString(params)
	}
	return b.String()
}

// isRevisionedPath reports whether path identifies a single revisioned
// planning object. Test runs are revisioned but have no update endpoint; their
// revision is returned on create and bumped when the run status is refreshed.
// Collections and sub-resources such as comments, watchers, attachments and
// links are deliberately excluded.
func isRevisionedPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "projects" || parts[3] != "{id}" {
		return false
	}
	switch parts[4] {
	case "prds", "epics", "features", "work-items", "sprints", "releases", "test-plans", "test-cases", "test-runs", "test-shared-steps":
		return true
	}
	return false
}

// isTestManagementMutation reports whether a test plan/suite sub-resource
// consumes a revision token. Create calls return a revision but do not require
// one, so they stay out of this helper and are covered by isRevisionedCreate.
func isTestManagementMutation(method, path string) bool {
	if !strings.Contains(path, "/test-plans") {
		return false
	}
	switch strings.ToUpper(method) {
	case "PUT":
		return true
	case "POST":
		return strings.HasSuffix(path, "/cases")
	case "DELETE":
		return strings.HasSuffix(path, "/cases/{caseId}")
	}
	return false
}

func isTestManagementCreate(method, path string) bool {
	if !strings.EqualFold(method, "POST") || !strings.Contains(path, "/test-plans") {
		return false
	}
	return strings.HasSuffix(path, "/test-plans") ||
		strings.HasSuffix(path, "/suites") ||
		strings.HasSuffix(path, "/cases")
}

func isWorkItemStatusPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 7 && parts[4] == "work-items" && parts[6] == "status"
}

func isWorkItemDetailPath(method, path string) bool {
	return strings.EqualFold(method, "GET") && isRevisionedPath(path) &&
		strings.Split(strings.Trim(path, "/"), "/")[4] == "work-items"
}

func isAttachmentUpload(method, path string) bool {
	return strings.EqualFold(method, "POST") && strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/attachments")
}

// isEvidenceUpload covers focused-runner evidence: a scanned file attached to
// one step result of a test run.
func isEvidenceUpload(method, path string) bool {
	return strings.EqualFold(method, "POST") &&
		strings.Contains(path, "/test-runs/") &&
		strings.HasSuffix(path, "/steps/{stepResultId}/evidence")
}

func isFileUpload(method, path string) bool {
	return isAttachmentUpload(method, path) || isEvidenceUpload(method, path)
}

func isAttachmentDownload(method, path string) bool {
	return strings.EqualFold(method, "GET") && strings.HasSuffix(path, "/attachments/{attachmentId}/download")
}

func isRevisionedCreate(method, path string) bool {
	if !strings.EqualFold(method, "POST") {
		return false
	}
	if isTestManagementCreate(method, path) {
		return true
	}
	switch {
	case strings.HasSuffix(path, "/prds"),
		strings.HasSuffix(path, "/epics"),
		strings.HasSuffix(path, "/features"),
		strings.HasSuffix(path, "/work-items"),
		strings.HasSuffix(path, "/sprints"),
		strings.HasSuffix(path, "/releases"),
		strings.HasSuffix(path, "/test-cases"),
		strings.HasSuffix(path, "/test-shared-steps"),
		strings.HasSuffix(path, "/test-runs"):
		return true
	}
	return false
}

func isRevisionedMutation(method, path string) bool {
	switch strings.ToUpper(method) {
	case "PUT", "DELETE":
		return isRevisionedPath(path) || isTestManagementMutation(method, path)
	case "PATCH":
		return isWorkItemStatusPath(path)
	default:
		return false
	}
}

// isLifecycleMutation reports whether a lifecycle sub-resource consumes a
// revision token and returns the new revision. These paths sit below a single
// revisioned resource, so they are not covered by isRevisionedPath.
func isLifecycleMutation(method, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "projects" || parts[3] != "{id}" {
		return false
	}
	switch strings.ToUpper(method) {
	case "POST":
		switch parts[4] {
		case "sprints":
			return parts[6] == "start" || parts[6] == "close"
		case "releases":
			return parts[6] == "freeze" || parts[6] == "sign-off"
		}
	case "DELETE":
		return parts[4] == "releases" && parts[6] == "freeze"
	}
	return false
}

// revisionHeaders documents the strong ETag returned by create and update.
func revisionHeaders(method, path string) string {
	if strings.EqualFold(method, "DELETE") && !isLifecycleMutation(method, path) {
		return ""
	}
	if !isRevisionedCreate(method, path) && !isRevisionedMutation(method, path) && !isLifecycleMutation(method, path) {
		return ""
	}
	return "    headers:\n      ETag:\n        description: The new revision as a strong ETag.\n        schema: { type: string }\n"
}

func revisionParameters(method, path string) string {
	if !isRevisionedMutation(method, path) && !isLifecycleMutation(method, path) && !isTestManagementMutation(method, path) {
		return ""
	}
	return `parameters:
  - {name: If-Match, in: header, required: false, schema: {type: string}, description: 'Current revision ("N", W/"N", or *). Required unless expected_version is supplied in the query or body.'}
  - {name: expected_version, in: query, required: false, schema: {type: integer, format: int64, minimum: 1}, description: Current revision; alternative to If-Match.}
`
}

// auditQueryParameters documents the admin audit filters. Other list routes
// deliberately keep the generic success shape until they gain a typed schema.
func auditQueryParameters(path string) string {
	isList := strings.HasSuffix(path, "/admin/audit/events")
	isExport := strings.HasSuffix(path, "/admin/audit/export")
	if !isList && !isExport {
		return ""
	}
	var b strings.Builder
	b.WriteString("parameters:\n")
	b.WriteString("  - {name: action, in: query, schema: {type: string, maxLength: 100}, description: Action prefix}\n")
	b.WriteString("  - {name: actor_id, in: query, schema: {type: string, format: uuid}}\n")
	b.WriteString("  - {name: project_id, in: query, schema: {type: string, format: uuid}}\n")
	b.WriteString("  - {name: result, in: query, schema: {type: string, enum: [success, failure, denied]}}\n")
	b.WriteString("  - {name: from, in: query, schema: {type: string}, description: RFC3339 or YYYY-MM-DD}\n")
	b.WriteString("  - {name: to, in: query, schema: {type: string}, description: RFC3339 or YYYY-MM-DD; a date is inclusive of the whole day}\n")
	b.WriteString("  - {name: cursor, in: query, schema: {type: string}, description: Opaque keyset cursor}\n")
	b.WriteString("  - {name: limit, in: query, schema: {type: integer, minimum: 1, maximum: 200, default: 50}}\n")
	if isExport {
		b.WriteString("  - {name: format, in: query, schema: {type: string, enum: [csv, json], default: csv}}\n")
	}
	return b.String()
}

func securityFor(auth server.AuthLevel) string {
	if auth == server.AuthPublic {
		return "security: []\n"
	}
	if auth == server.AuthSSETicket {
		return "security:\n  - bearerAuth: []\n  - streamTicket: []\n"
	}
	return "security:\n  - bearerAuth: []\n"
}

func successCode(method, path string) string {
	switch strings.ToUpper(method) {
	case "DELETE":
		if isRevisionedPath(path) || isTestManagementMutation(method, path) {
			return "200"
		}
		return "204"
	case "POST":
		switch {
		case strings.HasSuffix(path, "/auth/login"),
			strings.HasSuffix(path, "/auth/stream-ticket"),
			strings.HasSuffix(path, "/ai/ask"),
			strings.HasSuffix(path, "/provision"),
			strings.HasSuffix(path, "/watch"),
			strings.Contains(path, "/pull-requests/") && strings.HasSuffix(path, "/merge"),
			strings.Contains(path, "/test-plans/") && strings.HasSuffix(path, "/cases"),
			strings.HasSuffix(path, "/test-runs/{runId}/bugs"):
			return "200"
		}
		return "201"
	default:
		return "200"
	}
}

func errorCodes(route server.Route, path string) []string {
	codes := []string{}
	hasBody := strings.EqualFold(route.Method, "POST") || strings.EqualFold(route.Method, "PUT") || strings.EqualFold(route.Method, "PATCH")
	if hasBody && requestSchema(route.Method, path) != "" {
		codes = append(codes, "400")
	}
	if route.Auth != server.AuthPublic || strings.Contains(path, "/webhooks/") {
		codes = append(codes, "401")
	}
	switch route.Auth {
	case server.AuthAdmin, server.AuthMember, server.AuthProjectRead, server.AuthProjectWrite, server.AuthProjectAdmin, server.AuthSSETicket:
		codes = append(codes, "403")
	}
	if strings.Contains(path, "{") {
		codes = append(codes, "404")
	}
	if strings.HasSuffix(path, "/merge") {
		codes = append(codes, "422")
	}
	if isFileUpload(route.Method, path) ||
		(strings.EqualFold(route.Method, "POST") && strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/links")) {
		codes = appendUnique(codes, "422")
	}
	if strings.HasSuffix(path, "/test-runs/{runId}/finish") {
		codes = appendUnique(codes, "409")
	}
	if successCode(route.Method, path) == "201" && !strings.Contains(path, "/auth/login") {
		codes = appendUnique(codes, "409")
	}
	if isRevisionedMutation(route.Method, path) || isLifecycleMutation(route.Method, path) || isTestManagementMutation(route.Method, path) {
		codes = appendUnique(codes, "400", "409", "428")
	}
	if strings.Contains(path, "/auth/login") {
		codes = append(codes, "429")
	}
	codes = append(codes, "500")
	sort.Strings(codes)
	return codes
}

func appendUnique(codes []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, existing := range codes {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			codes = append(codes, value)
		}
	}
	return codes
}

func requestSchema(method, path string) string {
	switch strings.ToUpper(method) {
	case "GET", "DELETE", "HEAD", "OPTIONS":
		return ""
	}
	switch {
	case strings.HasSuffix(path, "/auth/stream-ticket"), strings.HasSuffix(path, "/provision"):
		return ""
	case strings.HasSuffix(path, "/auth/login"):
		return "LoginInput"
	case strings.Contains(path, "/webhooks/"):
		return "WebhookPayload"
	case strings.HasSuffix(path, "/ai/ask"):
		return "AIAskInput"
	case strings.HasSuffix(path, "/auth/me"):
		return "ProfileUpdateInput"
	case strings.HasSuffix(path, "/auth/password"):
		return "ChangePasswordInput"
	case strings.HasSuffix(path, "/admin/users"):
		return "AdminUserInput"
	case strings.Contains(path, "/admin/users/"):
		return "AdminUserInput"
	case strings.HasSuffix(path, "/members"):
		return "MemberInput"
	case strings.Contains(path, "/members/"):
		return "MemberInput"
	case strings.HasSuffix(path, "/settings"):
		return "ProjectSettings"
	case strings.HasSuffix(path, "/prds"), strings.Contains(path, "/prds/"):
		return "PRDInput"
	case strings.HasSuffix(path, "/epics"), strings.Contains(path, "/epics/"):
		return "EpicInput"
	case strings.HasSuffix(path, "/features"), strings.Contains(path, "/features/"):
		return "FeatureInput"
	case strings.Contains(path, "/comments/"), strings.HasSuffix(path, "/comments"):
		return "CommentInput"
	case strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/custom-fields"):
		return "CustomFieldValuesInput"
	case strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/links"):
		return "WorkItemLinkInput"
	case strings.HasSuffix(path, "/attachments"), strings.HasSuffix(path, "/watch"):
		return ""
	case strings.HasSuffix(path, "/labels"), strings.Contains(path, "/labels/"):
		return "ProjectLabelInput"
	case strings.HasSuffix(path, "/custom-fields"), strings.Contains(path, "/custom-fields/"):
		return "CustomFieldInput"
	case strings.HasSuffix(path, "/saved-filters"), strings.Contains(path, "/saved-filters/"):
		return "SavedFilterInput"
	case strings.HasSuffix(path, "/work-items"), strings.Contains(path, "/work-items/"):
		if strings.HasSuffix(path, "/status") {
			return "StatusInput"
		}
		return "WorkItemInput"
	case strings.HasSuffix(path, "/tickets"), strings.Contains(path, "/tickets/"):
		if strings.HasSuffix(path, "/state") {
			return "StateInput"
		}
		return "TicketInput"
	case strings.HasSuffix(path, "/sprints/:sprintId/start"), strings.HasSuffix(path, "/sprints/{sprintId}/start"):
		return "SprintStartInput"
	case strings.HasSuffix(path, "/sprints/{sprintId}/close"):
		return "SprintCloseInput"
	case strings.HasSuffix(path, "/releases/{releaseId}/freeze"):
		return "ReleaseFreezeInput"
	case strings.HasSuffix(path, "/releases/{releaseId}/exceptions"):
		return "ReleaseExceptionInput"
	case strings.HasSuffix(path, "/releases/{releaseId}/sign-off"):
		return "ReleaseSignOffInput"
	case strings.HasSuffix(path, "/sprints"), strings.Contains(path, "/sprints/"):
		return "SprintInput"
	case strings.HasSuffix(path, "/releases"), strings.Contains(path, "/releases/"):
		return "ReleaseInput"
	case strings.Contains(path, "/test-plans"):
		switch {
		case strings.HasSuffix(path, "/suite-order"):
			return "SuiteOrderInput"
		case strings.HasSuffix(path, "/cases"):
			return "SuiteCasesInput"
		case strings.HasSuffix(path, "/suites"), strings.Contains(path, "/suites/"):
			return "TestSuiteInput"
		default:
			return "TestPlanInput"
		}
	case strings.HasSuffix(path, "/test-runs/import"):
		return "TestImport"
	case strings.HasSuffix(path, "/test-runs/{runId}/bugs"):
		return "BulkBugInput"
	case strings.HasSuffix(path, "/test-runs/{runId}/finish"):
		return "TestRunFinishInput"
	case strings.HasSuffix(path, "/test-runs"), strings.Contains(path, "/test-runs/"):
		switch {
		case strings.HasSuffix(path, "/bug"):
			return "BugFromResultInput"
		case strings.Contains(path, "/steps/"):
			return "TestStepResultInput"
		case strings.Contains(path, "/results/"):
			return "TestResultInput"
		}
		return "TestRunInput"
	case strings.HasSuffix(path, "/test-cases/import"):
		return "TestCaseImportInput"
	case strings.HasSuffix(path, "/test-shared-steps"), strings.Contains(path, "/test-shared-steps/"):
		return "TestSharedStepInput"
	case strings.HasSuffix(path, "/test-cases"), strings.Contains(path, "/test-cases/"):
		return "TestCaseInput"
	case strings.HasSuffix(path, "/repositories"), strings.Contains(path, "/repositories/"):
		if strings.HasSuffix(path, "/provision") {
			return ""
		}
		return "RepositoryInput"
	case strings.HasSuffix(path, "/branches"):
		return "BranchInput"
	case strings.HasSuffix(path, "/pull-requests"):
		return "PullRequestInput"
	case strings.Contains(path, "/pull-requests/") && strings.HasSuffix(path, "/merge"):
		return "MergeInput"
	case strings.Contains(path, "/pull-requests/"):
		return "PullRequestInput"
	case strings.HasSuffix(path, "/environments"):
		return "EnvironmentInput"
	case strings.HasSuffix(path, "/links"):
		return "LinkInput"
	case strings.HasSuffix(path, "/projects"):
		return "ProjectInput"
	case strings.Contains(path, "/projects/") && !strings.Contains(path, "/ai/") && method == "PATCH":
		return "ProjectUpdateInput"
	}
	return ""
}

func successSchema(method, path string) string {
	switch {
	case isAttachmentDownload(method, path):
		return ""
	case strings.HasSuffix(path, "/sprints/{sprintId}/metrics"):
		return "SprintMetrics"
	case strings.HasSuffix(path, "/sprints/{sprintId}/close-preview"):
		return "SprintClosePreview"
	case strings.HasSuffix(path, "/sprints/{sprintId}/start"):
		return "SprintLifecycleResult"
	case strings.HasSuffix(path, "/sprints/{sprintId}/close"):
		return "SprintCloseResult"
	case strings.HasSuffix(path, "/releases/{releaseId}/freeze"):
		return "ReleaseFreezeResult"
	case strings.HasSuffix(path, "/releases/{releaseId}/exceptions") && strings.EqualFold(method, "POST"):
		return "ReleaseScopeException"
	case strings.HasSuffix(path, "/releases/{releaseId}/exceptions"):
		return "ReleaseExceptionList"
	case strings.HasSuffix(path, "/releases/{releaseId}/sign-off"):
		return "ReleaseSignOffResult"
	case strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/custom-fields"):
		return "CustomFieldValueList"
	case strings.HasSuffix(path, "/labels"):
		if strings.EqualFold(method, "GET") {
			return "ProjectLabelList"
		}
		return "ProjectLabel"
	case strings.Contains(path, "/labels/"):
		return "ProjectLabel"
	case strings.HasSuffix(path, "/custom-fields"):
		if strings.EqualFold(method, "GET") {
			return "CustomFieldList"
		}
		return "CustomField"
	case strings.Contains(path, "/custom-fields/"):
		return "CustomField"
	case strings.HasSuffix(path, "/saved-filters"):
		if strings.EqualFold(method, "GET") {
			return "SavedFilterList"
		}
		return "SavedFilter"
	case strings.Contains(path, "/saved-filters/"):
		return "SavedFilter"
	case isWorkItemDetailPath(method, path):
		return "WorkItemDetail"
	case strings.Contains(path, "/comments/"), strings.HasSuffix(path, "/comments"):
		return "Comment"
	case strings.HasSuffix(path, "/watch"):
		return "WatchState"
	case isAttachmentUpload(method, path):
		return "Attachment"
	case strings.Contains(path, "/work-items/") && strings.HasSuffix(path, "/links"):
		return "LinkedArtifact"
	case strings.EqualFold(method, "POST") && strings.Contains(path, "/test-plans/") && strings.HasSuffix(path, "/cases"):
		return "SuiteCasesResult"
	case strings.HasSuffix(path, "/test-cases/export"):
		return "TestCaseExport"
	case strings.HasSuffix(path, "/test-cases/import"):
		return "TestCaseImportResult"
	case strings.Contains(path, "/test-cases/") && strings.HasSuffix(path, "/versions"):
		return "TestCaseVersionList"
	case strings.HasSuffix(path, "/test-runs/{runId}/finish"):
		return "TestRunFinishResult"
	case strings.HasSuffix(path, "/test-runs/{runId}/bugs"):
		return "BulkBugResult"
	case strings.HasSuffix(path, "/test-runs/{runId}/results/{resultId}/bug"):
		return "BugCreateResult"
	case isEvidenceUpload(method, path):
		return "TestStepEvidence"
	case isTestManagementCreate(method, path) || isTestManagementMutation(method, path):
		return "RevisionResult"
	case isRevisionedCreate(method, path) || isRevisionedMutation(method, path):
		return "RevisionResult"
	case strings.HasSuffix(path, "/auth/login"):
		return "LoginResponse"
	case strings.HasSuffix(path, "/auth/me"):
		return "User"
	case strings.HasSuffix(path, "/admin/audit/events"):
		return "AuditPage"
	case strings.Contains(path, "/admin/audit/events/"):
		return "AuditEvent"
	case strings.HasSuffix(path, "/admin/audit/verify"):
		return "AuditVerification"
	case strings.HasSuffix(path, "/admin/users"), strings.HasSuffix(path, "/users"):
		return "User"
	case strings.HasSuffix(path, "/search"):
		return "SearchResponse"
	case strings.HasSuffix(path, "/workspace"):
		return "Workspace"
	case strings.HasSuffix(path, "/traceability"):
		return "Traceability"
	case strings.HasSuffix(path, "/settings"):
		return "ProjectSettings"
	case strings.HasSuffix(path, "/members"):
		return "Member"
	case strings.HasSuffix(path, "/projects"):
		return "ProjectSummary"
	}
	return ""
}

func tagFor(path string) string {
	switch {
	case strings.HasPrefix(path, "/health"):
		return "System"
	case strings.Contains(path, "/admin/"):
		return "Admin"
	case strings.HasPrefix(path, "/api/v1/auth"), strings.HasSuffix(path, "/users"):
		return "Auth"
	case strings.HasSuffix(path, "/search"):
		return "Search"
	case strings.Contains(path, "/ai/"):
		return "AI"
	case strings.Contains(path, "/webhooks/"):
		return "Webhooks"
	case strings.Contains(path, "/prds"), strings.Contains(path, "/epics"),
		strings.Contains(path, "/features"), strings.Contains(path, "/work-items"),
		strings.Contains(path, "/sprints"), strings.Contains(path, "/releases"),
		strings.Contains(path, "/tickets"), strings.Contains(path, "/comments"),
		strings.Contains(path, "/attachments"), strings.Contains(path, "/labels"),
		strings.Contains(path, "/custom-fields"), strings.Contains(path, "/saved-filters"):
		return "Planning"
	case strings.Contains(path, "/test-"):
		return "Tests"
	case strings.Contains(path, "/repositories"), strings.Contains(path, "/branches"),
		strings.Contains(path, "/pull-requests"):
		return "Code"
	case strings.Contains(path, "/environments"), strings.Contains(path, "/links"):
		return "Delivery"
	case strings.Contains(path, "/reports"), strings.Contains(path, "/release-readiness"):
		return "Reports"
	default:
		return "Projects"
	}
}

var summaryOverrides = map[string]string{
	"GET /health":                                                                                   "Health probe",
	"GET /health/live":                                                                              "Liveness probe",
	"GET /health/ready":                                                                             "Readiness probe",
	"POST /api/v1/auth/login":                                                                       "Sign in",
	"GET /api/v1/auth/me":                                                                           "Get the current user",
	"PATCH /api/v1/auth/me":                                                                         "Update the current profile",
	"POST /api/v1/auth/password":                                                                    "Change the current password",
	"POST /api/v1/auth/stream-ticket":                                                               "Issue a stream ticket",
	"GET /api/v1/users":                                                                             "List directory users",
	"GET /api/v1/search":                                                                            "Search artifacts",
	"GET /api/v1/admin/users":                                                                       "List users",
	"POST /api/v1/admin/users":                                                                      "Create a user",
	"PATCH /api/v1/admin/users/{userId}":                                                            "Update a user",
	"GET /api/v1/admin/audit/events":                                                                "List audit events",
	"GET /api/v1/admin/audit/events/{auditId}":                                                      "Get an audit event",
	"GET /api/v1/admin/audit/export":                                                                "Export audit events",
	"GET /api/v1/admin/audit/verify":                                                                "Verify the audit hash chain",
	"GET /api/v1/projects":                                                                          "List projects",
	"POST /api/v1/projects":                                                                         "Create a project",
	"GET /api/v1/projects/{id}":                                                                     "Get a project",
	"PATCH /api/v1/projects/{id}":                                                                   "Update a project",
	"GET /api/v1/projects/{id}/workspace":                                                           "Get the project workspace",
	"GET /api/v1/projects/{id}/activity":                                                            "List project activity",
	"GET /api/v1/projects/{id}/traceability":                                                        "Get traceability",
	"GET /api/v1/projects/{id}/reports":                                                             "Get project reports",
	"GET /api/v1/projects/{id}/reports/export":                                                      "Export a report",
	"GET /api/v1/projects/{id}/release-readiness":                                                   "Get release readiness",
	"GET /api/v1/projects/{id}/settings":                                                            "Get project settings",
	"PUT /api/v1/projects/{id}/settings":                                                            "Update project settings",
	"GET /api/v1/projects/{id}/members":                                                             "List project members",
	"POST /api/v1/projects/{id}/members":                                                            "Add a project member",
	"PATCH /api/v1/projects/{id}/members/{userId}":                                                  "Update a project member",
	"DELETE /api/v1/projects/{id}/members/{userId}":                                                 "Remove a project member",
	"GET /api/v1/projects/{id}/stream":                                                              "Stream project events",
	"GET /api/v1/projects/{id}/ai/search":                                                           "Search with AI",
	"POST /api/v1/projects/{id}/ai/ask":                                                             "Ask a question",
	"GET /api/v1/projects/{id}/ai/insights":                                                         "Get AI insights",
	"POST /api/v1/projects/{id}/pull-requests/{prId}/merge":                                         "Merge a pull request",
	"POST /api/v1/projects/{id}/repositories/{repoId}/provision":                                    "Provision a repository",
	"POST /api/v1/projects/{id}/test-runs/import":                                                   "Import test results",
	"POST /api/v1/projects/{id}/repositories":                                                       "Create a repository",
	"POST /api/v1/projects/{id}/branches":                                                           "Create a branch",
	"POST /api/v1/projects/{id}/pull-requests":                                                      "Create a pull request",
	"PUT /api/v1/projects/{id}/pull-requests/{prId}":                                                "Update a pull request",
	"POST /api/v1/projects/{id}/environments":                                                       "Create an environment",
	"POST /api/v1/projects/{id}/links":                                                              "Create a link",
	"DELETE /api/v1/projects/{id}/links/{linkId}":                                                   "Delete a link",
	"POST /api/v1/projects/{id}/tickets":                                                            "Create a ticket",
	"GET /api/v1/projects/{id}/work-items/{itemId}":                                                 "Get a work item detail",
	"POST /api/v1/projects/{id}/work-items/{itemId}/comments":                                       "Comment on a work item",
	"PATCH /api/v1/projects/{id}/comments/{commentId}":                                              "Edit a comment",
	"DELETE /api/v1/projects/{id}/comments/{commentId}":                                             "Delete a comment",
	"POST /api/v1/projects/{id}/work-items/{itemId}/watch":                                          "Watch a work item",
	"DELETE /api/v1/projects/{id}/work-items/{itemId}/watch":                                        "Stop watching a work item",
	"POST /api/v1/projects/{id}/work-items/{itemId}/attachments":                                    "Upload a work item attachment",
	"GET /api/v1/projects/{id}/attachments/{attachmentId}/download":                                 "Download an attachment",
	"DELETE /api/v1/projects/{id}/attachments/{attachmentId}":                                       "Delete an attachment",
	"POST /api/v1/projects/{id}/work-items/{itemId}/links":                                          "Link a work item",
	"DELETE /api/v1/projects/{id}/work-items/{itemId}/links/{linkId}":                               "Unlink a work item",
	"POST /api/v1/projects/{id}/test-plans":                                                         "Create a test plan",
	"PUT /api/v1/projects/{id}/test-plans/{planId}":                                                 "Update a test plan",
	"PUT /api/v1/projects/{id}/test-plans/{planId}/suite-order":                                     "Reorder test suites",
	"POST /api/v1/projects/{id}/test-plans/{planId}/suites":                                         "Create a test suite",
	"PUT /api/v1/projects/{id}/test-plans/{planId}/suites/{suiteId}":                                "Update a test suite",
	"POST /api/v1/projects/{id}/test-plans/{planId}/suites/{suiteId}/cases":                         "Add cases to a test suite",
	"DELETE /api/v1/projects/{id}/test-plans/{planId}/suites/{suiteId}/cases/{caseId}":              "Remove a case from a test suite",
	"POST /api/v1/projects/{id}/test-cases":                                                         "Create a test case",
	"GET /api/v1/projects/{id}/test-cases/export":                                                   "Export test cases",
	"POST /api/v1/projects/{id}/test-cases/import":                                                  "Import test cases",
	"GET /api/v1/projects/{id}/test-cases/{caseId}/versions":                                        "List test case versions",
	"POST /api/v1/projects/{id}/test-shared-steps":                                                  "Create a shared step",
	"PUT /api/v1/projects/{id}/test-shared-steps/{sharedStepId}":                                    "Update a shared step",
	"DELETE /api/v1/projects/{id}/test-shared-steps/{sharedStepId}":                                 "Delete a shared step",
	"POST /api/v1/projects/{id}/test-runs":                                                          "Create a test run",
	"POST /api/v1/projects/{id}/test-runs/{runId}/finish":                                           "Finish a test run",
	"POST /api/v1/projects/{id}/test-runs/{runId}/results/{resultId}/steps/{stepResultId}/evidence": "Upload test step evidence",
	"POST /api/v1/projects/{id}/test-runs/{runId}/results/{resultId}/bug":                           "Create a linked bug",
	"POST /api/v1/projects/{id}/test-runs/{runId}/bugs":                                             "Create bugs from failed results",
	"POST /api/v1/webhooks/gitea":                                                                   "Receive a Gitea webhook",
	"POST /api/v1/webhooks/argocd":                                                                  "Receive an Argo CD webhook",
	"POST /api/v1/webhooks/git/{provider}":                                                          "Receive a Git provider webhook",
	"POST /api/v1/webhooks/ci/{provider}":                                                           "Receive a CI provider webhook",
	"POST /api/v1/webhooks/cd/{provider}":                                                           "Receive a CD provider webhook",
}

func summaryFor(method, path string) string {
	if summary, ok := summaryOverrides[strings.ToUpper(method)+" "+path]; ok {
		return summary
	}
	verb := map[string]string{
		"GET": "Get", "POST": "Create", "PUT": "Update", "PATCH": "Update", "DELETE": "Delete",
	}[strings.ToUpper(method)]
	if strings.ToUpper(method) == "GET" && !strings.Contains(path, "{") {
		verb = "List"
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	resource := "resource"
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] != "" && !strings.HasPrefix(segments[i], "{") {
			resource = segments[i]
			break
		}
	}
	resource = singularResource(strings.ReplaceAll(resource, "-", " "))
	return verb + " " + resource
}

func singularResource(resource string) string {
	switch resource {
	case "branches":
		return "branch"
	case "repositories":
		return "repository"
	case "releases":
		return "release"
	case "policies":
		return "policy"
	case "statuses":
		return "status"
	}
	if strings.HasSuffix(resource, "ies") {
		return strings.TrimSuffix(resource, "ies") + "y"
	}
	if strings.HasSuffix(resource, "s") && !strings.HasSuffix(resource, "ss") {
		return strings.TrimSuffix(resource, "s")
	}
	return resource
}

func operationID(method, path string) string {
	replacer := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ":", "")
	id := strings.ToLower(method) + "_" + strings.Trim(replacer.Replace(path), "_")
	id = regexp.MustCompile(`_+`).ReplaceAllString(id, "_")
	return id
}

func ginToOpenAPI(path string) string {
	return ginParam.ReplaceAllStringFunc(path, func(match string) string {
		return "{" + strings.TrimPrefix(match, ":") + "}"
	})
}

func ensurePathParameters(operation *yaml.Node, path string) {
	params := mapGet(operation, "parameters")
	existing := map[string]bool{}
	if params != nil {
		for _, item := range params.Content {
			if name := mapGet(item, "name"); name != nil {
				existing[name.Value] = true
			}
		}
	}
	for _, match := range openAPIParam.FindAllStringSubmatch(path, -1) {
		if existing[match[1]] {
			continue
		}
		if params == nil {
			params = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			appendMap(operation, "parameters", params)
		}
		params.Content = append(params.Content, nodeFromYAML(fmt.Sprintf(`name: %q
in: path
required: true
schema: { type: string }`, match[1])))
	}
}

// --- YAML node helpers -------------------------------------------------------

func mappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func nodeFromYAML(text string) *yaml.Node {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil {
		panic(err)
	}
	if len(node.Content) == 0 {
		panic("empty YAML fragment")
	}
	return node.Content[0]
}

func mapGet(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func appendMap(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// setMap replaces an existing key or appends it when absent.
func setMap(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	appendMap(mapping, key, value)
}

func removeMap(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func ensureMapEntry(mapping *yaml.Node, key string, value *yaml.Node) {
	if mapGet(mapping, key) == nil {
		appendMap(mapping, key, value)
	}
}
