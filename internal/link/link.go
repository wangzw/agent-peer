package link

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Link maintains the authenticated frame channel to each peer.
type Link struct {
	peers    Peers
	settings Settings
	clock    Clock
	logger   *slog.Logger

	protocolVersion uint32 // Options.ProtocolVersion, or linkpb.ProtocolVersion when 0; not a test knob

	// knobs, fixed after New; tests change them with SetTestKnobs before Listen or Start
	sendQueueSize      int                          // 1024
	firstFrameTimeout  time.Duration                // 10s
	helloTimeout       time.Duration                // 10s
	rejectLinger       time.Duration                // 1s
	maxUnauthenticated int                          // 64
	enforcementMinTime time.Duration                // 5s
	localNets          func() ([]*net.IPNet, error) // nil in production: the dial loop then uses defaultLocalNets
	onDial             func(address string)         // nil in production
	randRead           func([]byte) (int, error)    // crypto/rand.Read

	rootCtx    context.Context
	cancelRoot context.CancelFunc
	wg         sync.WaitGroup

	mu     sync.Mutex // guards the fields marked "guarded by mu" in later blocks
	closed bool       // guarded by mu

	// --- records and link up/down notifications ---
	records  map[Fingerprint]*record       // guarded by mu
	dropGen  map[Fingerprint]uint64        // guarded by mu
	dropping map[Fingerprint]chan struct{} // guarded by mu
	subMu    sync.RWMutex
	subs     []subscription
	notifier *notifier
}
