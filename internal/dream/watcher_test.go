package dream_test

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/dream"
)

func TestWatchStopsOnContextCancel(t *testing.T) {
	ds, _, db := newDream(t)
	coordinator, err := dream.NewCoordinator(dream.CoordinatorConfig{
		Store: db.Maintenance(), Runner: ds, AutomaticEnabled: true,
		Interval: time.Second, MaxPassDuration: 5 * time.Second, LeaseDuration: 15 * time.Second, RenewInterval: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- coordinator.Watch(ctx, 10*time.Millisecond, true) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}

func TestWatchZeroIntervalReturnsImmediately(t *testing.T) {
	ds, _, db := newDream(t)
	coordinator, err := dream.NewCoordinator(dream.CoordinatorConfig{
		Store: db.Maintenance(), Runner: ds, AutomaticEnabled: true,
		Interval: time.Second, MaxPassDuration: 5 * time.Second, LeaseDuration: 15 * time.Second, RenewInterval: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = coordinator.Watch(context.Background(), 0, true)
	if err != nil {
		t.Errorf("zero interval should no-op, got err %v", err)
	}
}
