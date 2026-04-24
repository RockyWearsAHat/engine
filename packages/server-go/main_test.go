package main

import "testing"

func TestDefaultProjectPath_NonEmpty(t *testing.T) {
	path := defaultProjectPath()
	if path == "" {
		t.Error("defaultProjectPath() should never return empty string")
	}
}
