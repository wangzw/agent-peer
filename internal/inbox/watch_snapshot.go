package inbox

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

// testStampBeforeRead, when set by a test, runs after stampRecord has decided
// that a file looks readable and before it reads the content. It makes the
// window between the classifying Lstat and the read deterministic, so a test
// can replace the file there. Production leaves it nil.
var testStampBeforeRead atomic.Pointer[func()]

// fileStamp is the watcher's deduplication key for one registry file. The
// content digest is included because mtime and size alone miss an in-place
// rewrite of the same size that lands in the same file system timestamp tick,
// which is exactly the status change this package must report.
type fileStamp struct {
	mtime  time.Time
	size   int64
	mode   fs.FileMode
	digest [sha256.Size]byte
}

// scanSessions snapshots every canonical <pid>.json entry in dir. A missing or
// unreadable directory yields an empty snapshot; an entry that disappears
// during the scan is simply absent from this snapshot and will be reported by
// the next one.
func scanSessions(dir string) map[int]fileStamp {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	stamps := make(map[int]fileStamp, len(entries))
	for _, entry := range entries {
		pid, ok := parseRecordName(entry.Name())
		if !ok {
			continue
		}
		if stamp, ok := stampRecord(filepath.Join(dir, entry.Name())); ok {
			stamps[pid] = stamp
		}
	}
	return stamps
}

func stampRecord(path string) (fileStamp, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}, false
	}
	stamp := fileStamp{
		mtime: info.ModTime(),
		size:  info.Size(),
		mode:  info.Mode(),
	}
	// Only regular, bounded files are hashed. A symbolic link or an oversized
	// file still gets a stamp so that a change in either is reported; reads of
	// those files are already refused elsewhere.
	if !info.Mode().IsRegular() || info.Size() > maxRecordSize {
		return stamp, true
	}
	if hook := testStampBeforeRead.Load(); hook != nil {
		(*hook)()
	}
	// readRecordData uses O_NOFOLLOW and O_NONBLOCK and rechecks the file after
	// opening, so a path that a symbolic link or FIFO replaces after the Lstat
	// above cannot redirect the read or block this goroutine forever.
	data, err := readRecordData(path)
	if err != nil {
		// Keep the stat-based stamp. A transient read failure must not look
		// like the record was deleted; the next scan settles it.
		return stamp, true
	}
	stamp.digest = sha256.Sum256(data)
	return stamp, true
}

// changedPIDs returns the process ids whose stamp appeared, disappeared or
// changed between two snapshots, in ascending order.
func changedPIDs(previous, current map[int]fileStamp) []int {
	pids := make([]int, 0)
	for pid, stamp := range current {
		if old, ok := previous[pid]; !ok || old != stamp {
			pids = append(pids, pid)
		}
	}
	for pid := range previous {
		if _, ok := current[pid]; !ok {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}
