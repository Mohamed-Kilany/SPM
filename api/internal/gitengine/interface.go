// Package gitengine defines the contract for Git repository provisioning.
package gitengine

import (
	"context"
	"errors"
)

// ErrBranchExists is returned by BranchCreator.CreateBranch when the
// requested branch already exists in the remote repository.
var ErrBranchExists = errors.New("gitengine: branch already exists")

// ErrSourceBranchNotFound is returned by BranchCreator.CreateBranch when the
// specified source branch does not exist in the remote repository.
var ErrSourceBranchNotFound = errors.New("gitengine: source branch not found")

// Provisioner defines the Git repository provisioning contract.
// Implementations are responsible for creating a remote repository
// (e.g. on Gitea) for a newly created project.
type Provisioner interface {
	// ProvisionRepo creates a new repository for the given project.
	// projectID is the UUID of the project (used as a stable identifier),
	// and name is the human-readable project name used as the repo name.
	// Returns a non-nil error if provisioning fails.
	ProvisionRepo(ctx context.Context, projectID, name string) error
}

// WebhookRegistrar registers the SPM inbound webhook on a repository. It is
// optional so test doubles and alternative Git providers can omit it.
type WebhookRegistrar interface {
	EnsureRepoWebhook(ctx context.Context, repoName, webhookURL, secret string) error
}

// BranchCreator defines the contract for creating feature branches.
// Implementations are responsible for creating a branch on the remote
// Git repository (e.g. Gitea) when a ticket transitions to "In Progress".
type BranchCreator interface {
	// CreateBranch creates a new branch named branchName from sourceBranch
	// in the repository identified by repoName.
	// Returns ErrBranchExists if the branch already exists.
	// Returns a non-nil error for any other failure.
	CreateBranch(ctx context.Context, repoName, branchName, sourceBranch string) error
}
