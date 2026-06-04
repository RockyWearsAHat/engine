package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// HealthResponse describes a peer's available compute. The dispatcher uses it
// to pick the best peer for a given workload.
type HealthResponse struct {
	Name      string   `json:"name"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	CPUs      int      `json:"cpus"`
	Roles     []string `json:"roles,omitempty"`
	OllamaURL string   `json:"ollamaURL,omitempty"`
	UpAt      string   `json:"upAt"`
}

// ExecRequest is a remote shell execution. Used to dispatch test runs to a
// less-loaded peer. The signed body protects against unauthenticated callers.
type ExecRequest struct {
	Cwd     string   `json:"cwd,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	// TimeoutMs caps the shell execution. 0 → server-side default (10 min).
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// ExecResponse is the captured output from a remote exec.
type ExecResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	DurationMs int  `json:"durationMs"`
	Error    string `json:"error,omitempty"`
}

// Server hosts the mesh listener. Each request is HMAC-verified against the
// secret of the peer whose Origin header is presented.
type Server struct {
	cfg     *Config
	startAt time.Time
}

// NewServer constructs a mesh listener bound to cfg.
func NewServer(cfg *Config) *Server {
	if cfg == nil {
		cfg = &Config{}
	}
	return &Server{cfg: cfg, startAt: time.Now().UTC()}
}

// Register installs mesh routes on mux. The caller starts the HTTP server.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/mesh/health", s.handleHealth)
	mux.HandleFunc("/mesh/exec", s.handleExec)
	mux.HandleFunc("/mesh/inference", s.handleInferenceProxy)
}

// ListenAndServe binds the mux to cfg.ListenAddr.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	s.Register(mux)

	addr := s.cfg.ListenAddr
	if strings.TrimSpace(addr) == "" {
		addr = ":24445"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[mesh] listening on %s", addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// handleHealth returns this peer's capabilities. Signed; no body required.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := s.authenticate(r, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	resp := HealthResponse{
		Name:      s.cfg.SelfName,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		CPUs:      runtime.NumCPU(),
		Roles:     []string{},
		OllamaURL: s.cfg.SelfOllamaURL,
		UpAt:      s.startAt.Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode health response", http.StatusInternalServerError)
		return
	}
}

// handleExec runs a shell command on this peer and returns the captured
// output. Treats the request as fully trusted once the HMAC verifies.
//
// Security model: peers share a manually-provisioned secret. If your network
// has untrusted machines, do not run mesh. The HMAC prevents network passers-by
// from invoking exec, but a compromised peer secret = full RCE on this machine.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := s.authenticate(r, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var req ExecRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, req.Command, req.Args...) //nolint:gosec — mesh peers are explicitly trusted
	if strings.TrimSpace(req.Cwd) != "" {
		cmd.Dir = req.Cwd
	}
	if len(req.Env) > 0 {
		cmd.Env = append(cmd.Environ(), req.Env...)
	}
	stdout, stderr := &limitedBuffer{Max: 256 * 1024}, &limitedBuffer{Max: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	resp := ExecResponse{}
	runErr := cmd.Run()
	resp.Stdout = stdout.String()
	resp.Stderr = stderr.String()
	resp.DurationMs = int(time.Since(start).Milliseconds())
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
		} else {
			resp.ExitCode = -1
			resp.Error = runErr.Error()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode exec response", http.StatusInternalServerError)
		return
	}
}

// authenticate verifies the request was signed by a configured peer's secret.
// The Origin header tells us which peer claims to have sent it; the signature
// must then verify against that peer's secret.
func (s *Server) authenticate(r *http.Request, body []byte) error {
	origin := strings.TrimSpace(r.Header.Get(originHeader))
	if origin == "" {
		return fmt.Errorf("mesh: missing origin")
	}
	peer := s.cfg.FindPeer(origin)
	if peer == nil {
		return fmt.Errorf("mesh: unknown peer %q", origin)
	}
	if err := verifyRequest(peer.Secret, body, r.Header.Get(timestampHeader), r.Header.Get(signatureHeader)); err != nil {
		return err
	}
	return nil
}

// limitedBuffer captures up to Max bytes; further writes are dropped. Prevents
// a runaway peer command from OOMing the server.
type limitedBuffer struct {
	Max int
	buf strings.Builder
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.Max - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
