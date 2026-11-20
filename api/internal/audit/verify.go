package audit

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"spm-platform/api/internal/metrics"
)

// ChainRow is the verification-relevant projection of one audit row.
type ChainRow struct {
	ID       string
	Payload  string
	PrevHash string
	Hash     string
}

// Verification is the result returned by GET /admin/audit/verify.
type Verification struct {
	OK            bool      `json:"ok"`
	Checked       int64     `json:"checked"`
	FirstBadID    string    `json:"first_bad_id,omitempty"`
	FirstBadCause string    `json:"first_bad_cause,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

// ChainHash is the canonical link function:
//
//	sha256(prev_hash + "|" + payload)
//
// The database trigger applies the same function to the canonical JSONB
// payload, so nothing outside the trigger can change how a hash is derived.
func ChainHash(prevHash, payload string) string {
	sum := sha256.Sum256([]byte(prevHash + "|" + payload))
	return hex.EncodeToString(sum[:])
}

// ChainVerifier walks rows in (occurred_at, id) order. Anchors are hashes of
// partitions removed by the audited retention job; the first retained row may
// legitimately point at an anchor instead of a row that still exists.
type ChainVerifier struct {
	anchors  map[string]struct{}
	checked  int64
	previous string
	first    bool
	bad      *ChainRow
	cause    string
}

// NewChainVerifier creates a verifier with the supplied retention anchors.
func NewChainVerifier(anchors map[string]struct{}) *ChainVerifier {
	return &ChainVerifier{anchors: anchors, previous: "", first: true}
}

// Add verifies one row and advances the chain when the row is valid.
func (v *ChainVerifier) Add(row ChainRow) {
	v.checked++
	if v.bad != nil {
		return
	}
	if v.first {
		if row.PrevHash != "" {
			if _, ok := v.anchors[row.PrevHash]; !ok {
				v.fail(row, "missing_anchor")
				return
			}
		}
	} else if row.PrevHash != v.previous {
		v.fail(row, "broken_link")
		return
	}
	expected := ChainHash(row.PrevHash, row.Payload)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(row.Hash)) != 1 {
		v.fail(row, "hash_mismatch")
		return
	}
	v.previous = row.Hash
	v.first = false
}

func (v *ChainVerifier) fail(row ChainRow, cause string) {
	copied := row
	v.bad = &copied
	v.cause = cause
}

// Result returns the verification summary.
func (v *ChainVerifier) Result() Verification {
	result := Verification{
		OK:        v.bad == nil,
		Checked:   v.checked,
		CheckedAt: time.Now().UTC(),
	}
	if v.bad != nil {
		result.FirstBadID = v.bad.ID
		result.FirstBadCause = v.cause
	}
	return result
}

// Verify walks the retained audit log and reports the first broken link.
func (l *Logger) Verify(ctx context.Context) (Verification, error) {
	if !l.Enabled() {
		return Verification{OK: true, CheckedAt: time.Now().UTC()}, nil
	}
	anchors := make(map[string]struct{})
	anchorRows, err := l.pool.Query(ctx, `SELECT anchor_hash FROM audit_chain_anchors WHERE anchor_hash <> ''`)
	if err != nil {
		return Verification{}, err
	}
	for anchorRows.Next() {
		var hash string
		if err := anchorRows.Scan(&hash); err != nil {
			anchorRows.Close()
			return Verification{}, err
		}
		anchors[hash] = struct{}{}
	}
	anchorRows.Close()
	if err := anchorRows.Err(); err != nil {
		return Verification{}, err
	}

	rows, err := l.pool.Query(ctx, `
		SELECT id::text, payload::text, prev_hash, hash
		FROM audit_events
		ORDER BY occurred_at, id`)
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()

	verifier := NewChainVerifier(anchors)
	for rows.Next() {
		var row ChainRow
		if err := rows.Scan(&row.ID, &row.Payload, &row.PrevHash, &row.Hash); err != nil {
			return Verification{}, err
		}
		verifier.Add(row)
	}
	if err := rows.Err(); err != nil {
		return Verification{}, err
	}
	result := verifier.Result()
	metrics.Default().ObserveAuditVerification(result.OK)
	metrics.Default().ObserveAuditChecked(result.Checked)
	return result, nil
}
