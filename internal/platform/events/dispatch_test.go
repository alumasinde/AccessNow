package events

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRelayWithoutSubscribersDoesNothing(t *testing.T) {
	// A nil pool is the proof: an unsubscribed registry must not reach the database.
	d := NewDispatcher(nil, NewRegistry())
	if err := d.Relay(context.Background(), 1); err != nil {
		t.Fatalf("Relay() = %v, want nil", err)
	}
}

func TestRegistryKeysAndHandlerOrder(t *testing.T) {
	reg := NewRegistry()
	var calls []string
	reg.On("movement.recorded", func(context.Context, Event) error { calls = append(calls, "second"); return nil })
	reg.On("pass.approved", func(context.Context, Event) error { calls = append(calls, "first"); return nil })
	reg.On("movement.recorded", func(context.Context, Event) error { calls = append(calls, "third"); return nil })

	if got := reg.keys(); !reflect.DeepEqual(got, []string{"movement.recorded", "pass.approved"}) {
		t.Fatalf("keys() = %v", got)
	}

	d := NewDispatcher(nil, reg)
	if err := d.run(context.Background(), Event{EventKey: "movement.recorded"}); err != nil {
		t.Fatalf("run() = %v", err)
	}
	if want := []string{"second", "third"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("handlers ran %v, want %v", calls, want)
	}
	if err := d.run(context.Background(), Event{EventKey: "pass.approved"}); err != nil {
		t.Fatalf("run() = %v", err)
	}
	if len(calls) != 3 || calls[2] != "first" {
		t.Fatalf("handlers ran %v, want pass.approved last", calls)
	}
}

func TestRunStopsAtTheFirstFailure(t *testing.T) {
	boom := errors.New("gate is closed")
	reg := NewRegistry()
	var reached int
	reg.On("movement.recorded", func(context.Context, Event) error { reached++; return boom })
	reg.On("movement.recorded", func(context.Context, Event) error { reached++; return nil })

	err := NewDispatcher(nil, reg).run(context.Background(), Event{EventKey: "movement.recorded"})
	if !errors.Is(err, boom) {
		t.Fatalf("run() error = %v, want %v", err, boom)
	}
	if reached != 1 {
		t.Fatalf("reached = %d, want 1: a failed handler must not let the next one claim the row", reached)
	}
}

func TestStoredEventDecodesPayload(t *testing.T) {
	row := stored{
		ID: 7, TenantID: 1, EventKey: "movement.recorded", AggregateType: "movement",
		PassID: 4, Payload: `{"direction":"out","result":"granted"}`,
	}
	e, err := row.event()
	if err != nil {
		t.Fatalf("event() = %v", err)
	}
	if e.PassID != 4 || e.EventKey != "movement.recorded" {
		t.Fatalf("event = %+v", e)
	}
	if e.Payload["direction"] != "out" || e.Payload["result"] != "granted" {
		t.Fatalf("payload = %v", e.Payload)
	}
}

func TestStoredEventRejectsBrokenPayload(t *testing.T) {
	// A row whose payload will not decode is a failed reaction, not a panic: the relay has
	// to keep draining the rows behind it.
	broken := stored{ID: 1, EventKey: "movement.recorded", Payload: `{"direction":`}
	if _, err := broken.event(); err == nil {
		t.Fatal("event() = nil error, want a decode failure")
	}
}
