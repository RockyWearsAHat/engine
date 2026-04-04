//go:build windows

package terminal

import (
	"io"
	"os"
	"os/exec"
	"sync"
)

// Terminal represents a single shell session on Windows.
type Terminal struct {
	ID      string
	Cwd     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	outputs []io.ReadCloser
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

// Create spawns a new interactive Windows shell and returns the Terminal.
func (m *Manager) Create(id, cwd string, onData func(string), onExit func()) (*Terminal, error) {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}

	cmd := exec.Command(shell)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t := &Terminal{
		ID:      id,
		Cwd:     cwd,
		cmd:     cmd,
		stdin:   stdin,
		outputs: []io.ReadCloser{stdout, stderr},
		onData:  onData,
		onExit:  onExit,
	}

	m.mu.Lock()
	m.terminals[id] = t
	m.mu.Unlock()

	go t.readLoop(stdout)
	go t.readLoop(stderr)
	go t.waitLoop()

	return t, nil
}

func (t *Terminal) readLoop(pipe io.ReadCloser) {
	defer pipe.Close()

	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 && t.onData != nil {
			t.onData(string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) waitLoop() {
	_ = t.cmd.Wait()
	t.once.Do(func() {
		if t.onExit != nil {
			t.onExit()
		}
	})
}

// Write sends input to the shell.
func (m *Manager) Write(id, data string) {
	m.mu.Lock()
	t, ok := m.terminals[id]
	m.mu.Unlock()
	if ok {
		_, _ = t.stdin.Write([]byte(data))
	}
}

// Resize is a no-op on Windows pipe-backed shells.
func (m *Manager) Resize(id string, cols, rows uint16) {
	_, _, _ = id, cols, rows
}

// Kill terminates the shell and removes it from the manager.
func (m *Manager) Kill(id string) {
	m.mu.Lock()
	t, ok := m.terminals[id]
	if ok {
		delete(m.terminals, id)
	}
	m.mu.Unlock()

	if ok {
		t.once.Do(func() {
			if t.cmd.Process != nil {
				_ = t.cmd.Process.Kill()
			}
			if t.stdin != nil {
				_ = t.stdin.Close()
			}
			for _, output := range t.outputs {
				_ = output.Close()
			}
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
