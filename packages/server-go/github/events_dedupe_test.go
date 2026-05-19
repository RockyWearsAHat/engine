package github

import (
	"fmt"
	"testing"
)

func TestMarkEventProcessed_BlankIDAlwaysTrue(t *testing.T) {
	w := &EventsWatcher{processed: map[string]bool{}}
	if !w.markEventProcessed("  ") {
		t.Fatal("blank IDs should be processed best-effort")
	}
}

func TestMarkEventProcessed_DeduplicatesAndEvictsOldest(t *testing.T) {
	w := &EventsWatcher{processed: map[string]bool{}}

	if !w.markEventProcessed("id-1") {
		t.Fatal("first id-1 should be accepted")
	}
	if w.markEventProcessed("id-1") {
		t.Fatal("second id-1 should be rejected as duplicate")
	}

	for i := 0; i < maxProcessedEventIDs+5; i++ {
		_ = w.markEventProcessed(fmt.Sprintf("evict-%d", i))
	}

	if len(w.processedOrder) > maxProcessedEventIDs {
		t.Fatalf("processedOrder len = %d, want <= %d", len(w.processedOrder), maxProcessedEventIDs)
	}
}
