package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"spm-platform/api/internal/models"
)

// Sentinel errors returned by RedeemStreamTicket.
var (
	ErrTicketNotFound = errors.New("auth: stream ticket not found or already consumed")
	ErrTicketExpired  = errors.New("auth: stream ticket has expired")
)

// ticketEntry holds the claims and expiry for a single stream ticket.
type ticketEntry struct {
	claims    models.AuthClaims
	expiresAt time.Time
}

// TicketStore is an in-memory store for short-lived, single-use stream tickets.
// It is safe for concurrent use. A background cleanup goroutine removes expired
// entries at the configured interval.
type TicketStore struct {
	store           sync.Map
	cleanupInterval time.Duration
}

// NewTicketStore creates a TicketStore and starts its background cleanup
// goroutine. The goroutine runs until ctx is cancelled.
// If interval is zero it defaults to 10 seconds.
func NewTicketStore(ctx context.Context, interval time.Duration) *TicketStore {
	if interval == 0 {
		interval = 10 * time.Second
	}
	ts := &TicketStore{cleanupInterval: interval}
	go ts.cleanup(ctx)
	return ts
}

// IssueStreamTicket generates a UUID v4 ticket, stores it alongside the
// provided claims with a 30-second expiry, and returns the ticket ID string.
// If UUID generation fails the method returns an error and nothing is stored.
func (ts *TicketStore) IssueStreamTicket(claims models.AuthClaims) (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	claims.Normalize()
	ts.store.Store(id.String(), ticketEntry{
		claims:    claims,
		expiresAt: time.Now().Add(30 * time.Second),
	})
	return id.String(), nil
}

// RedeemStreamTicket atomically looks up and deletes the ticket identified by
// ticketID. It returns:
//   - the associated *AuthClaims and nil when the ticket is found and valid,
//   - ErrTicketExpired when the ticket exists but its TTL has elapsed (the
//     ticket is deleted in this case too),
//   - ErrTicketNotFound when the ticket is absent or has already been consumed.
//
// sync.Map.LoadAndDelete guarantees there is no window between the lookup and
// the delete, preventing double-redemption under concurrent access.
func (ts *TicketStore) RedeemStreamTicket(ticketID string) (*models.AuthClaims, error) {
	raw, loaded := ts.store.LoadAndDelete(ticketID)
	if !loaded {
		return nil, ErrTicketNotFound
	}
	entry := raw.(ticketEntry)
	if time.Now().After(entry.expiresAt) {
		return nil, ErrTicketExpired
	}
	c := entry.claims
	return &c, nil
}

// cleanup is a background goroutine that periodically scans the store and
// removes expired entries. It completes the current scan iteration before
// honouring a context cancellation, and exits when ctx is done.
func (ts *TicketStore) cleanup(ctx context.Context) {
	ticker := time.NewTicker(ts.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			ts.store.Range(func(key, value any) bool {
				if value.(ticketEntry).expiresAt.Before(now) {
					ts.store.Delete(key)
				}
				return true
			})
		}
	}
}
