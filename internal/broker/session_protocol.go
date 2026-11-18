package broker

// sessionPeerProtocol is the inbox frame protocol implemented by the vendored
// transport. A session record that names another version is still registered
// as usual; the Broker only leaves a log clue for a registration that succeeds
// but cannot exchange messages.
const sessionPeerProtocol = 1

// checkSessionProtocolLocked logs a session registry frame protocol version the
// Broker does not implement. Missing (0) and the supported version stay quiet.
// The same agent and value are logged once, so repeated rereads do not repeat
// the record. Callers must hold Broker.mu.
func (b *Broker) checkSessionProtocolLocked(a *agent) {
	p := a.session.PeerProtocol
	if p == 0 || p == sessionPeerProtocol || p == a.warnedPeerProtocol {
		return
	}
	b.log.Warn(EvSessionProtocolUnrecognized,
		"agent", a.id,
		"pid", a.session.PID,
		"peer_protocol", p,
		"supported", sessionPeerProtocol,
	)
	a.warnedPeerProtocol = p
}
