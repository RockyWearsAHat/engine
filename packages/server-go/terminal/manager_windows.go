//go:build windows

// Package terminal provides a stub terminal manager for Windows.
// creack/pty does not support Windows; ConPTY support is a future milestone.
// The stub satisfies the build and returns a clear error at runtime so the
// rest of the application functions normally on Windows.
package terminal

import (
	"fmt"
)

// Manager is a no-op terminal manager on Windows.
type Manager struct{}

// Terminal is a no-op terminal on Windows.
type Terminal struct {
	ID  string
	Cwd string
}

// NewManager returns a Manager stub on Windows.
func NewManager() *Manager { return &Manager{} }

// Create returns an error explaining PTY is not yet supported on Windows.
func (m *Manager) Create(cwd string) (*Terminal, error) {
	return nil, fmt.Errorf("terminal PTY is not yet supported on Windows; ConPTY support is planned")
}

// Write is a no-op on Windows.
func (m *Manager) Write(id, data string) error {
	return fmt.Errorf("terminal not supported on Windows")
}

// Resize is a no-op on Windows.
func (m *Manager) Resize(id string, cols, rows uint16) error { return nil }

// Close is a no-op on Windows.
func (m *Manager) Close(id string) {}

// CloseAll is a no-op on Windows.
func (m *Manager) CloseAll() {}
