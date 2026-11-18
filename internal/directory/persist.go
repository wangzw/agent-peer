package directory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

// fileName is the directory file kept in the data directory.
const fileName = "directory.json"

// fileFormat is the only directory file format this version writes and reads.
const fileFormat = 1

// errUnreadable reports that directory.json exists and can be read but cannot
// be used: its content does not parse or its format is unknown. The caller logs
// it and starts with an empty directory, which reconciliation can refill. A
// read failure is returned as itself so that startup fails instead of silently
// replacing the file.
var errUnreadable = errors.New("directory: directory.json is unreadable")

// directoryFile is the on-disk form of the directory.
type directoryFile struct {
	Format        int          `json:"format"`
	HighestIssued uint64       `json:"highest_issued"`
	Records       []fileRecord `json:"records"`
}

// fileRecord is one record as stored on disk. Raw and Signature hold the record
// bytes exactly as signed; encoding/json encodes []byte as base64. Source is the
// 64-character lowercase hexadecimal fingerprint of the link the record was
// accepted from, or the empty string for a locally issued record.
type fileRecord struct {
	Raw       []byte `json:"raw"`
	Signature []byte `json:"signature"`
	Source    string `json:"source"`
}

// saveFile writes format 1 with the given records in the given order to
// dir/directory.json. It does not sort; the caller is responsible for a stable
// order so that identical content produces identical bytes.
func saveFile(ops fileOps, dir string, highest uint64, recs []fileRecord) error {
	if ops == nil {
		ops = realFileOps{}
	}
	data, err := json.Marshal(directoryFile{
		Format:        fileFormat,
		HighestIssued: highest,
		Records:       recs,
	})
	if err != nil {
		return fmt.Errorf("directory: encode directory file: %w", err)
	}
	return atomicWrite(ops, filepath.Join(dir, fileName), data)
}

// loadFile returns the records and highest_issued stored in dir/directory.json
// unchanged. It does not verify signatures or validate Source. A missing file
// yields nil records, a zero highest and no error. A read failure is returned
// wrapped so that the caller fails startup. A parse failure or a format this
// version does not know yields errUnreadable so that the caller can log it and
// start with an empty directory; the file is left in place.
func loadFile(ops fileOps, dir string) (recs []fileRecord, highest uint64, err error) {
	if ops == nil {
		ops = realFileOps{}
	}
	path := filepath.Join(dir, fileName)
	data, err := ops.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("directory: read %s: %w", path, err)
	}
	var f directoryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, 0, errUnreadable
	}
	if f.Format != fileFormat {
		return nil, 0, errUnreadable
	}
	return f.Records, f.HighestIssued, nil
}

// atomicWrite writes data to a temporary file next to path, fsyncs it, renames
// it over path and fsyncs the containing directory. A same-directory temporary
// file is required for rename to be atomic. On any failure the temporary file is
// removed and the error is returned wrapped; path is left untouched.
func atomicWrite(ops fileOps, path string, data []byte) error {
	if ops == nil {
		ops = realFileOps{}
	}
	tmp := path + ".tmp-" + randomSuffix()
	if err := ops.WriteFile(tmp, data, 0o600); err != nil {
		_ = ops.Remove(tmp)
		return fmt.Errorf("directory: write %s: %w", tmp, err)
	}
	if err := ops.Rename(tmp, path); err != nil {
		_ = ops.Remove(tmp)
		return fmt.Errorf("directory: rename %s to %s: %w", tmp, path, err)
	}
	if err := ops.SyncDir(filepath.Dir(path)); err != nil {
		_ = ops.Remove(tmp)
		return fmt.Errorf("directory: sync %s: %w", filepath.Dir(path), err)
	}
	return nil
}

// randomSuffix returns 8 random bytes as lowercase hexadecimal for a temporary
// file name.
func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
