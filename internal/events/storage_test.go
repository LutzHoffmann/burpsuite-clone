package events

import "testing"

func TestStoragePauseEventsOnlyOnTransitions(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	hub.PublishStoragePaused(true, 1)
	hub.PublishStoragePaused(true, 1)
	hub.PublishStoragePaused(false, 2)
	// A rejection committed before the resume, but its notification arrived late.
	hub.PublishStoragePaused(true, 1)
	hub.PublishStoragePaused(true, 3)
	hub.PublishStoragePaused(true, 3)
	for _, want := range []bool{true, false, true} {
		select {
		case event := <-events:
			if event.Type != "storage.status.changed" || event.Data.(map[string]interface{})["paused"] != want {
				t.Fatalf("event = %#v", event)
			}
		default:
			t.Fatal("missing transition")
		}
	}
	select {
	case event := <-events:
		t.Fatalf("duplicate event: %#v", event)
	default:
	}
}
