package vault_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/vault"
)

type heldEmbedder struct{ entered, release chan struct{} }

func (e heldEmbedder) Model() string  { return "held" }
func (e heldEmbedder) Dimension() int { return 1 }
func (e heldEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	close(e.entered)
	select {
	case <-e.release:
		return []float32{1}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestExportCountsSaveAlreadyWaitingOnEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	embed := heldEmbedder{make(chan struct{}), make(chan struct{})}
	mem := memory.NewService(memory.Config{Store: db.Observations(), Embedder: embed})
	done := make(chan error, 1)
	go func() {
		_, err := mem.Save(ctx, memory.SaveInput{Title: "in flight", Content: "New fact still embedding", Type: memory.TypeContext})
		done <- err
	}()
	select {
	case <-embed.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	root := t.TempDir()
	ex := vault.NewExporter(vault.Config{Root: root, StorePath: path, Obs: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills()})
	stats, err := ex.ExportAll(ctx)
	close(embed.release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if stats.Observations != 0 {
		t.Fatal("pending save unexpectedly exported")
	}
	status := vault.InspectStatus(ctx, vault.StatusOptions{Root: root, StorePath: path})
	if !status.PendingKnown || status.Pending != 1 {
		t.Fatalf("not exported, but pending=%d known=%v error=%s", status.Pending, status.PendingKnown, status.PendingError)
	}
	if _, err := ex.ExportAll(ctx); err != nil {
		t.Fatal(err)
	}
	if after := vault.InspectStatus(ctx, vault.StatusOptions{Root: root, StorePath: path}); !after.PendingKnown || after.Pending != 0 {
		t.Fatalf("completed export remains pending: %+v", after)
	}
}

func TestExportSnapshotsBelongToTheirVault(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := time.Now().UTC().Add(-2 * time.Hour)
	mem := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return clock }})
	save := func(title string) {
		if _, err := mem.Save(ctx, memory.SaveInput{Title: title, Content: title, Type: memory.TypeContext}); err != nil {
			t.Fatal(err)
		}
	}
	export := func(root string) {
		e := vault.NewExporter(vault.Config{Root: root, StorePath: path, Obs: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills()})
		if _, err := e.ExportAll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	left, right := t.TempDir(), t.TempDir()
	save("first fact")
	export(left)
	clock = clock.Add(time.Hour)
	save("late committed fact with earlier created_at")
	export(right)
	for _, tc := range []struct {
		root    string
		pending int
	}{{left, 1}, {right, 0}} {
		status := vault.InspectStatus(ctx, vault.StatusOptions{Root: tc.root, StorePath: path})
		if !status.PendingKnown || status.Pending != tc.pending {
			t.Fatalf("pending=%d want=%d error=%s", status.Pending, tc.pending, status.PendingError)
		}
	}
}

func TestExportSnapshotDoesNotQueueInactiveRows(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := time.Now().UTC().Add(-48 * time.Hour)
	mem := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return old }})
	until := old.Add(time.Hour)
	for _, in := range []memory.SaveInput{
		{Title: "live", Content: "live", Type: memory.TypeContext},
		{Title: "expired", Content: "expired", Type: memory.TypeContext, TTLDays: 1},
		{Title: "retired", Content: "retired", Type: memory.TypeContext, ValidUntil: &until},
	} {
		if _, err := mem.Save(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	e := vault.NewExporter(vault.Config{Root: root, StorePath: path, Obs: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills()})
	stats, err := e.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Observations != 1 {
		t.Fatalf("exported %d rows", stats.Observations)
	}
	if status := vault.InspectStatus(ctx, vault.StatusOptions{Root: root, StorePath: path}); !status.PendingKnown || status.Pending != 0 {
		t.Fatalf("inactive rows remain pending: %+v", status)
	}
}
