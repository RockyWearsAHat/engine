package db

import "database/sql"

// UpsertProjectProfile persists profileJSON for projectPath.
// profileJSON is the JSON-serialised ai.ProjectProfile.
func UpsertProjectProfile(projectPath, profileJSON string) error {
	if projectPath == "" {
		return nil
	}
	t := now()
	_, err := globalDB.Exec(
		`INSERT INTO project_profiles (project_path, profile_json, created_at, updated_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT(project_path) DO UPDATE SET profile_json=excluded.profile_json, updated_at=excluded.updated_at`,
		projectPath,
		profileJSON,
		t,
		t,
	)
	return err
}

// GetProjectProfile returns the raw profile JSON for projectPath.
// Returns ("", nil) when no profile has been stored yet.
func GetProjectProfile(projectPath string) (string, error) {
	if projectPath == "" {
		return "", nil
	}
	row := globalDB.QueryRow(
		`SELECT profile_json FROM project_profiles WHERE project_path = ? LIMIT 1`,
		projectPath,
	)
	var profileJSON string
	if err := row.Scan(&profileJSON); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return profileJSON, nil
}
