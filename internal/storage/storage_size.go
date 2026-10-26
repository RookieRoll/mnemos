package storage

import "os"

// databaseStorageBytes reports the on-disk size of the SQLite database at path:
// the main file plus, when present, its write-ahead log.
//
// The -shm shared-memory file is deliberately excluded. It is an mmap
// coordination file for the WAL, not data, so its size follows the log rather
// than the content: counting it would add nothing an operator can act on and
// would make consecutive readings flicker. Free pages inside the main file stay
// counted, because this number is compared against what the filesystem and
// mnemos backup report, and a page-count * page-size substitute would disagree
// with both.
//
// A stat failure yields zero rather than an error. Diagnostics must not become
// an outage, so a missing, non-regular, or unreadable file only means "size
// unknown"; the rest of Stats is still returned. An in-memory database has no
// file at all and therefore also reports zero.
func databaseStorageBytes(path string) int64 {
	return regularFileSize(path) + regularFileSize(path+"-wal")
}

// regularFileSize returns the size of the regular file at path, or zero when it
// is absent, not a regular file, or unreadable.
func regularFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}
