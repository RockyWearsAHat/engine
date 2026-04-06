package db

type ProjectMessage struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	BranchName string `json:"branchName"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	CreatedAt  string `json:"createdAt"`
}

type ProjectSessionSummary struct {
	SessionID  string `json:"sessionId"`
	BranchName string `json:"branchName"`
	Summary    string `json:"summary"`
	UpdatedAt  string `json:"updatedAt"`
}

type ProjectLearning struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionId"`
	BranchName string  `json:"branchName"`
	Pattern    string  `json:"pattern"`
	Outcome    string  `json:"outcome"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
	Context    string  `json:"context"`
	CreatedAt  string  `json:"createdAt"`
}

type ProjectValidation struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	BranchName    string `json:"branchName"`
	Issue         string `json:"issue"`
	IssueResolved bool   `json:"issueResolved"`
	TestPassed    bool   `json:"testPassed"`
	ErrorCount    int    `json:"errorCount"`
	WarningCount  int    `json:"warningCount"`
	DurationMs    int64  `json:"durationMs"`
	Evidence      string `json:"evidence"`
	Command       string `json:"command"`
	CreatedAt     string `json:"createdAt"`
}

func GetProjectMessages(projectPath string, limit int) ([]ProjectMessage, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := globalDB.Query(`
		SELECT m.id, m.session_id, s.branch_name, m.role, m.content, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE s.project_path = ?
		ORDER BY m.created_at DESC
		LIMIT ?`,
		projectPath,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ProjectMessage
	for rows.Next() {
		var message ProjectMessage
		if err := rows.Scan(
			&message.ID,
			&message.SessionID,
			&message.BranchName,
			&message.Role,
			&message.Content,
			&message.CreatedAt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if messages == nil {
		messages = []ProjectMessage{}
	}
	return messages, nil
}

func GetProjectSessionSummaries(projectPath string, limit int) ([]ProjectSessionSummary, error) {
	if limit <= 0 {
		limit = 32
	}

	rows, err := globalDB.Query(`
		SELECT id, branch_name, summary, updated_at
		FROM sessions
		WHERE project_path = ? AND summary != ''
		ORDER BY updated_at DESC
		LIMIT ?`,
		projectPath,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []ProjectSessionSummary
	for rows.Next() {
		var summary ProjectSessionSummary
		if err := rows.Scan(
			&summary.SessionID,
			&summary.BranchName,
			&summary.Summary,
			&summary.UpdatedAt,
		); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if summaries == nil {
		summaries = []ProjectSessionSummary{}
	}
	return summaries, nil
}

func GetProjectLearnings(projectPath string, limit int) ([]ProjectLearning, error) {
	if limit <= 0 {
		limit = 48
	}

	rows, err := globalDB.Query(`
		SELECT l.id, l.session_id, s.branch_name, l.pattern, l.outcome, l.confidence, l.category, l.context, l.created_at
		FROM learning_events l
		JOIN sessions s ON s.id = l.session_id
		WHERE s.project_path = ?
		ORDER BY l.confidence DESC, l.created_at DESC
		LIMIT ?`,
		projectPath,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var learnings []ProjectLearning
	for rows.Next() {
		var learning ProjectLearning
		if err := rows.Scan(
			&learning.ID,
			&learning.SessionID,
			&learning.BranchName,
			&learning.Pattern,
			&learning.Outcome,
			&learning.Confidence,
			&learning.Category,
			&learning.Context,
			&learning.CreatedAt,
		); err != nil {
			return nil, err
		}
		learnings = append(learnings, learning)
	}
	if learnings == nil {
		learnings = []ProjectLearning{}
	}
	return learnings, nil
}

func GetProjectValidations(projectPath string, limit int) ([]ProjectValidation, error) {
	if limit <= 0 {
		limit = 40
	}

	rows, err := globalDB.Query(`
		SELECT v.id, v.session_id, s.branch_name, v.issue, v.issue_resolved, v.test_passed, v.error_count, v.warning_count, v.duration_ms, v.evidence, v.command, v.created_at
		FROM validation_results v
		JOIN sessions s ON s.id = v.session_id
		WHERE s.project_path = ?
		ORDER BY v.created_at DESC
		LIMIT ?`,
		projectPath,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var validations []ProjectValidation
	for rows.Next() {
		var validation ProjectValidation
		var issueResolved int
		var testPassed int
		if err := rows.Scan(
			&validation.ID,
			&validation.SessionID,
			&validation.BranchName,
			&validation.Issue,
			&issueResolved,
			&testPassed,
			&validation.ErrorCount,
			&validation.WarningCount,
			&validation.DurationMs,
			&validation.Evidence,
			&validation.Command,
			&validation.CreatedAt,
		); err != nil {
			return nil, err
		}
		validation.IssueResolved = issueResolved == 1
		validation.TestPassed = testPassed == 1
		validations = append(validations, validation)
	}
	if validations == nil {
		validations = []ProjectValidation{}
	}
	return validations, nil
}
