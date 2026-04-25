package db

type AttentionResidual struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"sessionId"`
	BranchName  string  `json:"branchName"`
	MessageID   string  `json:"messageId"`
	SourceKey   string  `json:"sourceKey"`
	SourceType  string  `json:"sourceType"`
	SourceLabel string  `json:"sourceLabel"`
	QueryText   string  `json:"queryText"`
	Weight      float64 `json:"weight"`
	Score       float64 `json:"score"`
	Context     string  `json:"context"`
	CreatedAt   string  `json:"createdAt"`
}

func SaveAttentionResiduals(residuals []AttentionResidual) error {
	if len(residuals) == 0 {
		return nil
	}

	tx, err := globalDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(`
		INSERT INTO attention_residuals (
			id, session_id, message_id, source_key, source_type, source_label, query_text, weight, score, context, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck

	for _, residual := range residuals {
		createdAt := residual.CreatedAt
		if createdAt == "" {
			createdAt = now()
		}
		if _, err := stmt.Exec(
			residual.ID,
			residual.SessionID,
			residual.MessageID,
			residual.SourceKey,
			residual.SourceType,
			residual.SourceLabel,
			residual.QueryText,
			residual.Weight,
			residual.Score,
			residual.Context,
			createdAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func GetProjectAttentionResiduals(projectPath string, limit int) ([]AttentionResidual, error) {
	if limit <= 0 {
		limit = 160
	}

	rows, err := globalDB.Query(`
		SELECT
			ar.id,
			ar.session_id,
			s.branch_name,
			ar.message_id,
			ar.source_key,
			ar.source_type,
			ar.source_label,
			ar.query_text,
			ar.weight,
			ar.score,
			ar.context,
			ar.created_at
		FROM attention_residuals ar
		JOIN sessions s ON s.id = ar.session_id
		WHERE s.project_path = ?
		ORDER BY ar.created_at DESC
		LIMIT ?
	`, projectPath, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	residuals := make([]AttentionResidual, 0, limit)
	for rows.Next() {
		var residual AttentionResidual
		if err := rows.Scan(
			&residual.ID,
			&residual.SessionID,
			&residual.BranchName,
			&residual.MessageID,
			&residual.SourceKey,
			&residual.SourceType,
			&residual.SourceLabel,
			&residual.QueryText,
			&residual.Weight,
			&residual.Score,
			&residual.Context,
			&residual.CreatedAt,
		); err != nil {
			return nil, err
		}
		residuals = append(residuals, residual)
	}
	return residuals, nil
}
