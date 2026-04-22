package db

import "database/sql"

func GetProjectDirection(projectPath string) (string, error) {
	if projectPath == "" {
		return "", nil
	}

	row := globalDB.QueryRow(
		`SELECT summary FROM project_directions WHERE project_path = ? LIMIT 1`,
		projectPath,
	)
	var summary string
	if err := row.Scan(&summary); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return summary, nil
}

func UpsertProjectDirection(projectPath, summary string) error {
	if projectPath == "" {
		return nil
	}
	t := now()
	_, err := globalDB.Exec(
		`INSERT INTO project_directions (project_path, summary, created_at, updated_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT(project_path) DO UPDATE SET summary=excluded.summary, updated_at=excluded.updated_at`,
		projectPath,
		summary,
		t,
		t,
	)
	return err
}