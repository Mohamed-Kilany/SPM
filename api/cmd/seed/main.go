package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/models"
)

const (
	apiBaseURL = "http://localhost:8080"
)

type loginResponse struct {
	Token string `json:"token"`
}

type projectResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ticketResponse struct {
	ID string `json:"id"`
}

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://spm:spm_local_dev_password@localhost:5432/spm?sslmode=disable"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	log.Println("Starting platform seeding...")

	// 1. Connect to PostgreSQL
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer conn.Close(ctx)

	// 2. Connect to NATS
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to obtain JetStream context: %v", err)
	}

	// 3. Log in as admin to retrieve JWT token
	token, err := login("admin", "Admin@1234")
	if err != nil {
		log.Fatalf("Admin login failed: %v", err)
	}
	log.Println("Logged in as admin successfully.")

	// Define member UUIDs from the SQL seeds
	adminID := "00000000-0000-0000-0000-000000000001"
	demoID := "00000000-0000-0000-0000-000000000002"
	developerID := "00000000-0000-0000-0000-000000000003"
	testerID := "00000000-0000-0000-0000-000000000004"

	// 4. Create Project 1: spm-core
	coreProjID, err := createProject(token, "spm-core", "Core Backend Platform API Services")
	if err != nil {
		log.Fatalf("Failed to create project spm-core: %v", err)
	}
	log.Printf("Created project spm-core: %s", coreProjID)

	// 5. Create Project 2: spm-frontend
	frontProjID, err := createProject(token, "spm-frontend", "Vue 3 Single Page Application Frontend")
	if err != nil {
		log.Fatalf("Failed to create project spm-frontend: %v", err)
	}
	log.Printf("Created project spm-frontend: %s", frontProjID)

	// 6. Map project members in database
	// spm-core: admin (admin), demo (member), developer (member), tester (member)
	addMember(ctx, conn, coreProjID, adminID, "admin")
	addMember(ctx, conn, coreProjID, demoID, "member")
	addMember(ctx, conn, coreProjID, developerID, "member")
	addMember(ctx, conn, coreProjID, testerID, "member")

	// spm-frontend: admin (admin), developer (member), tester (member)
	addMember(ctx, conn, frontProjID, adminID, "admin")
	addMember(ctx, conn, frontProjID, developerID, "member")
	addMember(ctx, conn, frontProjID, testerID, "member")
	log.Println("Added project members successfully.")

	// 7. Populate spm-core tickets & statuses
	seedCoreTickets(token, js, coreProjID, developerID, testerID)

	// 8. Populate spm-frontend tickets & statuses
	seedFrontendTickets(token, js, frontProjID, developerID, testerID)

	log.Println("Platform seeding completed successfully!")
}

func login(username, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	resp, err := http.Post(apiBaseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", err
	}
	return lr.Token, nil
}

func createProject(token, name, description string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"name":        name,
		"description": description,
	})
	req, err := http.NewRequest("POST", apiBaseURL+"/api/v1/projects", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create project: %s", string(respBody))
	}

	var pr projectResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	return pr.ID, nil
}

func addMember(ctx context.Context, conn *pgx.Conn, projectID, userID, role string) {
	_, err := conn.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		projectID, userID, role)
	if err != nil {
		log.Printf("Warning: failed to map member %s to project %s: %v", userID, projectID, err)
	}
}

func createTicket(token, projectID, title, description, priority, state string, assigneeID string) (string, error) {
	payload := map[string]interface{}{
		"title":       title,
		"description": description,
		"priority":    priority,
		"state":       state,
	}
	if assigneeID != "" {
		payload["assignee_id"] = assigneeID
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/v1/projects/%s/tickets", apiBaseURL, projectID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create ticket: %s", string(respBody))
	}

	var tr ticketResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	return tr.ID, nil
}

func changeTicketState(token, projectID, ticketID, state string) error {
	body, _ := json.Marshal(map[string]string{
		"state": state,
	})
	url := fmt.Sprintf("%s/api/v1/projects/%s/tickets/%s/state", apiBaseURL, projectID, ticketID)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update ticket state: %s", string(respBody))
	}
	return nil
}

func publishEvent(js nats.JetStreamContext, eventType string, aggregateID uuid.UUID, payload interface{}) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal event payload: %v", err)
		return
	}

	envelope := models.EventEnvelope{
		EventType:   eventType,
		AggregateID: aggregateID,
		Payload:     json.RawMessage(payloadBytes),
		CreatedAt:   time.Now().UTC(),
	}

	envBytes, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("Failed to marshal event envelope: %v", err)
		return
	}

	subject := fmt.Sprintf("spm.events.%s.%s", eventType[:strings.Index(eventType, ".")], aggregateID.String())
	_, err = js.Publish(subject, envBytes)
	if err != nil {
		log.Printf("Failed to publish event %s: %v", eventType, err)
	}
}

func seedCoreTickets(token string, js nats.JetStreamContext, projectID string, developerID, testerID string) {
	pID := uuid.MustParse(projectID)

	// 1. Done Ticket
	t1, err := createTicket(token, projectID, "Design core API database schema", "Create PostgreSQL tables and relations", "high", "Backlog", developerID)
	if err == nil {
		_ = changeTicketState(token, projectID, t1, "In Progress")
		_ = changeTicketState(token, projectID, t1, "In Review")
		_ = changeTicketState(token, projectID, t1, "Done")
	}

	// 2. In Review Ticket
	t2, err := createTicket(token, projectID, "Implement JWT authentication middleware", "Handle authentication token validation", "critical", "Backlog", developerID)
	if err == nil {
		_ = changeTicketState(token, projectID, t2, "In Progress")
		_ = changeTicketState(token, projectID, t2, "In Review")
		// Simulate successful pipeline on this feature branch
		publishEvent(js, "pipeline.status_changed", pID, map[string]interface{}{
			"branch":          fmt.Sprintf("feature/%s", t2),
			"commit_sha":      "92a831fd92bbd298319fbc923a10db1a89c9284d",
			"pipeline_status": "success",
			"updated_at":      time.Now().UTC(),
		})
	}

	// 3. In Progress Ticket
	t3, err := createTicket(token, projectID, "Add NATS JetStream event publishing", "Publish event envelope on state changes", "high", "Backlog", developerID)
	if err == nil {
		_ = changeTicketState(token, projectID, t3, "In Progress")
		// Simulate in_progress pipeline
		publishEvent(js, "pipeline.status_changed", pID, map[string]interface{}{
			"branch":          fmt.Sprintf("feature/%s", t3),
			"commit_sha":      "c3a812ba921e4231bca01e9d0c2834b928cb81a2",
			"pipeline_status": "in_progress",
			"updated_at":      time.Now().UTC(),
		})
	}

	// 4. Backlog Ticket
	_, _ = createTicket(token, projectID, "Write unit tests for ticket state transitions", "Provide test coverage for state constraints", "medium", "Backlog", testerID)

	// Simulate deployment statuses for spm-core app
	publishEvent(js, "deployment.status_changed", pID, map[string]interface{}{
		"app_name":      "spm-core",
		"sync_status":   "Synced",
		"health_status": "Healthy",
		"commit_sha":    "742b8219adcbef9e81b2da98c218d9f1092fae2b",
		"project_id":    projectID,
		"updated_at":    time.Now().UTC(),
	})
}

func seedFrontendTickets(token string, js nats.JetStreamContext, projectID string, developerID, testerID string) {
	pID := uuid.MustParse(projectID)

	// 1. Done Ticket
	t1, err := createTicket(token, projectID, "Set up Vue 3 SPA framework with Vite", "Initialize repository layout and basic styling", "high", "Backlog", developerID)
	if err == nil {
		_ = changeTicketState(token, projectID, t1, "In Progress")
		_ = changeTicketState(token, projectID, t1, "In Review")
		_ = changeTicketState(token, projectID, t1, "Done")
	}

	// 2. In Progress Ticket
	t2, err := createTicket(token, projectID, "Design project board UI component", "Create task columns and ticket cards with glassmorphism style", "high", "Backlog", developerID)
	if err == nil {
		_ = changeTicketState(token, projectID, t2, "In Progress")
		// Simulate in_progress pipeline
		publishEvent(js, "pipeline.status_changed", pID, map[string]interface{}{
			"branch":          fmt.Sprintf("feature/%s", t2),
			"commit_sha":      "e892c2ba831abf91a27e31f0a1c72834c9c819a3",
			"pipeline_status": "in_progress",
			"updated_at":      time.Now().UTC(),
		})
	}

	// 3. Backlog Ticket
	_, _ = createTicket(token, projectID, "Connect board component to SSE endpoint", "Listen for Server-Sent Events from core backend", "low", "Backlog", testerID)

	// Simulate deployment statuses for spm-frontend app
	publishEvent(js, "deployment.status_changed", pID, map[string]interface{}{
		"app_name":      "spm-frontend",
		"sync_status":   "OutOfSync",
		"health_status": "Progressing",
		"commit_sha":    "f8e12ab982cc2b8e31e9a22f8a18bda9e20a283e",
		"project_id":    projectID,
		"updated_at":    time.Now().UTC(),
	})
}

// helper strings standard Index wrapper
type stringIndex interface{}
