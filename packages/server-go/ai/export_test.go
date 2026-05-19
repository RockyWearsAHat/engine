package ai

// NewHandleForTest creates a properly-initialized OrchestratorHandle for use in
// tests. The cancel channel and approveCh are allocated so Stop() and
// ApproveStep() do not panic.
func NewHandleForTest() *OrchestratorHandle {
	return &OrchestratorHandle{
		cancel:    make(chan struct{}),
		approveCh: make(chan bool, 1),
	}
}
