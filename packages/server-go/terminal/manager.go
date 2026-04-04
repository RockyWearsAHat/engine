//go:build !windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Terminal represents a single PTY session.
type Terminal struct {
	ID      string
	Cwd     string
	ptmx    *os.File
	cmd     *exec.Cmd
	onData  func(string)
	onExit  func()
	once    sync.Once
}

// Manager holds all active terminals for a WebSocket connection.
type Manager struct {
	mu        sync.Mutex
	terminals map[string]*Terminal
}

// NewManager creates a new terminal manager.
func NewManager() *Manager {
	return &Manager{terminals: make(map[string]*Terminal)}
}

// Create spawns a new PTY shell and returns the Terminal.
func (m *Manager) Create(id, cwd string, onData func(string), onExit func()) (*Terminal, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/bash"
	}

	cmd := exec.Command(shell)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	t := &Terminal{
		ID:     id,
		Cwd:    cwd,
		ptmx:   ptmx,
		cmd:    cmd,
		onData: onData,
		onExit: onExit,
	}

	m.mu.Lock()
	m.terminals[id] = t
	m.mu.Unlock()

	// Stream PTY output to the onData callback in a goroutine.
	go t.readLoop()

	return t, nil
}

func (t *Terminal) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := t.ptmx.Read(buf)
		if n > 0 && t.onData != nil {
			t.onData(string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
	t.once.Do(func() {
		if t.onExit != nil {
			t.onExit()
		}
	})
}

// Write sends input to the PTY.
func (m *Manager) Write(id, data string) {
	m.mu.Lock()
	t, ok := m.terminals[id]
	m.mu.Unlock()
	if ok {
		t.ptmx.WriteString(data) //nolint:errcheck
	}
}

// Resize sets the PTY window size.
func (m *Manager) Resize(id string, cols, rows uint16) {
	m.mu.Lock()
	t, ok := m.terminals[id]
	m.mu.Unlock()
	if ok {
		pty.Setsize(t.ptmx, &pty.Winsize{Cols: cols, Rows: rows}) //nolint:errcheck
	}
}

// Kill terminates the PTY and removes it from the manager.
func (m *Manager) Kill(id string) {
	m.mu.Lock()
	t, ok := m.terminals[id]
	if ok {
		delete(m.terminals, id)
	}
	m.mu.Unlock()

	if ok {
		t.once.Do(func() {
			t.cmd.Process.Kill() //nolint:errcheck
			t.ptmx.Close()
			if t.onExit != nil {
				t.onExit()
			}
		})
	}
}

// KillAll terminates every terminal in the manager.
func (m *Manager) KillAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.terminals))
	for id := range m.terminals {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Kill(id)
	}
}
