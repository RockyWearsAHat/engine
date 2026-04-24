//go:build !windows

package terminal

import "testing"

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func TestManager_Write_Nonexistent(t *testing.T) {
	m := NewManager()
	// Write to a terminal that doesn't exist — must not panic or error.
	m.Write("no-such-id", "hello")
}

func TestManager_Resize_Nonexistent(t *testing.T) {
	m := NewManager()
	// Resize on a terminal that doesn't exist — must not panic or error.
	m.Resize("no-such-id", 80, 24)
}

func TestManager_Kill_Nonexistent(t *testing.T) {
	m := NewManager()
	// Kill on a terminal that doesn't exist — must not panic or error.
	m.Kill("no-such-id")
}

func TestManager_KillAll_Empty(t *testing.T) {
	m := NewManager()
	// KillAll on an empty manager — must not panic.
	m.KillAll()
}
