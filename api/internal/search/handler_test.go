package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTermAssigneeMeUsesOneArgument(t *testing.T) {
	args := []interface{}{[]string{"project-1"}}
	condition, err := buildTerm(Term{Field: "assignee", Value: "me", Operator: "="}, &args, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "wi.assignee_id = $2::uuid", condition)
	assert.Equal(t, []interface{}{[]string{"project-1"}, "user-1"}, args)
}

func TestBuildTermUsesComputedWorkItemKey(t *testing.T) {
	args := []interface{}{[]string{"project-1"}}
	condition, err := buildTerm(Term{FreeText: true, Value: "SPM-1"}, &args, "user-1")
	require.NoError(t, err)
	assert.Contains(t, condition, "(p.key_prefix || '-' || wi.item_number) ILIKE $2")
	assert.NotContains(t, condition, "wi.key")
}
