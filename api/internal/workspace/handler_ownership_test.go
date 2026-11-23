package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeTransferReason(t *testing.T) {
	maximum := strings.Repeat("a", 500)
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty is allowed", input: "", want: ""},
		{name: "trims surrounding whitespace", input: "  handover to the new lead  ", want: "handover to the new lead"},
		{name: "accepts the maximum", input: maximum, want: maximum},
		{name: "rejects longer than the maximum", input: maximum + "a", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTransferReason(tc.input)
			if tc.wantErr {
				if !errors.Is(err, errTransferReason) {
					t.Fatalf("err = %v, want errTransferReason", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthorizeTransferAction(t *testing.T) {
	transfer := OwnershipTransfer{
		FromUserID:  "owner",
		ToUserID:    "target",
		RequestedBy: "requester",
		Status:      "pending",
	}
	cases := []struct {
		name        string
		action      string
		actorID     string
		globalAdmin bool
		status      string
		wantErr     error
	}{
		{name: "target accepts", action: "accept", actorID: "target"},
		{name: "target declines", action: "decline", actorID: "target"},
		{name: "owner cannot accept", action: "accept", actorID: "owner", wantErr: errTransferAction},
		{name: "admin cannot accept for the target", action: "accept", actorID: "admin", globalAdmin: true, wantErr: errTransferAction},
		{name: "admin confirms", action: "confirm", actorID: "admin", globalAdmin: true},
		{name: "owner cannot confirm", action: "confirm", actorID: "owner", wantErr: errTransferAction},
		{name: "target cannot confirm", action: "confirm", actorID: "target", wantErr: errTransferAction},
		{name: "requester cancels", action: "cancel", actorID: "requester"},
		{name: "current owner cancels", action: "cancel", actorID: "owner"},
		{name: "admin cancels", action: "cancel", actorID: "admin", globalAdmin: true},
		{name: "target cannot cancel", action: "cancel", actorID: "target", wantErr: errTransferAction},
		{name: "unknown action is rejected", action: "approve", actorID: "target", wantErr: errTransferAction},
		{name: "resolved transfer is rejected", action: "accept", actorID: "target", status: "accepted", wantErr: errTransferNotPending},
		{name: "declined transfer is rejected", action: "cancel", actorID: "owner", status: "declined", wantErr: errTransferNotPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := transfer
			if tc.status != "" {
				candidate.Status = tc.status
			}
			err := authorizeTransferAction(tc.action, tc.actorID, candidate, tc.globalAdmin)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
