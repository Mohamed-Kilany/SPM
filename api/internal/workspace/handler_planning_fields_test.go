package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeLabelInput(t *testing.T) {
	input, err := normalizeLabelInput(labelRequest{Name: "  Customer   Escalation ", Color: " Blue "})
	if err != nil {
		t.Fatalf("normalizeLabelInput() error = %v", err)
	}
	if input.Name != "customer escalation" || input.Color != "blue" {
		t.Fatalf("normalizeLabelInput() = %+v, want customer escalation/blue", input)
	}

	input, err = normalizeLabelInput(labelRequest{Name: "bug"})
	if err != nil {
		t.Fatalf("default color error = %v", err)
	}
	if input.Color != "slate" {
		t.Fatalf("default color = %q, want slate", input.Color)
	}

	for _, request := range []labelRequest{
		{Name: "", Color: "blue"},
		{Name: "   ", Color: "blue"},
		{Name: "café", Color: "blue"},
		{Name: strings.Repeat("a", maxLabelRunes+1), Color: "blue"},
		{Name: "ok", Color: "chartreuse"},
	} {
		if _, err := normalizeLabelInput(request); err == nil {
			t.Fatalf("normalizeLabelInput(%+v) succeeded, want error", request)
		}
	}
}

func TestSlugifyFieldKey(t *testing.T) {
	cases := map[string]string{
		"Story Points":       "story_points",
		"  Release  Train  ": "release_train",
		"123":                "f_123",
		"":                   "field",
		"---":                "field",
		"Risk / Impact":      "risk_impact",
	}
	for input, want := range cases {
		if got := slugifyFieldKey(input); got != want {
			t.Fatalf("slugifyFieldKey(%q) = %q, want %q", input, got, want)
		}
	}
	if got := slugifyFieldKey(strings.Repeat("a", 60)); len(got) != 40 {
		t.Fatalf("long key length = %d, want 40", len(got))
	}
}

func TestValidateFieldOptions(t *testing.T) {
	options, err := validateFieldOptions("select", []CustomFieldOption{
		{Value: "", Label: "High Priority", Color: "red"},
		{Value: "low", Label: ""},
	})
	if err != nil {
		t.Fatalf("validateFieldOptions() error = %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("options length = %d, want 2", len(options))
	}
	if options[0].Value != "high_priority" || options[0].Label != "High Priority" || options[0].Color != "red" {
		t.Fatalf("normalized first option = %+v", options[0])
	}
	if options[1].Label != "low" {
		t.Fatalf("blank label should fall back to the value, got %q", options[1].Label)
	}

	if _, err := validateFieldOptions("select", nil); err == nil {
		t.Fatal("select without options succeeded")
	}
	if _, err := validateFieldOptions("select", []CustomFieldOption{{Value: "one"}, {Value: "one"}}); err == nil {
		t.Fatal("duplicate option values succeeded")
	}
	if _, err := validateFieldOptions("select", []CustomFieldOption{{Value: "Bad Value"}}); err == nil {
		t.Fatal("invalid option value succeeded")
	}
	if _, err := validateFieldOptions("select", []CustomFieldOption{{Value: "ok", Color: "chartreuse"}}); err == nil {
		t.Fatal("invalid option color succeeded")
	}
	if _, err := validateFieldOptions("text", []CustomFieldOption{{Value: "nope"}}); err == nil {
		t.Fatal("options on a text field succeeded")
	}
	if options, err := validateFieldOptions("number", nil); err != nil || len(options) != 0 {
		t.Fatalf("non-select options = %+v, %v", options, err)
	}
}

func TestValidateFieldConfig(t *testing.T) {
	config, err := validateFieldConfig("number", map[string]any{"min": float64(1), "max": float64(5)})
	if err != nil {
		t.Fatalf("validateFieldConfig() error = %v", err)
	}
	if config["min"] != float64(1) || config["max"] != float64(5) {
		t.Fatalf("config = %+v", config)
	}
	if _, err := validateFieldConfig("number", map[string]any{"min": "one"}); err == nil {
		t.Fatal("string minimum succeeded")
	}
	if _, err := validateFieldConfig("number", map[string]any{"min": float64(5), "max": float64(1)}); err == nil {
		t.Fatal("inverted range succeeded")
	}
	config, err = validateFieldConfig("text", map[string]any{"min": float64(5)})
	if err != nil {
		t.Fatalf("text config error = %v", err)
	}
	if len(config) != 0 {
		t.Fatalf("text config = %+v, want empty", config)
	}
}

func TestParseCustomValue(t *testing.T) {
	field := CustomField{Type: "text"}
	value, err := parseCustomValue(field, json.RawMessage(`"  hello  "`))
	if err != nil || value.text != "hello" || value.empty {
		t.Fatalf("text value = %+v, %v", value, err)
	}
	value, err = parseCustomValue(field, json.RawMessage(`"   "`))
	if err != nil || !value.empty {
		t.Fatalf("blank text value = %+v, %v", value, err)
	}
	if _, err := parseCustomValue(field, json.RawMessage(`12`)); err == nil {
		t.Fatal("number for a text field succeeded")
	}
	if _, err := parseCustomValue(field, json.RawMessage(`"`+strings.Repeat("x", 2001)+`"`)); err == nil {
		t.Fatal("overlong text succeeded")
	}

	field = CustomField{Type: "number", Config: map[string]any{"min": float64(1), "max": float64(10)}}
	value, err = parseCustomValue(field, json.RawMessage(`7.5`))
	if err != nil || value.number == nil || *value.number != 7.5 {
		t.Fatalf("number value = %+v, %v", value, err)
	}
	if _, err := parseCustomValue(field, json.RawMessage(`0`)); err == nil {
		t.Fatal("number below the minimum succeeded")
	}
	if _, err := parseCustomValue(field, json.RawMessage(`11`)); err == nil {
		t.Fatal("number above the maximum succeeded")
	}

	field = CustomField{Type: "select", Options: []CustomFieldOption{{Value: "high"}, {Value: "low"}}}
	value, err = parseCustomValue(field, json.RawMessage(`"high"`))
	if err != nil || value.text != "high" || value.empty {
		t.Fatalf("select value = %+v, %v", value, err)
	}
	if _, err := parseCustomValue(field, json.RawMessage(`"medium"`)); err == nil {
		t.Fatal("unknown select option succeeded")
	}
	value, _ = parseCustomValue(field, json.RawMessage(`""`))
	if !value.empty {
		t.Fatal("empty select option should be empty")
	}

	field = CustomField{Type: "date"}
	value, err = parseCustomValue(field, json.RawMessage(`"2026-11-21"`))
	if err != nil || value.date == nil || !value.date.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date value = %+v, %v", value, err)
	}
	if _, err := parseCustomValue(field, json.RawMessage(`"21/11/2026"`)); err == nil {
		t.Fatal("invalid date succeeded")
	}

	userID := uuid.New()
	field = CustomField{Type: "user"}
	value, err = parseCustomValue(field, json.RawMessage(`"`+userID.String()+`"`))
	if err != nil || value.user == nil || *value.user != userID {
		t.Fatalf("user value = %+v, %v", value, err)
	}
	if _, err := parseCustomValue(field, json.RawMessage(`"not-a-uuid"`)); err == nil {
		t.Fatal("invalid user ID succeeded")
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		for _, fieldType := range []string{"text", "number", "select", "date", "user"} {
			value, err := parseCustomValue(CustomField{Type: fieldType}, raw)
			if err != nil || !value.empty {
				t.Fatalf("%s %s = %+v, %v; want empty", fieldType, raw, value, err)
			}
		}
	}
}

func TestRequiredFieldMissing(t *testing.T) {
	requiredID := uuid.New()
	optionalID := uuid.New()
	archivedID := uuid.New()
	archivedAt := time.Now()
	fields := map[uuid.UUID]CustomField{
		requiredID: {Name: "Story points", Required: true},
		optionalID: {Name: "Notes"},
		archivedID: {Name: "Legacy", Required: true, ArchivedAt: &archivedAt},
	}

	field, missing := requiredFieldMissing(fields, map[uuid.UUID]parsedCustomValue{})
	if !missing || field.Name != "Story points" {
		t.Fatalf("missing = %v, field = %+v", missing, field)
	}
	if _, missing := requiredFieldMissing(fields, map[uuid.UUID]parsedCustomValue{
		requiredID: {text: "3"},
	}); missing {
		t.Fatal("populated required field reported missing")
	}
	if _, missing := requiredFieldMissing(fields, map[uuid.UUID]parsedCustomValue{
		requiredID: {empty: true},
	}); !missing {
		t.Fatal("empty required value not reported missing")
	}
	if _, missing := requiredFieldMissing(fields, map[uuid.UUID]parsedCustomValue{
		optionalID: {empty: true},
	}); !missing {
		t.Fatal("required field with only an optional value not reported missing")
	}
	emptyFields := map[uuid.UUID]CustomField{archivedID: fields[archivedID]}
	if _, missing := requiredFieldMissing(emptyFields, nil); missing {
		t.Fatal("archived required field reported missing")
	}
}

func TestValidateSavedFilter(t *testing.T) {
	request := savedFilterRequest{Name: "  My Board  ", Scope: " BOARD "}
	if err := validateSavedFilter(&request); err != nil {
		t.Fatalf("validateSavedFilter() error = %v", err)
	}
	if request.Name != "My Board" || request.Scope != "board" || request.Visibility != "private" {
		t.Fatalf("normalized filter = %+v", request)
	}
	if request.Filters == nil || request.Sort == nil {
		t.Fatal("default filters/sort should be empty maps")
	}

	invalid := []savedFilterRequest{
		{Name: "", Scope: "board"},
		{Name: strings.Repeat("a", 81), Scope: "board"},
		{Name: "ok", Scope: "calendar"},
		{Name: "ok", Scope: "board", Visibility: "public"},
		{Name: "ok", Scope: "board", Columns: []string{""}},
		{Name: "ok", Scope: "board", Columns: []string{strings.Repeat("a", 41)}},
		{Name: "ok", Scope: "board", Columns: make([]string, 21)},
	}
	for index := range invalid[6].Columns {
		invalid[6].Columns[index] = "status"
	}
	for _, candidate := range invalid {
		if err := validateSavedFilter(&candidate); err == nil {
			t.Fatalf("validateSavedFilter(%+v) succeeded", candidate)
		}
	}

	tooLarge := savedFilterRequest{
		Name: "huge", Scope: "board",
		Filters: map[string]any{"query": strings.Repeat("x", maxSavedFilterBytes)},
	}
	if err := validateSavedFilter(&tooLarge); err == nil {
		t.Fatal("oversized saved filter succeeded")
	}
}

func TestFilterVisibleSavedFilters(t *testing.T) {
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	other := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	filters := []SavedFilter{
		{Name: "private-own", Visibility: "private", OwnerID: owner},
		{Name: "private-other", Visibility: "private", OwnerID: other},
		{Name: "shared", Visibility: "shared", OwnerID: other},
	}

	visible := filterVisibleSavedFilters(filters, &owner, "developer", false)
	if len(visible) != 2 || visible[0].Name != "private-own" || visible[1].Name != "shared" {
		t.Fatalf("owner visible = %+v", visible)
	}
	visible = filterVisibleSavedFilters(filters, &owner, "developer", true)
	if len(visible) != 3 {
		t.Fatalf("admin visible = %d, want 3", len(visible))
	}
	visible = filterVisibleSavedFilters(filters, &owner, "maintainer", false)
	if len(visible) != 3 {
		t.Fatalf("maintainer visible = %d, want 3", len(visible))
	}
	visible = filterVisibleSavedFilters(filters, nil, "viewer", false)
	if len(visible) != 1 || visible[0].Name != "shared" {
		t.Fatalf("viewer visible = %+v", visible)
	}
}

func BenchmarkFilterVisibleSavedFilters(b *testing.B) {
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	other := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	filters := make([]SavedFilter, 1000)
	for index := range filters {
		filters[index] = SavedFilter{
			Name:       "view",
			Visibility: "private",
			OwnerID:    owner,
		}
		if index%2 == 1 {
			filters[index].OwnerID = other
		}
		if index%4 == 0 {
			filters[index].Visibility = "shared"
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		visible := filterVisibleSavedFilters(filters, &owner, "developer", false)
		if len(visible) != 500 {
			b.Fatalf("visible = %d, want 500", len(visible))
		}
	}
}

func TestValidFilterScope(t *testing.T) {
	for _, scope := range []string{"planning", "board", "test"} {
		if !validFilterScope(scope) {
			t.Fatalf("validFilterScope(%q) = false", scope)
		}
	}
	for _, scope := range []string{"", "calendar", "BOARD"} {
		if validFilterScope(scope) {
			t.Fatalf("validFilterScope(%q) = true", scope)
		}
	}
}
