package directory

import "os"

// fileOps is the file-system seam used by the directory file. Production uses
// realFileOps; tests inject failures without relying on a read-only directory.
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
