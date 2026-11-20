package audit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRedactMasksSensitiveKeysRecursively(t *testing.T) {
	input := map[string]any{
		"username": "ada",
		"password": "hunter2",
		"nested": map[string]any{
			"api_token": "abc",
			"note":      "visible",
		},
		"items": []any{
			map[string]any{"Authorization": "Bearer secret", "value": 7},
		},
	}
	out, ok := Redact(input).(map[string]any)
	if !ok {
		t.Fatalf("Redact returned %T, want map", Redact(input))
	}
	if out["username"] != "ada" {
		t.Errorf("username = %v, want ada", out["username"])
	}
	if out["password"] != redactedValue {
		t.Errorf("password = %v, want %s", out["password"], redactedValue)
	}
	nested := out["nested"].(map[string]any)
	if nested["api_token"] != redactedValue {
		t.Errorf("api_token = %v, want redacted", nested["api_token"])
	}
	if nested["note"] != "visible" {
		t.Errorf("note = %v, want visible", nested["note"])
	}
	item := out["items"].([]any)[0].(map[string]any)
	if item["Authorization"] != redactedValue {
		t.Errorf("Authorization = %v, want redacted", item["Authorization"])
	}
	if item["value"] != float64(7) {
		t.Errorf("value = %v, want 7", item["value"])
	}
}

func TestRedactTruncatesLongStrings(t *testing.T) {
	long := strings.Repeat("a", maxStringValue+50)
	out := Redact(map[string]any{"note": long}).(map[string]any)
	note := out["note"].(string)
	if len(note) > maxStringValue {
		t.Fatalf("redacted string length = %d, want <= %d", len(note), maxStringValue)
	}
	if !strings.HasSuffix(note, truncatedSuffix) {
		t.Fatalf("redacted string does not end with %q", truncatedSuffix)
	}
}

func TestRetentionDaysFromEnv(t *testing.T) {
	cases := map[string]int{
		"":      defaultRetention,
		"90":    90,
		"0":     0,
		"-5":    defaultRetention,
		"nope":  defaultRetention,
		"  30 ": 30,
	}
	for raw, want := range cases {
		if got := RetentionDaysFromEnv(raw); got != want {
			t.Errorf("RetentionDaysFromEnv(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestCSVRenderingPrefixesFormulaCells(t *testing.T) {
	events := []Event{{
		ID:         "event-1",
		OccurredAt: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		Action:     "=HYPERLINK(\"http://evil\")",
		TargetID:   "+1",
		Result:     "@SUM(A1)",
		Reason:     "-cmd",
		ActorRole:  "admin",
	}}
	body, err := RenderCSV(events)
	if err != nil {
		t.Fatalf("RenderCSV: %v", err)
	}
	text := string(body)
	for _, expected := range []string{"'=HYPERLINK", "'+1", "'@SUM", "'-cmd"} {
		if !strings.Contains(text, expected) {
			t.Errorf("CSV is missing guarded cell %q:\n%s", expected, text)
		}
	}
	if !strings.Contains(text, "admin") {
		t.Errorf("CSV lost an ordinary cell:\n%s", text)
	}
	if strings.Contains(text, "\x00") {
		t.Error("CSV contains a NUL byte")
	}
}

func TestCursorRoundTripAndValidation(t *testing.T) {
	occurred := time.Date(2026, 11, 20, 10, 30, 0, 12345, time.UTC)
	id := "0199b0c4-7d2a-7c31-9e1a-2b3c4d5e6f70"
	cursor := encodeCursor(occurred, id)
	decoded, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !decoded.occurredAt.Equal(occurred) {
		t.Errorf("occurredAt = %v, want %v", decoded.occurredAt, occurred)
	}
	if decoded.id != id {
		t.Errorf("id = %q, want %q", decoded.id, id)
	}
	for _, bad := range []string{"", "not-base64!", "Zm9v", "MjAyNi0xMS0yMHxub3QtYS11dWlk"} {
		if _, err := decodeCursor(bad); err == nil {
			t.Errorf("decodeCursor(%q) = nil error, want rejection", bad)
		}
	}
}

func TestChainVerifierAcceptsValidChain(t *testing.T) {
	verifier := NewChainVerifier(nil)
	previous := ""
	for i := 1; i <= 3; i++ {
		payload := fmt.Sprintf(`{"event":%d}`, i)
		hash := ChainHash(previous, payload)
		verifier.Add(ChainRow{ID: fmt.Sprintf("row-%d", i), Payload: payload, PrevHash: previous, Hash: hash})
		previous = hash
	}
	result := verifier.Result()
	if !result.OK || result.Checked != 3 {
		t.Fatalf("verification = %+v, want OK with 3 rows", result)
	}
}

func TestChainVerifierDetectsTamperedPayload(t *testing.T) {
	first := ChainHash("", `{"event":1}`)
	second := ChainHash(first, `{"event":2}`)
	verifier := NewChainVerifier(nil)
	verifier.Add(ChainRow{ID: "row-1", Payload: `{"event":1}`, PrevHash: "", Hash: first})
	verifier.Add(ChainRow{ID: "row-2", Payload: `{"event":999}`, PrevHash: first, Hash: second})
	result := verifier.Result()
	if result.OK {
		t.Fatal("tampered payload was accepted")
	}
	if result.FirstBadID != "row-2" || result.FirstBadCause != "hash_mismatch" {
		t.Fatalf("result = %+v, want row-2/hash_mismatch", result)
	}
}

func TestChainVerifierDetectsBrokenLink(t *testing.T) {
	first := ChainHash("", `{"event":1}`)
	secondPayload := `{"event":2}`
	second := ChainHash("deadbeef", secondPayload)
	verifier := NewChainVerifier(nil)
	verifier.Add(ChainRow{ID: "row-1", Payload: `{"event":1}`, PrevHash: "", Hash: first})
	verifier.Add(ChainRow{ID: "row-2", Payload: secondPayload, PrevHash: "deadbeef", Hash: second})
	result := verifier.Result()
	if result.OK || result.FirstBadCause != "broken_link" {
		t.Fatalf("result = %+v, want broken_link", result)
	}
}

func TestChainVerifierUsesRetentionAnchor(t *testing.T) {
	anchor := ChainHash("", `{"old":true}`)
	payload := `{"event":"first-retained"}`
	verified := NewChainVerifier(map[string]struct{}{anchor: {}})
	verified.Add(ChainRow{ID: "row-1", Payload: payload, PrevHash: anchor, Hash: ChainHash(anchor, payload)})
	if result := verified.Result(); !result.OK {
		t.Fatalf("anchored chain rejected: %+v", result)
	}

	missing := NewChainVerifier(nil)
	missing.Add(ChainRow{ID: "row-1", Payload: payload, PrevHash: anchor, Hash: ChainHash(anchor, payload)})
	if result := missing.Result(); result.OK || result.FirstBadCause != "missing_anchor" {
		t.Fatalf("missing anchor result = %+v, want missing_anchor", result)
	}
}

func TestParseFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/events?action=project.&result=SUCCESS&from=2026-01-01&to=2026-01-31&limit=25&actor_id=0199b0c4-7d2a-7c31-9e1a-2b3c4d5e6f70", nil)
	filter, err := parseFilter(context)
	if err != nil {
		t.Fatalf("parseFilter: %v", err)
	}
	if filter.Action != "project." || filter.Result != "success" || filter.Limit != 25 {
		t.Fatalf("filter = %+v, want parsed values", filter)
	}
	if want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC); !filter.To.Equal(want) {
		t.Errorf("To = %v, want %v", filter.To, want)
	}
}

func TestParseFilterRejectsInvalidValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []string{
		"actor_id=nope",
		"project_id=nope",
		"result=maybe",
		"from=yesterday",
		"to=31-01-2026",
		"limit=0",
		"limit=abc",
	}
	for _, query := range cases {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/events?"+query, nil)
		if _, err := parseFilter(context); err == nil {
			t.Errorf("parseFilter(%q) = nil error, want rejection", query)
		}
	}
}

func TestDisabledLoggerIsSafe(t *testing.T) {
	logger := NewLogger(nil)
	if logger.Enabled() {
		t.Fatal("nil pool logger reports enabled")
	}
	if err := logger.Record(t.Context(), Entry{Action: "x"}); err != nil {
		t.Fatalf("disabled Record: %v", err)
	}
	page, err := logger.List(t.Context(), Filter{})
	if err != nil || len(page.Events) != 0 || page.NextCursor != "" {
		t.Fatalf("disabled List = %+v, %v", page, err)
	}
	if _, err := logger.Get(t.Context(), "x"); err != ErrNotFound {
		t.Fatalf("disabled Get error = %v, want ErrNotFound", err)
	}
	result, err := logger.Verify(t.Context())
	if err != nil || !result.OK {
		t.Fatalf("disabled Verify = %+v, %v", result, err)
	}
	if err := logger.EnsurePartitions(t.Context(), 3); err != nil {
		t.Fatalf("disabled EnsurePartitions: %v", err)
	}
	if dropped, err := logger.Prune(t.Context(), 30); err != nil || dropped != 0 {
		t.Fatalf("disabled Prune = %d, %v", dropped, err)
	}
}

func TestRecordRejectsMissingAction(t *testing.T) {
	logger := &Logger{}
	if err := logger.Record(t.Context(), Entry{}); err == nil {
		t.Fatal("Record with an empty action succeeded")
	}
}

func TestExportRejectsUnknownFormat(t *testing.T) {
	logger := NewLogger(nil)
	if _, _, err := logger.Export(t.Context(), Filter{}, "xml"); err == nil {
		t.Fatal("Export accepted an unsupported format")
	}
}

func TestEventJSONUsesBeforeAfterNames(t *testing.T) {
	body, err := json.Marshal(Event{Before: json.RawMessage(`{"role":"developer"}`), After: json.RawMessage(`{"role":"owner"}`)})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"before":{"role":"developer"}`) {
		t.Errorf("event JSON is missing before: %s", text)
	}
	if !strings.Contains(text, `"after":{"role":"owner"}`) {
		t.Errorf("event JSON is missing after: %s", text)
	}
}
