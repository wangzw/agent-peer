package directory

// Start wires the directory into link and starts the maintenance loop. It
// registers the reconciliation frame handlers and the link notifications,
// purges records that expired while the Broker was stopped, applies the
// configured public flag, and then runs the loop in its own goroutine. The
// broker's directory assembler calls Start during its Start phase, before link
// starts listening.
//
// Start returns ErrClosed when Close has already been called. Calling Start
// again on a started directory returns nil and changes nothing.
func (d *Directory) Start() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	// Accepting a record published by the local Broker calls the hook that
	// corrects it; the hook is only meaningful once the directory is live.
	d.ownHook = d.reconcileOwn

	now := d.now()
	if n := d.st.purgeExpired(now); n > 0 {
		d.dirty = true
	}
	d.mu.Unlock()

	d.registerReconcile()
	d.trackLinks()
	d.startupCheck(now)
	go d.runLoop()
	return nil
}
