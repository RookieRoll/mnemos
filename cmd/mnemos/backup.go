package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/storage"
)

// runBackup writes a transactionally consistent snapshot of the live store:
//
//	mnemos backup <path>
//
// The snapshot comes from the storage layer's VACUUM INTO path, which copies a
// live write-ahead-log database without stopping writers and without copying
// sidecar files by hand. An existing target is never overwritten: that would
// destroy either an earlier snapshot or an unrelated file, so it is reported
// as a clear error and exit status 1.
func runBackup(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("backup")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErrorf("usage: mnemos backup <path>")
	}
	dest := fs.Arg(0)

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	size, err := d.db.Backup(ctx, dest)
	if err != nil {
		if errors.Is(err, storage.ErrBackupTargetExists) {
			return fmt.Errorf("backup: %s already exists; refusing to overwrite it: %w", dest, err)
		}
		return fmt.Errorf("backup: %w", err)
	}
	fmt.Printf("wrote a consistent snapshot to %s (%d bytes)\n", dest, size)
	return nil
}
