package dream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
)

type coordinatorTestStore struct {
	fence      maintenance.Fence
	complete   int
	claimCalls int
	renewErr   error
}

func (s *coordinatorTestStore) TryClaim(context.Context, maintenance.ClaimRequest) (maintenance.ClaimResult, error) {
	s.claimCalls++
	return maintenance.ClaimResult{Status: maintenance.ClaimAcquired, Fence: s.fence}, nil
}

func (s *coordinatorTestStore) Renew(context.Context, maintenance.Fence, time.Duration) (time.Time, error) {
	return time.Now().Add(time.Minute), s.renewErr
}

func (s *coordinatorTestStore) Complete(context.Context, maintenance.Fence) error {
	s.complete++
	return nil
}

type coordinatorTestMaintenance struct {
	prune   func(context.Context) error
	decay   int
	started chan struct{}
}

func (m *coordinatorTestMaintenance) PruneFenced(ctx context.Context, _ maintenance.Fence, _ time.Time) (int64, error) {
	if m.started != nil {
		close(m.started)
	}
	if m.prune != nil {
		if err := m.prune(ctx); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func (m *coordinatorTestMaintenance) DecayImportanceFenced(context.Context, maintenance.Fence, int, int) (int64, error) {
	m.decay++
	return 0, nil
}

func newCoordinatorTest(t *testing.T, store *coordinatorTestStore, runner *Service) *Coordinator {
	t.Helper()
	c, err := NewCoordinator(CoordinatorConfig{
		Store: store, Runner: runner, AutomaticEnabled: true,
		Interval: time.Hour, MaxPassDuration: time.Second,
		LeaseDuration: 3 * time.Second, RenewInterval: 100 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCoordinatorStageFailureReturnsPartialJournalAndKeepsCycleUnsuccessful(t *testing.T) {
	store := &coordinatorTestStore{fence: maintenance.Fence{OwnerToken: "owner", CycleID: "cycle"}}
	stageStore := &coordinatorTestMaintenance{prune: func(context.Context) error { return errors.New("injected prune failure") }}
	runner := NewService(Config{Memory: memory.NewService(memory.Config{}), Store: stageStore})
	coordinator := newCoordinatorTest(t, store, runner)

	journal, err := coordinator.Run(context.Background(), false)
	if err == nil || journal == nil || len(journal.Notes) == 0 {
		t.Fatalf("stage failure should return partial journal and error: journal=%+v err=%v", journal, err)
	}
	if stageStore.decay != 1 {
		t.Fatalf("ordinary stage error should allow remaining stages to run; decay calls=%d", stageStore.decay)
	}
	if store.complete != 0 {
		t.Fatalf("failed pass must not advance last_success, Complete calls=%d", store.complete)
	}
}

func TestCoordinatorCancellationAndLostFenceStopStagesAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure func(context.Context, context.CancelFunc) error
		want    error
	}{
		{
			name: "cancel",
			failure: func(ctx context.Context, cancel context.CancelFunc) error {
				cancel()
				return ctx.Err()
			},
			want: context.Canceled,
		},
		{
			name: "lost fence",
			failure: func(context.Context, context.CancelFunc) error {
				return maintenance.ErrFenceLost
			},
			want: maintenance.ErrFenceLost,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &coordinatorTestStore{fence: maintenance.Fence{OwnerToken: "owner", CycleID: "cycle"}}
			var cancel context.CancelFunc
			stageStore := &coordinatorTestMaintenance{prune: func(ctx context.Context) error { return tc.failure(ctx, cancel) }}
			runner := NewService(Config{Memory: memory.NewService(memory.Config{}), Store: stageStore})
			coordinator := newCoordinatorTest(t, store, runner)
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			defer stop()

			journal, err := coordinator.Run(ctx, true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run error=%v, want %v", err, tc.want)
			}
			if journal == nil {
				t.Fatal("fatal stage should return its partial journal")
			}
			if stageStore.decay != 0 || store.complete != 0 {
				t.Fatalf("fatal stage continued: decay calls=%d Complete calls=%d", stageStore.decay, store.complete)
			}
		})
	}
}

func TestMaybeRunDisabledDoesNotClaimButManualRunStillDoes(t *testing.T) {
	store := &coordinatorTestStore{fence: maintenance.Fence{OwnerToken: "owner", CycleID: "cycle"}}
	runner := NewService(Config{Memory: memory.NewService(memory.Config{}), Store: &coordinatorTestMaintenance{}})
	c := newCoordinatorTest(t, store, runner)
	c.automaticEnabled = false
	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.claimCalls != 0 {
		t.Fatalf("disabled automatic opportunity claimed lease %d times", store.claimCalls)
	}
	if _, err := c.Run(context.Background(), false); err != nil {
		t.Fatalf("explicit pass should override only automatic gate: %v", err)
	}
	if store.claimCalls != 1 || store.complete != 1 {
		t.Fatalf("manual pass calls: claim=%d complete=%d", store.claimCalls, store.complete)
	}
}

func TestRenewFailureCancelsLongStageAndPassTimeoutDoesNotComplete(t *testing.T) {
	for _, tc := range []struct {
		name       string
		maxPass    time.Duration
		renewEvery time.Duration
		renewErr   error
		want       error
	}{
		{name: "renewal loses fence", maxPass: 500 * time.Millisecond, renewEvery: 10 * time.Millisecond, renewErr: maintenance.ErrFenceLost, want: maintenance.ErrFenceLost},
		{name: "pass budget expires", maxPass: 40 * time.Millisecond, renewEvery: 200 * time.Millisecond, want: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &coordinatorTestStore{
				fence: maintenance.Fence{OwnerToken: "owner", CycleID: "cycle"}, renewErr: tc.renewErr,
			}
			started := make(chan struct{})
			stageStore := &coordinatorTestMaintenance{started: started}
			stageStore.prune = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
			runner := NewService(Config{Memory: memory.NewService(memory.Config{}), Store: stageStore})
			c, err := NewCoordinator(CoordinatorConfig{
				Store: store, Runner: runner, AutomaticEnabled: true,
				Interval: time.Hour, MaxPassDuration: tc.maxPass,
				LeaseDuration: time.Second, RenewInterval: tc.renewEvery,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := c.Run(context.Background(), true)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("long stage did not start")
			}
			select {
			case err := <-done:
				if !errors.Is(err, tc.want) {
					t.Fatalf("Run error=%v, want %v", err, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("coordinator did not stop the blocked stage")
			}
			if stageStore.decay != 0 || store.complete != 0 {
				t.Fatalf("cancelled pass continued: decay=%d Complete=%d", stageStore.decay, store.complete)
			}
		})
	}
}
