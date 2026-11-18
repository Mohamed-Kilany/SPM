// Package ai provides the client for the stateless Python RAG service.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is an HTTP client for the SPM AI service.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a client from AI_SERVICE_URL. An empty URL disables AI features.
func New() *Client {
	return &Client{
		baseURL: strings.TrimRight(os.Getenv("AI_SERVICE_URL"), "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Enabled reports whether an AI service URL is configured.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

// Document is an authorized artifact supplied by the Go API.
type Document struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Title      string                 `json:"title"`
	Text       string                 `json:"text"`
	ProjectID  string                 `json:"project_id"`
	ProjectKey string                 `json:"project_key"`
	Status     string                 `json:"status"`
	URL        string                 `json:"url"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// SearchHit is a ranked artifact.
type SearchHit struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Title      string                 `json:"title"`
	Text       string                 `json:"text"`
	ProjectID  string                 `json:"project_id"`
	ProjectKey string                 `json:"project_key"`
	Status     string                 `json:"status"`
	URL        string                 `json:"url"`
	Score      float64                `json:"score"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// SearchResponse is returned by /semantic-search.
type SearchResponse struct {
	Query     string      `json:"query"`
	Hits      []SearchHit `json:"hits"`
	Engine    string      `json:"engine"`
	ElapsedMS float64     `json:"elapsed_ms"`
}

// Citation points to an artifact used in an answer.
type Citation struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	URL   string `json:"url"`
}

// AskResponse is returned by /ask.
type AskResponse struct {
	Answer     string     `json:"answer"`
	Citations  []Citation `json:"citations"`
	Confidence float64    `json:"confidence"`
	Engine     string     `json:"engine"`
	ElapsedMS  float64    `json:"elapsed_ms"`
}

// Insight is a deterministic recommendation.
type Insight struct {
	Severity string   `json:"severity"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence"`
	Action   string   `json:"action"`
}

// InsightResponse is returned by /insights.
type InsightResponse struct {
	Insights []Insight `json:"insights"`
	Engine   string    `json:"engine"`
}

// Search ranks documents for a query.
func (c *Client) Search(ctx context.Context, query string, documents []Document, topK int) (SearchResponse, error) {
	var response SearchResponse
	if !c.Enabled() {
		return response, fmt.Errorf("AI service is not configured")
	}
	err := c.post(ctx, "/semantic-search", map[string]interface{}{
		"query": query, "documents": documents, "top_k": topK,
	}, &response)
	return response, err
}

// Ask answers a question from supplied context.
func (c *Client) Ask(ctx context.Context, question string, documents []Document, topK int) (AskResponse, error) {
	var response AskResponse
	if !c.Enabled() {
		return response, fmt.Errorf("AI service is not configured")
	}
	err := c.post(ctx, "/ask", map[string]interface{}{
		"question": question, "documents": documents, "top_k": topK,
	}, &response)
	return response, err
}

// Insights computes deterministic recommendations from supplied context.
func (c *Client) Insights(ctx context.Context, documents []Document, limit int) (InsightResponse, error) {
	var response InsightResponse
	if !c.Enabled() {
		return response, fmt.Errorf("AI service is not configured")
	}
	err := c.post(ctx, "/insights", map[string]interface{}{
		"documents": documents, "limit": limit,
	}, &response)
	return response, err
}

func (c *Client) post(ctx context.Context, path string, payload interface{}, out interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode AI request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build AI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("AI service request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("AI service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode AI response: %w", err)
	}
	return nil
}
