package pairing

import "context"

// Transport is injected by the broker during assembly. The link package
// implements it; pairing never imports link, so the module dependency stays
// link -> pairing.
//
// PairDial dials address and hands the connection to run. Errors caused by the
// dial itself must wrap ErrDialFailed; errors returned by run are returned
// unchanged. If run returns nil, the connection becomes a link and PairDial
// sends the link's Hello itself. Connect does not send Hello.
//
// Drop closes the link to fp. It is safe to call when no link exists and may be
// called more than once. pairing calls it outside the peer-table mutex.
type Transport interface {
	PairDial(ctx context.Context, address string, run func(PairConn) error) error
	Drop(fp Fingerprint)
}
