package pairing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// fileOps is the file-system seam used by the peer table and by the identity
// code. Production uses realFileOps; tests inject failures without relying on
// read-only directories or on the real disk.
type fileOps interface {
	// WriteFile creates or truncates path, writes data, fsyncs and closes it.
	WriteFile(path string, data []byte, mode os.FileMode) error
	Rename(oldpath, newpath string) error
	SyncDir(dir string) error
	ReadFile(path string) ([]byte, error)
	Remove(path string) error
}

// realFileOps implements fileOps with the os package.
type realFileOps struct{}

func (realFileOps) WriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (realFileOps) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

func (realFileOps) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func (realFileOps) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (realFileOps) Remove(path string) error { return os.Remove(path) }

// atomicWrite writes data to a temporary file in the same directory, fsyncs it,
// renames it over path and fsyncs the directory. A same-directory temporary file
// is required for rename to be atomic. If the write or the rename fails the
// temporary file is removed and path is left untouched.
//
// A directory-sync failure after a successful rename is not reported as an
// error. The rename already replaced path, so the file on disk holds the new
// data; returning an error would make the caller keep the old in-memory table
// while disk has the new one, and a restart would silently undo the reported
// failure. The error is logged instead and the write counts as committed, which
// keeps memory and disk consistent. The only cost is that the rename might not
// survive a crash, which is strictly weaker than the durability the caller can
// observe.
func atomicWrite(ops fileOps, path string, data []byte, mode os.FileMode) error {
	if ops == nil {
		ops = realFileOps{}
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.tmp-%s", filepath.Base(path), tempSuffix()))
	if err := ops.WriteFile(tmp, data, mode); err != nil {
		_ = ops.Remove(tmp)
		return err
	}
	if err := ops.Rename(tmp, path); err != nil {
		_ = ops.Remove(tmp)
		return err
	}
	if err := ops.SyncDir(dir); err != nil {
		slog.Error("pairing: sync directory after rename", "dir", dir, "path", path, "error", err)
	}
	return nil
}

// tempSuffix returns a short random suffix for a temporary file name. Writes are
// serialized by the peer-table mutex, so a collision would require an external
// writer creating the same name; the random value makes that negligible.
func tempSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// readFileOrNotExist reads path and reports whether it was missing. A missing
// file is not an error; any other read error is returned unchanged.
func readFileOrNotExist(ops fileOps, path string) (data []byte, missing bool, err error) {
	if ops == nil {
		ops = realFileOps{}
	}
	data, err = ops.ReadFile(path)
	if err == nil {
		return data, false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	return nil, false, err
}
