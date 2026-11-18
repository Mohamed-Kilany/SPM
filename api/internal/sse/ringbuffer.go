package sse

import "sync"

// BufferSize is the per-project replay window.
const BufferSize = 500

// SSEEvent is a single SSE event stored in the ring buffer.
type SSEEvent struct {
	ID        int64
	EventType string
	Data      []byte
}

// RingBuffer is a fixed-size, concurrency-safe circular buffer of SSEEvents.
type RingBuffer struct {
	mu   sync.RWMutex
	buf  [BufferSize]SSEEvent
	head int
	size int
}

// NewRingBuffer creates an empty RingBuffer.
func NewRingBuffer() *RingBuffer { return &RingBuffer{} }

// Append adds an event, overwriting the oldest entry when full.
func (rb *RingBuffer) Append(event SSEEvent) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf[rb.head] = event
	rb.head = (rb.head + 1) % BufferSize
	if rb.size < BufferSize {
		rb.size++
	}
}

// Replay returns events with ID > fromID in insertion order.
// inRange is false when the requested ID cannot be satisfied from this buffer.
func (rb *RingBuffer) Replay(fromID int64) (events []SSEEvent, inRange bool) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		if fromID <= 0 {
			return []SSEEvent{}, true
		}
		return nil, false
	}

	tail := (rb.head - rb.size + BufferSize) % BufferSize
	minID := rb.buf[tail].ID
	maxID := rb.buf[(rb.head-1+BufferSize)%BufferSize].ID

	// The client is ahead of the server (stale/wrong project ID) or older than
	// the retained window. Either way it needs a full snapshot.
	if fromID > maxID || fromID < minID-1 {
		return nil, false
	}
	if fromID == maxID {
		return []SSEEvent{}, true
	}

	result := make([]SSEEvent, 0, rb.size)
	for i := 0; i < rb.size; i++ {
		ev := rb.buf[(tail+i)%BufferSize]
		if ev.ID > fromID {
			result = append(result, ev)
		}
	}
	return result, true
}
