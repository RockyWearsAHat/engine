package github

// GitHub Projects V2 integration via GraphQL.
//
// Required env vars:
//   ENGINE_GITHUB_PROJECT_NUMBER — the project board number (shown in the URL,
//     e.g. https://github.com/users/rocky/projects/3 → 3)
//   ENGINE_GITHUB_PROJECT_OWNER  — the owner of the project board (user or org
//     login). Falls back to EngineLogin() when unset.
//
// Engine must have "Write" access to the project board and the token must carry
// the `project` OAuth scope (or `read:project`+`write:project` for fine-grained).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// projectOwner returns the configured project board owner (user or org login).
// Falls back to EngineLogin() when ENGINE_GITHUB_PROJECT_OWNER is unset.
func projectOwner() string {
	if v := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_PROJECT_OWNER")); v != "" {
		return v
	}
	return EngineLogin()
}

// projectNumber returns the configured project board number, or 0 when unset.
// Reads ENGINE_GITHUB_PROJECT_NUMBER environment variable.
func projectNumber() int {
	raw := strings.TrimSpace(os.Getenv("ENGINE_GITHUB_PROJECT_NUMBER"))
	if raw == "" {
		return 0
	}
	n, _ := strconv.Atoi(raw)
	return n
}

// validateProjectConfig returns the configured project owner, token, and number.
// Returns zero values when project is not configured (best-effort mode).
func validateProjectConfig() (owner, token string, number int) {
	number = projectNumber()
	if number == 0 {
		return "", "", 0
	}
	owner = projectOwner()
	token = EngineToken()
	return owner, token, number
}

// graphqlDo executes a GraphQL query/mutation against the GitHub GraphQL API.
// Automatically sets authentication, content-type, and API version headers.
// The response is unmarshaled into the out parameter (if non-nil).
func graphqlDo(token string, query string, variables map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", apiBase()+"/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := eventsHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("graphql %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var wrapper struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return fmt.Errorf("parse graphql response: %w", err)
	}
	if len(wrapper.Errors) > 0 {
		msgs := make([]string, 0, len(wrapper.Errors))
		for _, e := range wrapper.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
	}
	if out != nil && wrapper.Data != nil {
		return json.Unmarshal(wrapper.Data, out)
	}
	return nil
}

// getProjectV2ID resolves the node ID for the configured project board.
// isOrg should be true when the project is under an organisation.
func getProjectV2ID(token, ownerLogin string, number int) (string, error) {
	query := `query($login: String!, $number: Int!) {
  user(login: $login) {
    projectV2(number: $number) { id }
  }
}`
	vars := map[string]any{"login": ownerLogin, "number": number}
	var data struct {
		User struct {
			ProjectV2 struct {
				ID string `json:"id"`
			} `json:"projectV2"`
		} `json:"user"`
	}
	if err := graphqlDo(token, query, vars, &data); err != nil {
		// Retry under organisation if user lookup fails
		orgQuery := `query($login: String!, $number: Int!) {
  organization(login: $login) {
    projectV2(number: $number) { id }
  }
}`
		var orgData struct {
			Organization struct {
				ProjectV2 struct {
					ID string `json:"id"`
				} `json:"projectV2"`
			} `json:"organization"`
		}
		if err2 := graphqlDo(token, orgQuery, vars, &orgData); err2 != nil {
			return "", fmt.Errorf("get project id (user: %v, org: %v)", err, err2)
		}
		return orgData.Organization.ProjectV2.ID, nil
	}
	if data.User.ProjectV2.ID == "" {
		return "", fmt.Errorf("project #%d not found for user %s", number, ownerLogin)
	}
	return data.User.ProjectV2.ID, nil
}

// getIssueNodeID returns the GraphQL node ID for owner/repo#issueNumber.
func getIssueNodeID(token, owner, repo string, issueNumber int) (string, error) {
	query := `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    issue(number: $number) { id }
  }
}`
	vars := map[string]any{"owner": owner, "repo": repo, "number": issueNumber}
	var data struct {
		Repository struct {
			Issue struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"repository"`
	}
	if err := graphqlDo(token, query, vars, &data); err != nil {
		return "", err
	}
	if data.Repository.Issue.ID == "" {
		return "", fmt.Errorf("issue #%d not found in %s/%s", issueNumber, owner, repo)
	}
	return data.Repository.Issue.ID, nil
}

// AddIssueToEngineProject adds the given issue to the Engine project board.
// Returns the project item ID (used later to update status). Best-effort:
// returns ("", nil) when the project is not configured.
func AddIssueToEngineProject(owner, repo string, issueNumber int) (itemID string, _ error) {
	ownerLogin, tok, num := validateProjectConfig()
	if num == 0 || tok == "" || ownerLogin == "" {
		return "", nil // project board not configured
	}

	projectID, err := getProjectV2ID(tok, ownerLogin, num)
	if err != nil {
		return "", fmt.Errorf("add to project: %w", err)
	}

	nodeID, err := getIssueNodeID(tok, owner, repo, issueNumber)
	if err != nil {
		return "", fmt.Errorf("add to project: %w", err)
	}

	mutation := `mutation($projectId: ID!, $contentId: ID!) {
  addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) {
    item { id }
  }
}`
	vars := map[string]any{"projectId": projectID, "contentId": nodeID}
	var data struct {
		AddProjectV2ItemById struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	if err := graphqlDo(tok, mutation, vars, &data); err != nil {
		return "", fmt.Errorf("add to project mutation: %w", err)
	}
	return data.AddProjectV2ItemById.Item.ID, nil
}

// findStatusFieldAndOption resolves the Status field ID and option ID for the given status value.
// Returns empty strings when the field or option cannot be found (best-effort).
func findStatusFieldAndOption(token, projectID, statusValue string) (fieldID, optionID string, _ error) {
	fieldQuery := `query($projectId: ID!) {
  node(id: $projectId) {
    ... on ProjectV2 {
      fields(first: 20) {
        nodes {
          ... on ProjectV2SingleSelectField {
            id name
            options { id name }
          }
        }
      }
    }
  }
}`
	var fieldData struct {
		Node struct {
			Fields struct {
				Nodes []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					Options []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"options"`
				} `json:"nodes"`
			} `json:"fields"`
		} `json:"node"`
	}
	if err := graphqlDo(token, fieldQuery, map[string]any{"projectId": projectID}, &fieldData); err != nil {
		return "", "", fmt.Errorf("list fields: %w", err)
	}

	for _, f := range fieldData.Node.Fields.Nodes {
		if strings.EqualFold(f.Name, "status") {
			fieldID = f.ID
			for _, opt := range f.Options {
				if strings.EqualFold(opt.Name, statusValue) {
					optionID = opt.ID
					break
				}
			}
			break
		}
	}
	return fieldID, optionID, nil
}

// UpdateProjectItemStatus sets the "Status" single-select field on a project
// board item to the given status name (e.g. "In Progress", "Done").
// Best-effort — no-ops when the status field or value cannot be resolved.
func UpdateProjectItemStatus(owner string, itemID, statusValue string) error {
	num := projectNumber()
	if num == 0 || itemID == "" {
		return nil
	}
	ownerLogin := projectOwner()
	tok := EngineToken()
	if tok == "" || ownerLogin == "" {
		return nil
	}

	projectID, err := getProjectV2ID(tok, ownerLogin, num)
	if err != nil {
		return fmt.Errorf("update status: get project: %w", err)
	}

	fieldID, optionID, err := findStatusFieldAndOption(tok, projectID, statusValue)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	if fieldID == "" || optionID == "" {
		return nil // field or option not found — skip silently
	}

	mutation := `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId
    itemId: $itemId
    fieldId: $fieldId
    value: { singleSelectOptionId: $optionId }
  }) {
    projectV2Item { id }
  }
}`
	vars := map[string]any{
		"projectId": projectID,
		"itemId":    itemID,
		"fieldId":   fieldID,
		"optionId":  optionID,
	}
	return graphqlDo(tok, mutation, vars, nil)
}
