//go:build !windows

package terminal

import (
	"strings"
	"testing"
	"time"
)

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

func TestManager_Create_SpawnsPTY(t *testing.T) {
	m := NewManager()

	var received []string
	exitCalled := make(chan struct{})

	term, err := m.Create("t1", t.TempDir(), func(data string) {
		received = append(received, data)
	}, func() {
		close(exitCalled)
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if term == nil {
		t.Fatal("expected non-nil Terminal")
	}

	// Send a command that produces deterministic output then exits.
	m.Write("t1", "printf 'engine-marker'; exit\n")

	// Wait for exit or timeout.
	select {
	case <-exitCalled:
	case <-time.After(5 * time.Second):
		t.Error("terminal did not exit within 5s")
		m.Kill("t1")
		return
	}

	combined := strings.Join(received, "")
	if !strings.Contains(combined, "engine-marker") {
		t.Errorf("expected 'engine-marker' in PTY output, got %q", combined)
	}
}

func TestManager_Create_Write_Resize(t *testing.T) {
	m := NewManager()

	exitCalled := make(chan struct{})
	_, err := m.Create("t2", t.TempDir(), func(string) {}, func() {
		close(exitCalled)
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Resize before exit — must not panic.
	m.Resize("t2", 120, 40)
	m.Write("t2", "exit\n")

	select {
	case <-exitCalled:
	case <-time.After(5 * time.Second):
		t.Error("terminal did not exit within 5s")
	}
	m.Kill("t2") // idempotent after exit
}

func TestManager_KillAll(t *testing.T) {
	m := NewManager()

	exits := make(chan struct{}, 2)
	onExit := func() { exits <- struct{}{} }

	for _, id := range []string{"ta", "tb"} {
		_, err := m.Create(id, t.TempDir(), func(string) {}, onExit)
		if err != nil {
			t.Fatalf("Create %s failed: %v", id, err)
		}
	}

	m.KillAll()

	// Wait for both onExit callbacks.
	for i := 0; i < 2; i++ {
		select {
		case <-exits:
		case <-time.After(5 * time.Second):
			t.Errorf("terminal %d did not exit after KillAll", i)
		}
	}
}

