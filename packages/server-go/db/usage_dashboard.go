package db

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const activeGapCap = 15 * time.Minute

type UsageTotals struct {
	Requests             int     `json:"requests"`
	InputTokens          int64   `json:"inputTokens"`
	OutputTokens         int64   `json:"outputTokens"`
	TotalTokens          int64   `json:"totalTokens"`
	CostUSD              float64 `json:"costUsd"`
	AIComputeMs          int64   `json:"aiComputeMs"`
	ActiveDevelopmentMs  int64   `json:"activeDevelopmentMs"`
	AveragePricePerToken float64 `json:"averagePricePerToken"`
}

type UsageModelBreakdown struct {
	Provider             string  `json:"provider"`
	Model                string  `json:"model"`
	Requests             int     `json:"requests"`
	InputTokens          int64   `json:"inputTokens"`
	OutputTokens         int64   `json:"outputTokens"`
	TotalTokens          int64   `json:"totalTokens"`
	CostUSD              float64 `json:"costUsd"`
	AIComputeMs          int64   `json:"aiComputeMs"`
	AveragePricePerToken float64 `json:"averagePricePerToken"`
}

type UsageProjectBreakdown struct {
	ProjectPath          string  `json:"projectPath"`
	Requests             int     `json:"requests"`
	InputTokens          int64   `json:"inputTokens"`
	OutputTokens         int64   `json:"outputTokens"`
	TotalTokens          int64   `json:"totalTokens"`
	CostUSD              float64 `json:"costUsd"`
	AIComputeMs          int64   `json:"aiComputeMs"`
	ActiveDevelopmentMs  int64   `json:"activeDevelopmentMs"`
	AveragePricePerToken float64 `json:"averagePricePerToken"`
}

type UsageDashboard struct {
	Scope       string                  `json:"scope"`
	ProjectPath string                  `json:"projectPath,omitempty"`
	ModelFilter string                  `json:"modelFilter,omitempty"`
	GeneratedAt string                  `json:"generatedAt"`
	Totals      UsageTotals             `json:"totals"`
	Models      []UsageModelBreakdown   `json:"models"`
	Projects    []UsageProjectBreakdown `json:"projects"`
}

type usageEventRow struct {
	SessionID    string
	ProjectPath  string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	AIComputeMs int64
	CreatedAt    string
}

func LogUsageEvent(id, sessionID, projectPath, provider, model string, inputTokens, outputTokens, totalTokens int, costUSD float64, aiComputeMs int64) error {
	_, err := globalDB.Exec(
		`INSERT INTO usage_events (
			id, session_id, project_path, provider, model,
			input_tokens, output_tokens, total_tokens, cost_usd, api_duration_ms, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id,
		sessionID,
		projectPath,
		provider,
		model,
		inputTokens,
		outputTokens,
		totalTokens,
		costUSD,
		aiComputeMs,
		now(),
	)
	return err
}

func GetUsageDashboard(scope, projectPath, modelFilter string) (*UsageDashboard, error) {
	normalizedScope := strings.ToLower(strings.TrimSpace(scope))
	if normalizedScope == "" {
		normalizedScope = "project"
	}
	if normalizedScope != "project" && normalizedScope != "user" {
		return nil, fmt.Errorf("unsupported usage scope %q", scope)
	}
	if normalizedScope == "project" && strings.TrimSpace(projectPath) == "" {
		return nil, fmt.Errorf("projectPath is required for project scope")
	}

	dashboard := &UsageDashboard{
		Scope:       normalizedScope,
		ProjectPath: strings.TrimSpace(projectPath),
		ModelFilter: strings.TrimSpace(modelFilter),
		GeneratedAt: now(),
		Models:      []UsageModelBreakdown{},
		Projects:    []UsageProjectBreakdown{},
	}

	events, err := loadUsageEvents(normalizedScope, strings.TrimSpace(projectPath), strings.TrimSpace(modelFilter))
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return dashboard, nil
	}

	sessionIDsByProject := make(map[string]map[string]struct{})
	modelMap := make(map[string]*UsageModelBreakdown)
	projectMap := make(map[string]*UsageProjectBreakdown)

	for _, ev := range events {
		dashboard.Totals.Requests++
		dashboard.Totals.InputTokens += ev.InputTokens
		dashboard.Totals.OutputTokens += ev.OutputTokens
		dashboard.Totals.TotalTokens += ev.TotalTokens
		dashboard.Totals.CostUSD += ev.CostUSD
		dashboard.Totals.AIComputeMs += ev.AIComputeMs

		modelKey := strings.ToLower(strings.TrimSpace(ev.Provider)) + "::" + strings.ToLower(strings.TrimSpace(ev.Model))
		modelAgg := modelMap[modelKey]
		if modelAgg == nil {
			modelAgg = &UsageModelBreakdown{Provider: ev.Provider, Model: ev.Model}
			modelMap[modelKey] = modelAgg
		}
		modelAgg.Requests++
		modelAgg.InputTokens += ev.InputTokens
		modelAgg.OutputTokens += ev.OutputTokens
		modelAgg.TotalTokens += ev.TotalTokens
		modelAgg.CostUSD += ev.CostUSD
		modelAgg.AIComputeMs += ev.AIComputeMs

		projectAgg := projectMap[ev.ProjectPath]
		if projectAgg == nil {
			projectAgg = &UsageProjectBreakdown{ProjectPath: ev.ProjectPath}
			projectMap[ev.ProjectPath] = projectAgg
		}
		projectAgg.Requests++
		projectAgg.InputTokens += ev.InputTokens
		projectAgg.OutputTokens += ev.OutputTokens
		projectAgg.TotalTokens += ev.TotalTokens
		projectAgg.CostUSD += ev.CostUSD
		projectAgg.AIComputeMs += ev.AIComputeMs

		sessions := sessionIDsByProject[ev.ProjectPath]
		if sessions == nil {
			sessions = make(map[string]struct{})
			sessionIDsByProject[ev.ProjectPath] = sessions
		}
		sessions[ev.SessionID] = struct{}{}
	}

	activeByProject, err := computeActiveDevelopmentMsByProject(sessionIDsByProject)
	if err != nil {
		return nil, err
	}

	for projectPathValue, activeMs := range activeByProject {
		if projectAgg := projectMap[projectPathValue]; projectAgg != nil {
			projectAgg.ActiveDevelopmentMs = activeMs
			dashboard.Totals.ActiveDevelopmentMs += activeMs
		}
	}

	for _, modelAgg := range modelMap {
		modelAgg.AveragePricePerToken = averagePricePerToken(modelAgg.CostUSD, modelAgg.TotalTokens)
		dashboard.Models = append(dashboard.Models, *modelAgg)
	}
	sort.Slice(dashboard.Models, func(i, j int) bool {
		if dashboard.Models[i].CostUSD == dashboard.Models[j].CostUSD {
			return dashboard.Models[i].Model < dashboard.Models[j].Model
		}
		return dashboard.Models[i].CostUSD > dashboard.Models[j].CostUSD
	})

	for _, projectAgg := range projectMap {
		projectAgg.AveragePricePerToken = averagePricePerToken(projectAgg.CostUSD, projectAgg.TotalTokens)
		dashboard.Projects = append(dashboard.Projects, *projectAgg)
	}
	sort.Slice(dashboard.Projects, func(i, j int) bool {
		if dashboard.Projects[i].CostUSD == dashboard.Projects[j].CostUSD {
			return dashboard.Projects[i].ProjectPath < dashboard.Projects[j].ProjectPath
		}
		return dashboard.Projects[i].CostUSD > dashboard.Projects[j].CostUSD
	})

	dashboard.Totals.AveragePricePerToken = averagePricePerToken(dashboard.Totals.CostUSD, dashboard.Totals.TotalTokens)

	return dashboard, nil
}

func loadUsageEvents(scope, projectPath, modelFilter string) ([]usageEventRow, error) {
	rows, err := globalDB.Query(`
		SELECT
			u.session_id,
			s.project_path,
			u.provider,
			u.model,
			u.input_tokens,
			u.output_tokens,
			u.total_tokens,
			u.cost_usd,
			u.api_duration_ms,
			u.created_at
		FROM usage_events u
		JOIN sessions s ON s.id = u.session_id
		WHERE (? = 'user' OR s.project_path = ?)
		  AND (? = '' OR LOWER(u.model) = LOWER(?))
		ORDER BY u.created_at ASC`,
		scope,
		projectPath,
		modelFilter,
		modelFilter,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []usageEventRow{}
	for rows.Next() {
		var row usageEventRow
		if err := rows.Scan(
			&row.SessionID,
			&row.ProjectPath,
			&row.Provider,
			&row.Model,
			&row.InputTokens,
			&row.OutputTokens,
			&row.TotalTokens,
			&row.CostUSD,
			&row.AIComputeMs,
			&row.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func computeActiveDevelopmentMsByProject(sessionIDsByProject map[string]map[string]struct{}) (map[string]int64, error) {
	out := make(map[string]int64, len(sessionIDsByProject))
	for projectPath, sessionSet := range sessionIDsByProject {
		activeMs, err := computeActiveDevelopmentMs(sessionSet)
		if err != nil {
			return nil, err
		}
		out[projectPath] = activeMs
	}
	return out, nil
}

func computeActiveDevelopmentMs(sessionSet map[string]struct{}) (int64, error) {
	if len(sessionSet) == 0 {
		return 0, nil
	}

	sessionIDs := make([]string, 0, len(sessionSet))
	for sessionID := range sessionSet {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)

	args := make([]any, 0, len(sessionIDs))
	placeholders := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		placeholders = append(placeholders, "?")
		args = append(args, sessionID)
	}

	query := `
		SELECT session_id, created_at
		FROM messages
		WHERE session_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY session_id ASC, created_at ASC`

	rows, err := globalDB.Query(query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	lastTimestampBySession := make(map[string]time.Time, len(sessionIDs))
	var activeMs int64
	for rows.Next() {
		var sessionID string
		var createdAtRaw string
		if err := rows.Scan(&sessionID, &createdAtRaw); err != nil {
			return 0, err
		}
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			continue
		}
		if last, ok := lastTimestampBySession[sessionID]; ok {
			delta := createdAt.Sub(last)
			if delta > 0 {
				if delta > activeGapCap {
					delta = activeGapCap
				}
				activeMs += delta.Milliseconds()
			}
		}
		lastTimestampBySession[sessionID] = createdAt
	}

	return activeMs, rows.Err()
}

func averagePricePerToken(costUSD float64, totalTokens int64) float64 {
	if totalTokens <= 0 {
		return 0
	}
	return costUSD / float64(totalTokens)
}
