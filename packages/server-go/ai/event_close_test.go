package ai

import "testing"

func TestEventBus_Close_Idempotent(t *testing.T) {
	b := NewEventBus()
	b.Close()
	b.Close() // second close must not panic
}

func TestEventBus_Close_EmitAfterClose(t *testing.T) {
	b := NewEventBus()
	b.Close()
	// Emit after close must not panic or block
	b.Emit(Event{Type: EventCancel})
}

func TestEventBus_SubscribeAndClose(t *testing.T) {
	b := NewEventBus()
	ch := b.Subscribe(EventCancel, 1)
	_ = ch
	b.Close()
	// After close, Emit should be a no-op and not panic
	b.Emit(Event{Type: EventCancel})
	b.Emit(Event{Type: EventTeamDone})
}
