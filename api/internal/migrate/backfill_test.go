package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeBackfillStore struct {
	progress map[string]BackfillProgress
	saves    []BackfillProgress
	loadErr  error
	saveErr  error
}

func newFakeBackfillStore() *fakeBackfillStore {
	return &fakeBackfillStore{progress: map[string]BackfillProgress{}}
}

func (s *fakeBackfillStore) Load(_ context.Context, name string) (BackfillProgress, error) {
	if s.loadErr != nil {
		return BackfillProgress{}, s.loadErr
	}
	return s.progress[name], nil
}

func (s *fakeBackfillStore) Save(_ context.Context, name string, progress BackfillProgress) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.progress[name] = progress
	s.saves = append(s.saves, progress)
	return nil
}

func TestRunBackfillCompletesAndPersists(t *testing.T) {
	store := newFakeBackfillStore()
	var cursors []string
	fn := func(_ context.Context, cursor string, limit int) (string, int, error) {
		if limit != 2 {
			t.Errorf("limit = %d, want 2", limit)
		}
		cursors = append(cursors, cursor)
		switch cursor {
		case "":
			return "2", 2, nil
		case "2":
			return "4", 2, nil
		default:
			return "", 0, nil
		}
	}

	result, err := RunBackfill(context.Background(), store, "revision-backfill", 2, 0, fn)
	if err != nil {
		t.Fatalf("RunBackfill: %v", err)
	}
	if result.Batches != 3 || result.Processed != 4 {
		t.Errorf("result = %+v, want 3 batches and 4 rows", result)
	}
	if !result.Completed || result.Cursor != "" {
		t.Errorf("result = %+v, want completed with an empty cursor", result)
	}
	if strings.Join(cursors, ",") != ",2,4" {
		t.Errorf("cursors = %v, want empty, 2, 4", cursors)
	}
	if len(store.saves) != 3 || !store.saves[len(store.saves)-1].Completed {
		t.Errorf("saves = %+v, want two checkpoints and a completion", store.saves)
	}
}

func TestRunBackfillResumesFromCursor(t *testing.T) {
	store := newFakeBackfillStore()
	store.progress["revision-backfill"] = BackfillProgress{Cursor: "40"}

	fn := func(_ context.Context, cursor string, _ int) (string, int, error) {
		if cursor != "40" {
			t.Fatalf("callback cursor = %q, want 40", cursor)
		}
		return "60", 10, nil
	}

	result, err := RunBackfill(context.Background(), store, "revision-backfill", 10, 1, fn)
	if err != nil {
		t.Fatalf("RunBackfill: %v", err)
	}
	if result.Batches != 1 || result.Cursor != "60" || result.Completed {
		t.Errorf("result = %+v, want one incomplete batch at 60", result)
	}
	if got := store.progress["revision-backfill"]; got.Cursor != "60" || got.Completed {
		t.Errorf("stored progress = %+v, want cursor 60", got)
	}
}

func TestRunBackfillSkipsCompletedBackfill(t *testing.T) {
	store := newFakeBackfillStore()
	store.progress["done"] = BackfillProgress{Completed: true}
	called := false
	result, err := RunBackfill(context.Background(), store, "done", 10, 0, func(context.Context, string, int) (string, int, error) {
		called = true
		return "", 0, nil
	})
	if err != nil {
		t.Fatalf("RunBackfill: %v", err)
	}
	if called {
		t.Error("callback ran for a completed backfill")
	}
	if !result.Completed {
		t.Errorf("result = %+v, want completed", result)
	}
}

func TestRunBackfillRejectsStalledCursor(t *testing.T) {
	store := newFakeBackfillStore()
	store.progress["stalled"] = BackfillProgress{Cursor: "10"}
	_, err := RunBackfill(context.Background(), store, "stalled", 10, 0, func(context.Context, string, int) (string, int, error) {
		return "10", 5, nil
	})
	if err == nil || !strings.Contains(err.Error(), "cursor did not advance") {
		t.Fatalf("RunBackfill error = %v, want stalled cursor", err)
	}
}

func TestRunBackfillStopsOnCallbackError(t *testing.T) {
	store := newFakeBackfillStore()
	boom := errors.New("boom")
	_, err := RunBackfill(context.Background(), store, "failing", 10, 0, func(context.Context, string, int) (string, int, error) {
		return "", 0, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("RunBackfill error = %v, want boom", err)
	}
}

func TestRunBackfillPropagatesStoreErrors(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		store := newFakeBackfillStore()
		store.loadErr = errors.New("load failed")
		if _, err := RunBackfill(context.Background(), store, "x", 10, 0, func(context.Context, string, int) (string, int, error) { return "", 0, nil }); err == nil {
			t.Fatal("RunBackfill ignored the load error")
		}
	})
	t.Run("save", func(t *testing.T) {
		store := newFakeBackfillStore()
		store.saveErr = errors.New("save failed")
		if _, err := RunBackfill(context.Background(), store, "x", 10, 0, func(context.Context, string, int) (string, int, error) { return "2", 1, nil }); err == nil {
			t.Fatal("RunBackfill ignored the save error")
		}
	})
}

func TestRunBackfillValidation(t *testing.T) {
	valid := func(context.Context, string, int) (string, int, error) { return "", 0, nil }
	tests := []struct {
		name      string
		store     BackfillStore
		backfill  string
		batchSize int
		fn        BackfillFunc
	}{
		{name: "nil store", backfill: "x", batchSize: 1, fn: valid},
		{name: "empty name", store: newFakeBackfillStore(), batchSize: 1, fn: valid},
		{name: "zero batch", store: newFakeBackfillStore(), backfill: "x", fn: valid},
		{name: "nil function", store: newFakeBackfillStore(), backfill: "x", batchSize: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RunBackfill(context.Background(), test.store, test.backfill, test.batchSize, 0, test.fn); err == nil {
				t.Fatal("RunBackfill accepted invalid input")
			}
		})
	}
}

func TestPostgresBackfillStoreRejectsNilPool(t *testing.T) {
	store := NewPostgresBackfillStore(nil)
	if _, err := store.Load(context.Background(), "x"); err == nil {
		t.Error("Load accepted a nil pool")
	}
	if err := store.Save(context.Background(), "x", BackfillProgress{}); err == nil {
		t.Error("Save accepted a nil pool")
	}
	var nilStore *PostgresBackfillStore
	if _, err := nilStore.Load(context.Background(), "x"); err == nil {
		t.Error("nil store Load did not fail")
	}
	if err := nilStore.Save(context.Background(), "x", BackfillProgress{}); err == nil {
		t.Error("nil store Save did not fail")
	}
}
