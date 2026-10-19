package continuity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestCloseAndCheckpointHaveOneTransactionOrder(t *testing.T) {
	h := newHarness(t)
	other, err := storage.Open(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for iteration := 0; iteration < 10; iteration++ {
		a := h.open("", fmt.Sprintf("race-%d", iteration))
		gate := make(chan struct{})
		var wg sync.WaitGroup
		var writeErr, closeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-gate
			_, writeErr = h.svc.Checkpoint(context.Background(), checkpoint(a.SessionID, "write", 0))
		}()
		go func() {
			defer wg.Done()
			<-gate
			closeErr = other.Sessions().Close(context.Background(), session.CloseInput{ID: a.SessionID, Summary: "final", Reflection: "keep private"})
		}()
		close(gate)
		wg.Wait()
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if writeErr != nil && !errors.Is(writeErr, continuity.ErrConflict) {
			t.Fatal(writeErr)
		}
		sess, err := h.db.Sessions().Get(context.Background(), a.SessionID)
		if err != nil || sess.EndedAt == nil || sess.Summary != "final" || sess.Reflection != "keep private" {
			t.Fatalf("close lost %+v %v", sess, err)
		}
		if writeErr == nil {
			if h.save(checkpoint(a.SessionID, "write", 0)).Status != "replayed" {
				t.Fatal("closed retry failed")
			}
		}
	}
}

func TestRecoverySnapshotNeverMixesClearWithOldBody(t *testing.T) {
	h := newHarness(t)
	a := h.open("", "source")
	receiver := h.open("", "receiver")
	h.save(checkpoint(a.SessionID, "save", 0))
	other, err := storage.Open(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	svc2 := continuity.New(continuity.Config{Store: other.Continuity(), Resolver: h.resolver, Now: func() time.Time { return h.now }})
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var clearErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-gate
		_, clearErr = svc2.Checkpoint(context.Background(), continuity.CheckpointInput{ProtocolVersion: 1, SessionID: a.SessionID, Action: "clear", RequestID: "clear", ExpectedRevision: revision(1)})
	}()
	close(gate)
	for i := 0; i < 15; i++ {
		out, err := h.svc.Context(context.Background(), continuity.ContextInput{ProtocolVersion: 1, SessionID: receiver.SessionID, SourceSessionID: a.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if out.Status == "cleared" {
			if out.Source.Revision != 2 || out.Source.Payload != nil {
				t.Fatal("mixed clear snapshot")
			}
		} else if out.Source.Revision != 1 || out.Source.Payload == nil {
			t.Fatal("mixed saved snapshot")
		}
	}
	wg.Wait()
	if clearErr != nil {
		t.Fatal(clearErr)
	}
}
