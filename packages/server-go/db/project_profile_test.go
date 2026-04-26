package db

import "testing"

func TestUpsertAndGetProjectProfile_RoundTrip(t *testing.T) {
	initTestDB(t)

	const path = "/project/myapp"
	const json = `{"projectPath":"/project/myapp","type":"web-app"}`

	if err := UpsertProjectProfile(path, json); err != nil {
		t.Fatalf("UpsertProjectProfile: %v", err)
	}

	got, err := GetProjectProfile(path)
	if err != nil {
		t.Fatalf("GetProjectProfile: %v", err)
	}
	if got != json {
		t.Errorf("profile mismatch: got %q, want %q", got, json)
	}
}

func TestUpsertProjectProfile_UpdatesExisting(t *testing.T) {
	initTestDB(t)

	const path = "/project/myapp"
	if err := UpsertProjectProfile(path, `{"type":"unknown"}`); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	updated := `{"type":"rest-api"}`
	if err := UpsertProjectProfile(path, updated); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := GetProjectProfile(path)
	if err != nil {
		t.Fatalf("GetProjectProfile: %v", err)
	}
	if got != updated {
		t.Errorf("expected updated json %q, got %q", updated, got)
	}
}

func TestGetProjectProfile_NotFound(t *testing.T) {
	initTestDB(t)

	got, err := GetProjectProfile("/no/such/path")
	if err != nil {
		t.Fatalf("expected nil error for missing profile, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing profile, got %q", got)
	}
}

func TestUpsertProjectProfile_EmptyPath_Noop(t *testing.T) {
	initTestDB(t)

	if err := UpsertProjectProfile("", `{"type":"cli"}`); err != nil {
		t.Fatalf("empty path upsert should be noop, got %v", err)
	}
}

func TestGetProjectProfile_EmptyPath_Noop(t *testing.T) {
	initTestDB(t)

	got, err := GetProjectProfile("")
	if err != nil {
		t.Fatalf("empty path get should be noop, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for empty path, got %q", got)
	}
}

func TestGetProjectProfile_ClosedDB_Error(t *testing.T) {
	initTestDB(t)
	// Close the DB (but keep the pointer non-nil) to trigger a Scan error.
	_ = globalDB.Close()
	// Do NOT nil out globalDB — a nil pointer would panic before Scan is reached.

	_, err := GetProjectProfile("/project/x")
	if err == nil {
		t.Fatal("expected error when DB is closed, got nil")
	}
}
