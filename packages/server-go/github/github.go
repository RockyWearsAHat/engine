// Package github provides a lightweight GitHub API client for issue management.
// It uses GITHUB_TOKEN from the environment for authentication.
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.github.com"

func apiBase() string {
	if v := os.Getenv("GITHUB_API_BASE"); v != "" {
		return v
	}
	return defaultAPIBase
}

// Client wraps HTTP calls to the GitHub REST API.
type Client struct {
	token      string
	httpClient *http.Client
	owner      string
	repo       string
}

// Issue represents a GitHub issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Labels    []Label   `json:"labels"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Label represents a GitHub issue label.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Comment represents a GitHub issue comment.
type Comment struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	HTMLURL   string    `json:"html_url"`
}

// NewClient creates a GitHub client for the given owner/repo.
// Reads GITHUB_TOKEN from environment. Returns error if token is missing.
func NewClient(owner, repo string) (*Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN environment variable not set")
	}
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		owner:      owner,
		repo:       repo,
	}, nil
}

// NewClientWithToken creates a GitHub client with an explicit token.
func NewClientWithToken(owner, repo, token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		owner:      owner,
		repo:       repo,
	}
}

// ListIssues returns open issues for the repository.
func (c *Client) ListIssues(state string, labels []string) ([]Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues?state=%s&per_page=30",
		apiBase(), c.owner, c.repo, state)
	if len(labels) > 0 {
		url += "&labels=" + strings.Join(labels, ",")
	}

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}

	var issues []Issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}
	return issues, nil
}

// GetIssue returns a single issue by number.
func (c *Client) GetIssue(number int) (*Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", apiBase(), c.owner, c.repo, number)
	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("get issue #%d: %w", number, err)
	}

	var issue Issue
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("parse issue: %w", err)
	}
	return &issue, nil
}

// CreateIssue creates a new issue and returns it.
func (c *Client) CreateIssue(title, body string, labels []string) (*Issue, error) {
	payload := map[string]any{
		"title": title,
		"body":  body,
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}

	respBody, err := c.doPost(
		fmt.Sprintf("%s/repos/%s/%s/issues", apiBase(), c.owner, c.repo),
		payload,
	)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	var issue Issue
	if err := json.Unmarshal(respBody, &issue); err != nil {
		return nil, fmt.Errorf("parse created issue: %w", err)
	}
	return &issue, nil
}

// AddComment adds a comment to an issue.
func (c *Client) AddComment(number int, body string) (*Comment, error) {
	payload := map[string]string{"body": body}
	respBody, err := c.doPost(
		fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", apiBase(), c.owner, c.repo, number),
		payload,
	)
	if err != nil {
		return nil, fmt.Errorf("add comment to #%d: %w", number, err)
	}

	var comment Comment
	if err := json.Unmarshal(respBody, &comment); err != nil {
		return nil, fmt.Errorf("parse comment: %w", err)
	}
	return &comment, nil
}

// CloseIssue closes an issue with an optional closing comment.
func (c *Client) CloseIssue(number int, comment string) error {
	if comment != "" {
		if _, err := c.AddComment(number, comment); err != nil {
			return err
		}
	}

	payload := map[string]string{"state": "closed"}
	_, err := c.doPatch(
		fmt.Sprintf("%s/repos/%s/%s/issues/%d", apiBase(), c.owner, c.repo, number),
		payload,
	)
	if err != nil {
		return fmt.Errorf("close issue #%d: %w", number, err)
	}
	return nil
}

// UpdateIssue updates an issue's title, body, and/or state.
func (c *Client) UpdateIssue(number int, updates map[string]any) (*Issue, error) {
	respBody, err := c.doPatch(
		fmt.Sprintf("%s/repos/%s/%s/issues/%d", apiBase(), c.owner, c.repo, number),
		updates,
	)
	if err != nil {
		return nil, fmt.Errorf("update issue #%d: %w", number, err)
	}

	var issue Issue
	if err := json.Unmarshal(respBody, &issue); err != nil {
		return nil, fmt.Errorf("parse updated issue: %w", err)
	}
	return &issue, nil
}

// --- HTTP helpers ---

func (c *Client) doGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *Client) doPost(url string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", url, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *Client) doPatch(url string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("PATCH", url, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *Client) doRequest(req *http.Request) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if req.Method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API %s %s returned %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, string(body))
	}
	return body, nil
}
