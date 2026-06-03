package db

import (
	"encoding/json"
	"testing"
)

func TestMemoryLedgerAppendAndVerify(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	project := "/tmp/project-memory-ledger"
	if err := Init(project); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := CreateSession("sess-memory-ledger", project, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := AppendMemoryLedgerEvent(project, "sess-memory-ledger", "user_message", "test-model", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("AppendMemoryLedgerEvent first: %v", err)
	}
	second, err := AppendMemoryLedgerEvent(project, "sess-memory-ledger", "tool_call_result", "test-model", map[string]any{"tool": "run_tests", "isError": false})
	if err != nil {
		t.Fatalf("AppendMemoryLedgerEvent second: %v", err)
	}

	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("unexpected sequence progression: first=%d second=%d", first.Sequence, second.Sequence)
	}
	if second.PrevHash != first.EventHash {
		t.Fatalf("hash chain mismatch: prev=%s first=%s", second.PrevHash, first.EventHash)
	}

	if err := VerifyMemoryLedgerChain(project); err != nil {
		t.Fatalf("VerifyMemoryLedgerChain: %v", err)
	}
}

func TestMemoryResidualNodesAndSnapshotsRoundTrip(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	project := "/tmp/project-memory-residual"
	if err := Init(project); err != nil {
		t.Fatalf("Init: %v", err)
	}

	node := MemoryResidualNode{
		NodeKey:              "task:fix-context",
		ProjectPath:          project,
		SessionID:            "sess-a",
		NodeType:             "task",
		Label:                "Fix deterministic context compilation",
		VerificationStatus:   "unverified",
		Confidence:           0.8,
		Novelty:              0.5,
		Surprise:             0.3,
		VerificationStrength: 0.2,
		DependencyCentrality: 0.9,
		ResidualScore:        0.74,
		LastEventSequence:    11,
	}
	if err := UpsertMemoryResidualNode(node); err != nil {
		t.Fatalf("UpsertMemoryResidualNode: %v", err)
	}

	nodes, err := ListTopMemoryResidualNodes(project, "sess-a", 5)
	if err != nil {
		t.Fatalf("ListTopMemoryResidualNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one residual node, got %d", len(nodes))
	}
	if nodes[0].NodeKey != node.NodeKey {
		t.Fatalf("unexpected node key: %s", nodes[0].NodeKey)
	}

	payload := map[string]any{"goal": []string{"fix context"}, "verifiedFacts": []string{"tool ran"}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := SaveMemoryStateSnapshot(MemoryStateSnapshot{
		ProjectPath:   project,
		SessionID:     "sess-a",
		SnapshotType:  "scribe-shared",
		EventSequence: 11,
		SnapshotJSON:  string(body),
	}); err != nil {
		t.Fatalf("SaveMemoryStateSnapshot: %v", err)
	}

	snapshot, err := LoadLatestMemoryStateSnapshot(project, "sess-a", "scribe-shared")
	if err != nil {
		t.Fatalf("LoadLatestMemoryStateSnapshot: %v", err)
	}
	if snapshot == nil || snapshot.EventSequence != 11 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}
