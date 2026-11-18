package sse

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeEvent is a helper to build a simple SSEEvent.
func makeEvent(id int64) SSEEvent {
	return SSEEvent{
		ID:        id,
		EventType: "test",
		Data:      []byte(fmt.Sprintf(`{"seq":%d}`, id)),
	}
}

// TestReplay_SingleEvent inserts 1 event and replays from ID 0.
func TestReplay_SingleEvent(t *testing.T) {
	rb := NewRingBuffer()
	rb.Append(makeEvent(1))

	events, ok := rb.Replay(0)
	require.True(t, ok)
	require.Len(t, events, 1)
	assert.Equal(t, int64(1), events[0].ID)
}

// TestReplay_EmptyBuffer_FromZero returns ([], true) when buffer is empty and fromID == 0.
func TestReplay_EmptyBuffer_FromZero(t *testing.T) {
	rb := NewRingBuffer()

	events, ok := rb.Replay(0)
	assert.True(t, ok)
	assert.Empty(t, events)
}

// TestReplay_EmptyBuffer_NonZero returns (nil, false) when buffer is empty and fromID != 0.
func TestReplay_EmptyBuffer_NonZero(t *testing.T) {
	rb := NewRingBuffer()

	events, ok := rb.Replay(42)
	assert.False(t, ok)
	assert.Nil(t, events)
}

// TestReplay_Overflow verifies that after inserting 501 events the oldest is evicted
// and the buffer holds only the last 500.
func TestReplay_Overflow(t *testing.T) {
	rb := NewRingBuffer()

	// Insert 501 events with IDs 1..501.
	for i := int64(1); i <= 501; i++ {
		rb.Append(makeEvent(i))
	}

	// The buffer should hold events 2..501 (event 1 was evicted).
	// Replaying from ID 0 should trigger out-of-range because minID is 2.
	events, ok := rb.Replay(0)
	assert.False(t, ok, "fromID=0 should be out of range after overflow")
	assert.Nil(t, events)

	// Replaying from ID 1 (== minID-1) should succeed and return events 2..501.
	events, ok = rb.Replay(1)
	require.True(t, ok)
	require.Len(t, events, 500)
	assert.Equal(t, int64(2), events[0].ID)
	assert.Equal(t, int64(501), events[len(events)-1].ID)
}

// TestReplay_OutOfRange returns (nil, false) when fromID is below the buffer's minimum.
func TestReplay_OutOfRange(t *testing.T) {
	rb := NewRingBuffer()

	// Insert events 100..599 (500 items) so minID=100.
	for i := int64(100); i < 600; i++ {
		rb.Append(makeEvent(i))
	}

	// fromID=0 is well below minID-1 (99), so it should be out of range.
	events, ok := rb.Replay(0)
	assert.False(t, ok)
	assert.Nil(t, events)
}

// TestReplay_NoNewEvents returns ([], true) when fromID equals the last event ID.
func TestReplay_NoNewEvents(t *testing.T) {
	rb := NewRingBuffer()

	for i := int64(1); i <= 5; i++ {
		rb.Append(makeEvent(i))
	}

	events, ok := rb.Replay(5)
	assert.True(t, ok)
	assert.Empty(t, events)
}

// TestReplay_PartialRange returns only events after fromID.
func TestReplay_PartialRange(t *testing.T) {
	rb := NewRingBuffer()

	for i := int64(1); i <= 10; i++ {
		rb.Append(makeEvent(i))
	}

	events, ok := rb.Replay(5)
	require.True(t, ok)
	require.Len(t, events, 5)
	for i, ev := range events {
		assert.Equal(t, int64(6+i), ev.ID)
	}
}

// TestAppend_Concurrent verifies no data race when multiple goroutines call Append.
// Run with: go test -race ./internal/sse/...
func TestAppend_Concurrent(t *testing.T) {
	rb := NewRingBuffer()

	const goroutines = 50
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for e := 0; e < eventsPerGoroutine; e++ {
				id := int64(g*eventsPerGoroutine + e + 1)
				rb.Append(makeEvent(id))
			}
		}()
	}

	wg.Wait()

	// After 1000 concurrent appends the buffer should be full (size == BufferSize).
	rb.mu.RLock()
	size := rb.size
	rb.mu.RUnlock()

	assert.Equal(t, BufferSize, size)
}

// TestReplay_OrderPreserved verifies that Replay returns events in insertion order.
func TestReplay_OrderPreserved(t *testing.T) {
	rb := NewRingBuffer()

	for i := int64(1); i <= 10; i++ {
		rb.Append(makeEvent(i))
	}

	events, ok := rb.Replay(0)
	require.True(t, ok)
	require.Len(t, events, 10)

	for i, ev := range events {
		assert.Equal(t, int64(i+1), ev.ID, "events should be in insertion order")
	}
}
