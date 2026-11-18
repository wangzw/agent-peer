package directory

import "time"

// runLoop is the maintenance loop. It wakes every maintainInterval, or earlier
// when the next reconciliation is due, and runs one maintenance pass. Close
// stops it through d.stop; the goroutine closes loopDone on the way out so that
// Close can wait for the loop.
func (d *Directory) runLoop() {
	defer close(d.loopDone)

	lastReconcile := d.now()
	for {
		now := d.now()
		wait := maintainInterval
		if next := lastReconcile.Add(d.settings.ReconcileInterval()).Sub(now); next < wait {
			wait = next
		}
		if wait < 0 {
			wait = 0
		}

		t := d.clk.NewTimer(wait)
		select {
		case <-t.C():
		case <-d.stop:
			t.Stop()
			return
		}

		lastReconcile = d.maintain(d.now(), lastReconcile)
	}
}

// maintain runs one maintenance pass: it purges expired records, renews the
// local card and the active public topic declarations that are due, reconciles
// with every established peer once the interval has elapsed, and retries a
// pending file write. The instant of the last reconciliation is returned so the
// loop can schedule the next wake.
func (d *Directory) maintain(now, lastReconcile time.Time) time.Time {
	d.mu.Lock()
	if n := d.st.purgeExpired(now); n > 0 {
		d.dirty = true
	}
	d.mu.Unlock()

	d.renewDue(now)

	if now.Sub(lastReconcile) >= d.settings.ReconcileInterval() {
		d.reconcileAll()
		lastReconcile = now
	}

	d.persistIfDirty()
	if d.afterMaintain != nil {
		d.afterMaintain()
	}
	return lastReconcile
}
