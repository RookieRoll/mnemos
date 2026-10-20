package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type initPinger func(context.Context) error

func (p initPinger) PingContext(ctx context.Context) error { return p(ctx) }

type initSQLiteError int

func (e initSQLiteError) Error() string { return fmt.Sprintf("sqlite initialization code %d", e) }
func (e initSQLiteError) Code() int     { return int(e) }

func TestConnectionInitRetriesOnlyTypedBusyFamily(t *testing.T) {
	for _, code := range []int{5, 261, 517} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			err := pingSQLite(context.Background(), initPinger(func(context.Context) error {
				calls++
				if calls == 1 {
					return fmt.Errorf("open: %w", initSQLiteError(code))
				}
				return nil
			}), time.Second)
			if err != nil || calls != 2 {
				t.Fatalf("BUSY retry calls=%d err=%v", calls, err)
			}
		})
	}
	for _, failure := range []error{initSQLiteError(6), initSQLiteError(1), errors.New("busy text without SQLite code")} {
		calls := 0
		err := pingSQLite(context.Background(), initPinger(func(context.Context) error { calls++; return failure }), 20*time.Millisecond)
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatalf("retried non-BUSY failure: calls=%d err=%v", calls, err)
		}
	}
}

func TestConnectionInitCancellationAndRetryWindowPreserveCauses(t *testing.T) {
	t.Run("parent cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		err := pingSQLite(ctx, initPinger(func(context.Context) error { calls++; cancel(); return initSQLiteError(5) }), time.Second)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("parent cancellation lost: %d %v", calls, err)
		}
	})
	t.Run("already cancelled never opens", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := pingSQLite(ctx, initPinger(func(context.Context) error { calls++; return nil }), time.Second)
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("cancelled parent opened connection: %d %v", calls, err)
		}
	})
	t.Run("parent shorter deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		deadline, _ := ctx.Deadline()
		err := pingSQLite(ctx, initPinger(func(child context.Context) error {
			actual, _ := child.Deadline()
			if !actual.Equal(deadline) {
				t.Errorf("parent deadline replaced: %v %v", actual, deadline)
			}
			<-child.Done()
			return initSQLiteError(5)
		}), time.Second)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("parent timeout lost: %v", err)
		}
	})
	t.Run("window exhausted retains SQLite error", func(t *testing.T) {
		failure := fmt.Errorf("initialization: %w", initSQLiteError(5))
		calls := 0
		err := pingSQLite(context.Background(), initPinger(func(child context.Context) error {
			calls++
			<-child.Done()
			return failure
		}), 5*time.Millisecond)
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatalf("window exceeded or cause discarded: %d %v", calls, err)
		}
	})
	t.Run("expiry while retrying retains last busy", func(t *testing.T) {
		failure := initSQLiteError(5)
		calls := 0
		err := pingSQLite(context.Background(), initPinger(func(child context.Context) error {
			calls++
			if calls == 1 {
				return failure
			}
			<-child.Done()
			return child.Err()
		}), 25*time.Millisecond)
		if !errors.Is(err, failure) || calls > 2 {
			t.Fatalf("retry expiry lost last SQLite cause: %d %v", calls, err)
		}
	})
}
