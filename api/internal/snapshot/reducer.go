package snapshot

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"spm-platform/api/internal/models"
)

// ErrUnknownEventType is returned by Apply when the EventEnvelope carries
// an event type that the reducer does not recognise.
var ErrUnknownEventType = errors.New("snapshot: unknown event type")

// ---------------------------------------------------------------------------
// Payload types
// ---------------------------------------------------------------------------

type projectCreatedPayload struct {
	ProjectID     uuid.UUID              `json:"project_id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	OwnerID       uuid.UUID              `json:"owner_id"`
	RepoName      string                 `json:"repo_name"`
	DefaultBranch string                 `json:"default_branch"`
	AppName       string                 `json:"app_name"`
	Members       []models.ProjectMember `json:"members"`
	Columns       []models.BoardColumn   `json:"columns"`
	CreatedAt     time.Time              `json:"created_at"`
}

type projectUpdatedPayload struct {
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	RepoName      string    `json:"repo_name"`
	DefaultBranch string    `json:"default_branch"`
	AppName       string    `json:"app_name"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ticketCreatedPayload struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Priority    string     `json:"priority"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ticketUpdatedPayload struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Priority    string     `json:"priority"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ticketStateChangedPayload struct {
	ID        uuid.UUID `json:"id"`
	NewState  string    `json:"new_state"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ticketDeletedPayload struct {
	ID uuid.UUID `json:"id"`
}

type pipelineStatusChangedPayload struct {
	Branch         string    `json:"branch"`
	CommitSHA      string    `json:"commit_sha"`
	Status         string    `json:"status"`
	PipelineStatus string    `json:"pipeline_status"`
	TicketID       uuid.UUID `json:"ticket_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type deploymentStatusChangedPayload struct {
	AppName      string    `json:"app_name"`
	SyncStatus   string    `json:"sync_status"`
	HealthStatus string    `json:"health_status"`
	CommitSHA    string    `json:"commit_sha"`
	ProjectID    string    `json:"project_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type gitBranchCreatedPayload struct {
	TicketID   uuid.UUID `json:"ticket_id"`
	BranchName string    `json:"branch_name"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type gitBranchFailedPayload struct {
	TicketID   uuid.UUID  `json:"ticket_id"`
	BranchName string     `json:"branch_name"`
	Reason     string     `json:"reason"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
}

type gitRepoProvisionedPayload struct {
	RepoName string `json:"repo_name"`
}

type gitRepoProvisionFailedPayload struct {
	Reason string `json:"reason"`
}

type gitCodePushedPayload struct {
	TicketID  uuid.UUID `json:"ticket_id"`
	Branch    string    `json:"branch"`
	CommitSHA string    `json:"commit_sha"`
	Author    string    `json:"author"`
}

type notificationPayload struct {
	TicketID   uuid.UUID  `json:"ticket_id"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	Reason     string     `json:"reason"`
	Message    string     `json:"message"`
}

type deploymentManifestFailedPayload struct {
	Reason   string `json:"reason"`
	ImageTag string `json:"image_tag"`
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

// Apply applies a single EventEnvelope to the given ProjectState and returns
// the updated state. The input state is not mutated. Unknown event types return
// ErrUnknownEventType and the original state.
func Apply(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	next := shallowCopy(state)
	if next.ProjectID == uuid.Nil {
		next.ProjectID = env.AggregateID
	}

	switch env.EventType {
	case "project.created":
		return applyProjectCreated(next, env)
	case "project.updated":
		return applyProjectUpdated(next, env)
	case "ticket.created":
		return applyTicketCreated(next, env)
	case "ticket.updated":
		return applyTicketUpdated(next, env)
	case "ticket.state_changed":
		return applyTicketStateChanged(next, env)
	case "ticket.deleted":
		return applyTicketDeleted(next, env)
	case "pipeline.status_changed":
		return applyPipelineStatusChanged(next, env)
	case "deployment.status_changed":
		return applyDeploymentStatusChanged(next, env)
	case "deployment.health_degraded":
		return applyDeploymentDegraded(next, env)
	case "deployment.manifest_commit_failed":
		return applyManifestCommitFailed(next, env)
	case "git.branch.created", "git.branch.already_exists":
		return applyGitBranchCreated(next, env)
	case "git.branch.creation_failed":
		return applyGitBranchFailed(next, env)
	case "notification.branch_creation_failed":
		return applyNotification(next, env)
	case "git.code.pushed":
		return applyGitCodePushed(next, env)
	case "git.repo.provisioned":
		return applyGitRepoProvisioned(next, env)
	case "git.repo.provision_failed":
		return applyGitRepoProvisionFailed(next, env)
	case "git.pr.merged":
		return finish(next, env, env.CreatedAt), nil
	case "workspace.changed":
		// Normalized workspace artifacts live in their own tables. The event
		// exists to bump the snapshot revision so SSE clients know to refetch
		// the workspace aggregate.
		return finish(next, env, env.CreatedAt), nil
	default:
		return state, ErrUnknownEventType
	}
}

// ---------------------------------------------------------------------------
// Project
// ---------------------------------------------------------------------------

func applyProjectCreated(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p projectCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	if p.ProjectID != uuid.Nil {
		state.ProjectID = p.ProjectID
	}
	if state.ProjectID == uuid.Nil {
		state.ProjectID = env.AggregateID
	}
	state.Name = p.Name
	state.Description = p.Description
	state.OwnerID = p.OwnerID
	state.RepoName = p.RepoName
	state.DefaultBranch = p.DefaultBranch
	state.AppName = p.AppName
	if len(p.Members) > 0 {
		state.Members = p.Members
	}
	if len(p.Columns) > 0 {
		state.Columns = p.Columns
	} else if len(state.Columns) == 0 {
		state.Columns = models.DefaultBoardColumns()
	}
	if state.Tickets == nil {
		state.Tickets = []models.Ticket{}
	}
	if state.Pipelines == nil {
		state.Pipelines = []models.PipelineStatus{}
	}
	if state.Deployments == nil {
		state.Deployments = []models.DeploymentStatus{}
	}
	if state.Notifications == nil {
		state.Notifications = []models.Notification{}
	}
	at := p.CreatedAt
	if at.IsZero() {
		at = env.CreatedAt
	}
	return finish(state, env, at), nil
}

func applyProjectUpdated(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p projectUpdatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	if p.Name != "" {
		state.Name = p.Name
	}
	if p.Description != "" {
		state.Description = p.Description
	}
	if p.RepoName != "" {
		state.RepoName = p.RepoName
	}
	if p.DefaultBranch != "" {
		state.DefaultBranch = p.DefaultBranch
	}
	if p.AppName != "" {
		state.AppName = p.AppName
	}
	return finish(state, env, p.UpdatedAt), nil
}

// ---------------------------------------------------------------------------
// Tickets
// ---------------------------------------------------------------------------

func applyTicketCreated(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p ticketCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	priority := p.Priority
	if priority == "" {
		priority = models.PriorityMedium
	}
	ticket := models.Ticket{
		ID:          p.ID,
		Title:       p.Title,
		Description: p.Description,
		State:       p.State,
		AssigneeID:  p.AssigneeID,
		Priority:    priority,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.CreatedAt,
	}
	if ticket.CreatedAt.IsZero() {
		ticket.CreatedAt = env.CreatedAt
		ticket.UpdatedAt = env.CreatedAt
	}
	// Idempotent replay: replace an existing ticket instead of duplicating it.
	replaced := false
	for i := range state.Tickets {
		if state.Tickets[i].ID == ticket.ID {
			state.Tickets[i] = ticket
			replaced = true
			break
		}
	}
	if !replaced {
		state.Tickets = append(state.Tickets, ticket)
	}
	return finish(state, env, p.CreatedAt), nil
}

func applyTicketUpdated(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p ticketUpdatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	for i := range state.Tickets {
		if state.Tickets[i].ID == p.ID {
			state.Tickets[i].Title = p.Title
			state.Tickets[i].Description = p.Description
			state.Tickets[i].AssigneeID = p.AssigneeID
			if p.Priority != "" {
				state.Tickets[i].Priority = p.Priority
			}
			state.Tickets[i].UpdatedAt = eventTime(p.UpdatedAt, env.CreatedAt)
			break
		}
	}
	return finish(state, env, p.UpdatedAt), nil
}

func applyTicketStateChanged(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p ticketStateChangedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	for i := range state.Tickets {
		if state.Tickets[i].ID == p.ID {
			state.Tickets[i].State = p.NewState
			state.Tickets[i].UpdatedAt = eventTime(p.UpdatedAt, env.CreatedAt)
			break
		}
	}
	return finish(state, env, p.UpdatedAt), nil
}

func applyTicketDeleted(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p ticketDeletedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	filtered := make([]models.Ticket, 0, len(state.Tickets))
	for _, t := range state.Tickets {
		if t.ID != p.ID {
			filtered = append(filtered, t)
		}
	}
	state.Tickets = filtered
	return finish(state, env, env.CreatedAt), nil
}

// ---------------------------------------------------------------------------
// Pipelines and deployments
// ---------------------------------------------------------------------------

func applyPipelineStatusChanged(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p pipelineStatusChangedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	status := p.Status
	if status == "" {
		status = p.PipelineStatus
	}
	at := eventTime(p.UpdatedAt, env.CreatedAt)

	upserted := false
	for i := range state.Pipelines {
		if state.Pipelines[i].Branch == p.Branch {
			state.Pipelines[i].CommitSHA = p.CommitSHA
			state.Pipelines[i].Status = status
			state.Pipelines[i].UpdatedAt = at
			upserted = true
			break
		}
	}
	if !upserted {
		state.Pipelines = append(state.Pipelines, models.PipelineStatus{
			Branch: p.Branch, CommitSHA: p.CommitSHA, Status: status, UpdatedAt: at,
		})
	}

	for i := range state.Tickets {
		if p.TicketID != uuid.Nil && state.Tickets[i].ID == p.TicketID {
			state.Tickets[i].PipelineStatus = status
		} else if state.Tickets[i].BranchName != "" && state.Tickets[i].BranchName == p.Branch {
			state.Tickets[i].PipelineStatus = status
		}
	}
	return finish(state, env, at), nil
}

func applyDeploymentStatusChanged(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p deploymentStatusChangedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	at := eventTime(p.UpdatedAt, env.CreatedAt)
	upsertDeployment(&state, p, at)
	return finish(state, env, at), nil
}

func applyDeploymentDegraded(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p deploymentStatusChangedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	at := eventTime(p.UpdatedAt, env.CreatedAt)
	upsertDeployment(&state, p, at)
	addNotification(&state, models.Notification{
		Type:      "deployment.health_degraded",
		Message:   "Deployment " + p.AppName + " is degraded",
		CreatedAt: at,
	})
	return finish(state, env, at), nil
}

func applyManifestCommitFailed(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p deploymentManifestFailedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	addNotification(&state, models.Notification{
		Type:      "deployment.manifest_commit_failed",
		Message:   "Failed to commit manifest for " + p.ImageTag + ": " + p.Reason,
		CreatedAt: env.CreatedAt,
	})
	return finish(state, env, env.CreatedAt), nil
}

func upsertDeployment(state *models.ProjectState, p deploymentStatusChangedPayload, at time.Time) {
	appName := p.AppName
	if appName == "" {
		appName = state.AppName
	}
	for i := range state.Deployments {
		if state.Deployments[i].AppName == appName {
			state.Deployments[i].AppName = appName
			state.Deployments[i].SyncStatus = p.SyncStatus
			state.Deployments[i].HealthStatus = p.HealthStatus
			state.Deployments[i].CommitSHA = p.CommitSHA
			state.Deployments[i].UpdatedAt = at
			return
		}
	}
	state.Deployments = append(state.Deployments, models.DeploymentStatus{
		AppName: appName, SyncStatus: p.SyncStatus, HealthStatus: p.HealthStatus,
		CommitSHA: p.CommitSHA, UpdatedAt: at,
	})
}

// ---------------------------------------------------------------------------
// Git
// ---------------------------------------------------------------------------

func applyGitBranchCreated(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p gitBranchCreatedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	for i := range state.Tickets {
		if state.Tickets[i].ID == p.TicketID {
			state.Tickets[i].BranchName = p.BranchName
			state.Tickets[i].UpdatedAt = eventTime(p.UpdatedAt, env.CreatedAt)
			break
		}
	}
	return finish(state, env, p.UpdatedAt), nil
}

func applyGitBranchFailed(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p gitBranchFailedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	message := "Failed to create branch " + p.BranchName + ": " + p.Reason
	addNotification(&state, models.Notification{
		Type:       "git.branch.creation_failed",
		Message:    message,
		TicketID:   uuidPtr(p.TicketID),
		AssigneeID: p.AssigneeID,
		CreatedAt:  env.CreatedAt,
	})
	return finish(state, env, env.CreatedAt), nil
}

func applyNotification(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p notificationPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	message := p.Message
	if message == "" {
		message = "Branch creation failed for ticket " + p.TicketID.String() + ": " + p.Reason
	}
	addNotification(&state, models.Notification{
		Type:       "notification.branch_creation_failed",
		Message:    message,
		TicketID:   uuidPtr(p.TicketID),
		AssigneeID: p.AssigneeID,
		CreatedAt:  env.CreatedAt,
	})
	return finish(state, env, env.CreatedAt), nil
}

func applyGitCodePushed(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p gitCodePushedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	for i := range state.Tickets {
		if state.Tickets[i].ID == p.TicketID {
			state.Tickets[i].BranchName = p.Branch
			state.Tickets[i].UpdatedAt = env.CreatedAt
			break
		}
	}
	return finish(state, env, env.CreatedAt), nil
}

func applyGitRepoProvisioned(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p gitRepoProvisionedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	if p.RepoName != "" {
		state.RepoName = p.RepoName
	}
	state.GitProvisioningFailed = false
	return finish(state, env, env.CreatedAt), nil
}

func applyGitRepoProvisionFailed(state models.ProjectState, env models.EventEnvelope) (models.ProjectState, error) {
	var p gitRepoProvisionFailedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return state, err
	}
	state.GitProvisioningFailed = true
	addNotification(&state, models.Notification{
		Type:      "git.repo.provision_failed",
		Message:   "Git repository provisioning failed: " + p.Reason,
		CreatedAt: env.CreatedAt,
	})
	return finish(state, env, env.CreatedAt), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func finish(state models.ProjectState, env models.EventEnvelope, at time.Time) models.ProjectState {
	if at.IsZero() {
		at = env.CreatedAt
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	state.UpdatedAt = at
	if env.SequenceID > state.Revision {
		state.Revision = env.SequenceID
	}
	return state
}

func eventTime(at, fallback time.Time) time.Time {
	if at.IsZero() {
		return fallback
	}
	return at
}

func addNotification(state *models.ProjectState, n models.Notification) {
	if n.ID == uuid.Nil {
		n.ID = models.NewID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	state.Notifications = append(state.Notifications, n)
	// Keep the snapshot bounded; notifications are advisory alerts.
	const maxNotifications = 100
	if len(state.Notifications) > maxNotifications {
		state.Notifications = state.Notifications[len(state.Notifications)-maxNotifications:]
	}
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func shallowCopy(s models.ProjectState) models.ProjectState {
	cp := s
	if s.Tickets != nil {
		cp.Tickets = append([]models.Ticket{}, s.Tickets...)
	}
	if s.Pipelines != nil {
		cp.Pipelines = append([]models.PipelineStatus{}, s.Pipelines...)
	}
	if s.Deployments != nil {
		cp.Deployments = append([]models.DeploymentStatus{}, s.Deployments...)
	}
	if s.Columns != nil {
		cp.Columns = append([]models.BoardColumn{}, s.Columns...)
	}
	if s.Members != nil {
		cp.Members = append([]models.ProjectMember{}, s.Members...)
	}
	if s.Notifications != nil {
		cp.Notifications = append([]models.Notification{}, s.Notifications...)
	}
	return cp
}
