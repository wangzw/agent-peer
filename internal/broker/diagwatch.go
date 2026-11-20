package broker

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// configDirWatch reports changes to the configuration file while a degraded
// Broker runs diagnostics only. config's own watcher never starts when Load
// fails, so the degraded loop watches the directory itself.
type configDirWatch struct {
	events chan struct{}

	fsw  *fsnotify.Watcher
	log  *slog.Logger
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// startConfigDirWatch watches dir for changes to configFile. A watcher that
// cannot be created is logged and inert; the minute retry still runs.
func startConfigDirWatch(configFile string, log *slog.Logger) *configDirWatch {
	if log == nil {
		log = slog.Default()
	}
	w := &configDirWatch{
		events: make(chan struct{}, 1),
		log:    log,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	if configFile == "" {
		close(w.done)
		return w
	}
	fsw, err := fsnotify.NewWatcher()
	if err == nil {
		err = fsw.Add(filepath.Dir(configFile))
	}
	if err != nil {
		if log != nil {
			log.Warn("degraded_config_watch_failed", "error", err)
		}
		if fsw != nil {
			_ = fsw.Close()
		}
		close(w.done)
		return w
	}
	w.fsw = fsw
	go w.run(baseName(configFile))
	return w
}

func baseName(path string) string { return filepath.Base(path) }

func (w *configDirWatch) run(name string) {
	defer close(w.done)
	var (
		timer  *time.Timer
		timing <-chan time.Time
	)
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(50 * time.Millisecond)
		} else {
			timer.Reset(50 * time.Millisecond)
		}
		timing = timer.C
	}
	for {
		select {
		case <-w.stop:
			if timer != nil {
				timer.Stop()
			}
			_ = w.fsw.Close()
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if baseName(ev.Name) != name {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			arm()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if w.log != nil {
				w.log.Warn("degraded_config_watch_error", "error", err)
			}
			// A watcher error only costs the immediate retry; the minute
			// tick still drives the loop.
		case <-timing:
			// arm reinstalls timing on the next change; keep it nil until then
			// so a stale firing cannot wake the loop twice.
			timing = nil
			select {
			case w.events <- struct{}{}:
			default:
			}
		}
	}
}

func (w *configDirWatch) stopNow() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if w.fsw != nil {
			close(w.stop)
		}
	})
	<-w.done
}
