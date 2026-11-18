package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/models"
)

func testTicketStore(t *testing.T) *TicketStore {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewTicketStore(ctx, time.Hour)
}

func TestStreamTicketIsSingleUse(t *testing.T) {
	store := testTicketStore(t)
	claims := models.AuthClaims{
		Role: "member",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-1",
		},
	}
	ticket, err := store.IssueStreamTicket(claims)
	require.NoError(t, err)
	require.NotEmpty(t, ticket)

	first, err := store.RedeemStreamTicket(ticket)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "user-1", first.CanonicalUserID())
	assert.Equal(t, "user-1", first.UserID, "IssueStreamTicket normalizes legacy claims")

	second, err := store.RedeemStreamTicket(ticket)
	assert.Nil(t, second)
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

func TestStreamTicketExpiry(t *testing.T) {
	store := testTicketStore(t)
	ticket, err := store.IssueStreamTicket(models.AuthClaims{Role: "viewer"})
	require.NoError(t, err)

	raw, ok := store.store.Load(ticket)
	require.True(t, ok)
	entry := raw.(ticketEntry)
	entry.expiresAt = time.Now().Add(-time.Second)
	store.store.Store(ticket, entry)

	claims, err := store.RedeemStreamTicket(ticket)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, ErrTicketExpired)

	// Expired tickets are consumed atomically and cannot be retried.
	_, err = store.RedeemStreamTicket(ticket)
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

func TestStreamTicketUnknownID(t *testing.T) {
	store := testTicketStore(t)
	claims, err := store.RedeemStreamTicket("0192f3a4-1111-7000-8000-000000000001")
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, ErrTicketNotFound)
}
