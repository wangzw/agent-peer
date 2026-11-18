//go:build e2e

// Command fakepeer is an end-to-end test peer. It joins a real Broker's link
// through pairing and then serves directory records supplied by the test, so a
// test can present cards and announcements the Broker could not produce
// itself.
//
// Commands arrive as one JSON object per line on stdin. Events go out as one
// JSON object per line on stdout.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/directory/directorypb"
	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/link/linkpb"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// commandTimeout bounds one pairing or dial command.
const commandTimeout = 10 * time.Second

var _ pairing.Transport = (*lateTransport)(nil)

// lateTransport lets pairing.Open run before the Link exists. Pairing never
// dials during Open, so the Link is installed before the first command.
type lateTransport struct {
	mu sync.Mutex
	l  *link.Link
}

func (t *lateTransport) PairDial(ctx context.Context, address string, run func(pairing.PairConn) error) error {
	t.mu.Lock()
	l := t.l
	t.mu.Unlock()
	if l == nil {
		return pairing.ErrDialFailed
	}
	return l.PairDial(ctx, address, run)
}

func (t *lateTransport) Drop(fp pairing.Fingerprint) {
	t.mu.Lock()
	l := t.l
	t.mu.Unlock()
	if l != nil {
		l.Drop(fp)
	}
}

// emitter serializes event writes: link callbacks and the command loop emit
// from different goroutines.
type emitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newEmitter(w io.Writer) *emitter { return &emitter{enc: json.NewEncoder(w)} }

func (e *emitter) emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(v)
}

type readyEvent struct {
	Event       string `json:"event"`
	Fingerprint string `json:"fingerprint"`
}

type linkedEvent struct {
	Event string `json:"event"`
	Peer  string `json:"peer"`
}

type pairedEvent struct {
	Event       string `json:"event"`
	Fingerprint string `json:"fingerprint"`
}

type recordsSetEvent struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

type closedEvent struct {
	Event  string `json:"event"`
	Reason string `json:"reason"`
}

type errorEvent struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

// peerSet is the set of peers whose link is up.
type peerSet struct {
	mu    sync.Mutex
	peers map[pairing.Fingerprint]struct{}
}

func newPeerSet() *peerSet { return &peerSet{peers: make(map[pairing.Fingerprint]struct{})} }

func (s *peerSet) add(fp pairing.Fingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[fp] = struct{}{}
}

func (s *peerSet) remove(fp pairing.Fingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, fp)
}

func (s *peerSet) snapshot() []pairing.Fingerprint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pairing.Fingerprint, 0, len(s.peers))
	for fp := range s.peers {
		out = append(out, fp)
	}
	return out
}

// command is one stdin line.
type command struct {
	Cmd     string       `json:"cmd"`
	Address string       `json:"address"`
	Key     string       `json:"key"`
	Records []wireRecord `json:"records"`
}

type wireRecord struct {
	Raw       string `json:"raw"`
	Signature string `json:"signature"`
}

func main() {
	if err := run(); err != nil {
		newEmitter(os.Stdout).emit(errorEvent{Event: "error", Message: err.Error()})
		os.Exit(1)
	}
}

func run() error {
	if err := prepareHome(); err != nil {
		return err
	}
	if err := config.Load(config.ModeUser, ""); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	defer config.Close()

	lt := &lateTransport{}
	p, err := pairing.Open(lt)
	if err != nil {
		return fmt.Errorf("open pairing: %w", err)
	}

	l, err := link.New(link.Options{Peers: p, Settings: link.ConfigSettings()})
	if err != nil {
		return fmt.Errorf("open link: %w", err)
	}
	defer l.Close()
	lt.mu.Lock()
	lt.l = l
	lt.mu.Unlock()

	e := newEmitter(os.Stdout)
	set := &recordSet{}
	established := newPeerSet()

	l.HandleState(directorypb.KindRecordList, func(peer link.Fingerprint, _ link.Frame) {
		_ = l.Send(peer, link.Frame{Kind: directorypb.KindRecordList, Payload: marshal(set.list())})
	})
	l.HandleRequest(directorypb.KindRecordRequest, func(_ context.Context, _ link.Fingerprint, f link.Frame) link.Frame {
		req := new(directorypb.RecordRequest)
		if err := proto.Unmarshal(f.Payload, req); err != nil {
			req = new(directorypb.RecordRequest)
		}
		return link.Frame{Kind: directorypb.KindRecords, Payload: marshal(set.answer(req))}
	})
	l.Subscribe(func(peer link.Fingerprint, _ []byte) {
		established.add(peer)
		e.emit(linkedEvent{Event: "linked", Peer: peer.String()})
		_ = l.Send(peer, link.Frame{Kind: directorypb.KindRecordList, Payload: marshal(set.list())})
	}, func(peer link.Fingerprint) {
		established.remove(peer)
	})
	l.Start()

	e.emit(readyEvent{Event: "ready", Fingerprint: p.Fingerprint().String()})

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		handleCommand(scanner.Bytes(), p, l, set, established, e)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read commands: %w", err)
	}
	return nil
}

// prepareHome creates the data directory and, when absent, a config file that
// keeps the fake peer from listening on the default port.
func prepareHome() error {
	home := os.Getenv("AGENT_PEER_TEST_HOME")
	if home == "" {
		return errors.New("AGENT_PEER_TEST_HOME is not set")
	}
	dir := filepath.Join(home, ".agent-peer")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	switch _, err := os.Stat(path); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.WriteFile(path, []byte("broker:\n  listen: \"\"\n"), 0o600); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	case err != nil:
		return fmt.Errorf("stat config file: %w", err)
	}
	return nil
}

func handleCommand(line []byte, p *pairing.Pairing, l *link.Link, set *recordSet, established *peerSet, e *emitter) {
	var cmd command
	if err := json.Unmarshal(line, &cmd); err != nil {
		e.emit(errorEvent{Event: "error", Message: "decode command: " + err.Error()})
		return
	}

	switch cmd.Cmd {
	case "pair":
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		peer, _, err := p.Connect(ctx, cmd.Address, cmd.Key, "")
		if err != nil {
			e.emit(errorEvent{Event: "error", Message: err.Error()})
			return
		}
		e.emit(pairedEvent{Event: "paired", Fingerprint: peer.Fingerprint.String()})
	case "records":
		recs, err := decodeRecords(cmd.Records)
		if err != nil {
			e.emit(errorEvent{Event: "error", Message: err.Error()})
			return
		}
		set.set(recs)
		payload := marshal(set.list())
		for _, peer := range established.snapshot() {
			_ = l.Send(peer, link.Frame{Kind: directorypb.KindRecordList, Payload: payload})
		}
		e.emit(recordsSetEvent{Event: "records_set", Count: len(recs)})
	case "dial_unpaired":
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		var conn *grpc.ClientConn
		open := func(ctx context.Context) (linkpb.Link_ConnectClient, error) {
			tc := p.TLSConfig().Clone()
			c, err := grpc.NewClient(cmd.Address, grpc.WithTransportCredentials(credentials.NewTLS(tc)))
			if err != nil {
				return nil, err
			}
			conn = c
			return linkpb.NewLinkClient(c).Connect(ctx)
		}
		reason := dialUnpaired(ctx, open, set)
		if conn != nil {
			_ = conn.Close()
		}
		e.emit(closedEvent{Event: "closed", Reason: reason})
	default:
		e.emit(errorEvent{Event: "error", Message: "unknown command"})
	}
}

// decodeRecords turns the stdin form of a record into the wire message. Both
// fields are standard base64.
func decodeRecords(wire []wireRecord) ([]*directorypb.Record, error) {
	recs := make([]*directorypb.Record, 0, len(wire))
	for i, w := range wire {
		raw, err := base64.StdEncoding.DecodeString(w.Raw)
		if err != nil {
			return nil, fmt.Errorf("record %d: decode raw: %w", i, err)
		}
		sig, err := base64.StdEncoding.DecodeString(w.Signature)
		if err != nil {
			return nil, fmt.Errorf("record %d: decode signature: %w", i, err)
		}
		recs = append(recs, &directorypb.Record{Raw: raw, Signature: sig})
	}
	return recs, nil
}
