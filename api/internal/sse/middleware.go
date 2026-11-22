package sse

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/auth"
)

// uuidV4RE matches a well-formed UUID v4 string.
var uuidV4RE = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// StreamAuthMiddleware authenticates SSE requests using the following priority:
//  1. ?token= query param present → reject 401 UNAUTHORIZED (JWT URL params not accepted)
//  2. ?ticket= query param present → validate UUID v4 format, redeem via TicketStore
//  3. No ?ticket= → fall through to Bearer JWT validation via auth.Middleware
//
// Persistent-session validation happens in Manager.Stream on connect and on
// every heartbeat, so both ticket and bearer paths are covered.
func StreamAuthMiddleware(
	store *auth.TicketStore,
	invalidationKV nats.KeyValue,
	validators ...auth.SessionValidator,
) gin.HandlerFunc {
	jwtMW := auth.Middleware(invalidationKV, validators...)

	return func(c *gin.Context) {
		// 1. Reject legacy ?token= URL parameter unconditionally.
		if c.Query("token") != "" {
			abortUnauthorized(c, "UNAUTHORIZED", "JWT URL parameters are not accepted")
			return
		}

		// 2. Ticket-based authentication path.
		ticket := c.Query("ticket")
		if ticket != "" {
			// Validate UUID v4 format before touching the store.
			if !uuidV4RE.MatchString(ticket) {
				abortUnauthorized(c, "TICKET_INVALID", "Malformed stream ticket")
				return
			}

			claims, err := store.RedeemStreamTicket(ticket)
			switch err {
			case nil:
				c.Set(auth.ClaimsKey, claims)
				c.Next()
			case auth.ErrTicketExpired:
				abortUnauthorized(c, "TICKET_EXPIRED", "Stream ticket has expired")
			default:
				abortUnauthorized(c, "TICKET_INVALID", "Stream ticket is invalid or already used")
			}
			return
		}

		// 3. Fall through to Bearer JWT validation.
		jwtMW(c)
	}
}

// abortUnauthorized writes a canonical 401 error response and aborts the chain.
func abortUnauthorized(c *gin.Context, code, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
