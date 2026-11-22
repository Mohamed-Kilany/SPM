package workspace

import "testing"

func TestBoardColumnForStatus(t *testing.T) {
	cases := map[string]string{
		"backlog":     "backlog",
		"new":         "backlog",
		"reopened":    "backlog",
		"cancelled":   "backlog",
		"ready":       "ready",
		"triaged":     "ready",
		"in_progress": "in_progress",
		"in_review":   "review",
		"fixed":       "review",
		"in_qa":       "qa",
		"verified":    "qa",
		"done":        "done",
		"closed":      "done",
		"blocked":     "blocked",
		"unknown":     "backlog",
	}
	for status, want := range cases {
		if got := boardColumnForStatus(status); got != want {
			t.Errorf("boardColumnForStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestValidateBoardSettings(t *testing.T) {
	board := BoardSettings{WIPLimits: map[string]int{
		" In_Progress ": 3,
		"qa":            0,
		"done":          5,
	}}
	if err := validateBoardSettings(&board); err != nil {
		t.Fatalf("validateBoardSettings() error = %v", err)
	}
	if len(board.WIPLimits) != 2 {
		t.Fatalf("normalized limits = %+v, want two positive entries", board.WIPLimits)
	}
	if board.WIPLimits["in_progress"] != 3 || board.WIPLimits["done"] != 5 {
		t.Fatalf("normalized limits = %+v", board.WIPLimits)
	}
	if _, ok := board.WIPLimits["qa"]; ok {
		t.Fatalf("zero limit should be removed, got %+v", board.WIPLimits)
	}

	invalid := []BoardSettings{
		{WIPLimits: map[string]int{"epic": 2}},
		{WIPLimits: map[string]int{"qa": -1}},
		{WIPLimits: map[string]int{"qa": 1000}},
	}
	for _, candidate := range invalid {
		if err := validateBoardSettings(&candidate); err == nil {
			t.Fatalf("validateBoardSettings(%+v) succeeded", candidate)
		}
	}

	empty := BoardSettings{}
	if err := validateBoardSettings(&empty); err != nil {
		t.Fatalf("empty policy rejected: %v", err)
	}
	if empty.WIPLimits == nil {
		t.Fatal("empty policy should normalize to an empty map, not nil")
	}
}

func TestDefaultBoardSettingsAreExplicit(t *testing.T) {
	settings := DefaultProjectSettings()
	if settings.Board.WIPLimits == nil {
		t.Fatal("default board WIP limits should be an empty map")
	}
	if settings.Board.WIPHardBlock {
		t.Fatal("hard WIP block must be opt-in")
	}
}

func TestDecodeSettingsReadsBoardPolicy(t *testing.T) {
	settings := decodeSettings([]byte(`{"board":{"wip_limits":{"qa":2,"review":4},"wip_hard_block":true}}`))
	if settings.Board.WIPLimits["qa"] != 2 || settings.Board.WIPLimits["review"] != 4 {
		t.Fatalf("decoded WIP limits = %+v", settings.Board.WIPLimits)
	}
	if !settings.Board.WIPHardBlock {
		t.Fatal("decoded hard block = false, want true")
	}
	// The delivery defaults survive a partial settings document.
	if !settings.MergePolicy.RequireLinkedWorkItem || !settings.MergePolicy.RequirePassingChecks {
		t.Fatalf("merge defaults lost: %+v", settings.MergePolicy)
	}
}
