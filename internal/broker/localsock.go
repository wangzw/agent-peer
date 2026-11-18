package broker

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync"
	"syscall"
)

// LocalSocket is the Unix socket serving the local gRPC interface. It refuses
// to overwrite anything but a leftover socket, never lets the listener's Close
// unlink the path, and removes the path only when it still names this
// listener's socket.
type LocalSocket struct {
	mu     sync.Mutex
	path   string
	ln     *net.UnixListener
	dev    uint64
	ino    uint64
	closed bool
}

// BindLocalSocket binds the local interface to path with file mode perm. A
// stale socket left by a previous process is replaced; any other kind of file
// at path is left alone and reported as an error. The caller must hold the
// account lock, so that only the one running Broker ever tries this.
func BindLocalSocket(path string, perm os.FileMode) (*LocalSocket, error) {
	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace %s: not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist):
		// Nothing to replace.
	default:
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// The listener must never unlink the path on Close: by then another Broker
	// may own a freshly bound socket there.
	ln.SetUnlinkOnClose(false)

	if err := os.Chmod(path, perm); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("set mode %s: %w", path, err)
	}
	fi, err = os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("stat socket %s: %w", path, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("stat socket %s: missing file identity", path)
	}
	return &LocalSocket{
		path: path,
		ln:   ln,
		dev:  uint64(st.Dev),
		ino:  uint64(st.Ino),
	}, nil
}

// Listener returns the underlying listener so the caller can serve the local
// gRPC service on it.
func (s *LocalSocket) Listener() net.Listener {
	return s.ln
}

// Present reports whether path still names the socket this LocalSocket bound.
// It is the check the periodic runtime-directory review makes before deciding
// to bind again, and it guards Close from removing a replacement socket.
func (s *LocalSocket) Present() bool {
	fi, err := os.Lstat(s.path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(st.Dev) == s.dev && uint64(st.Ino) == s.ino
}

// Close closes the listener. When unlink is true and path still names this
// socket, it also removes the path. Close is safe to call more than once.
func (s *LocalSocket) Close(unlink bool) error {
	s.mu.Lock()
	ln := s.ln
	if !s.closed {
		s.closed = true
	}
	s.mu.Unlock()

	var closeErr error
	if ln != nil {
		closeErr = ln.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
	}
	if unlink && s.Present() {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if closeErr != nil {
				return errors.Join(closeErr, err)
			}
			return err
		}
	}
	return closeErr
}
