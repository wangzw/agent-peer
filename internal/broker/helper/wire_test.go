package helper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestWireConcurrentWritesDoNotInterleave(t *testing.T) {
	const (
		writers   = 16
		perWriter = 50
		payload   = 64 << 10
		total     = writers * perWriter
	)

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	w := newMsgWriter(pw)

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			fill := byte('a' + g)
			for i := 0; i < perWriter; i++ {
				n := uint64(g*perWriter + i)
				body := make([]byte, 8+payload)
				binary.BigEndian.PutUint64(body[:8], n)
				for j := 8; j < len(body); j++ {
					body[j] = fill
				}
				if err := w.write(wrapperspb.Bytes(body)); err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	go func() {
		wg.Wait()
		close(errCh)
	}()

	r := newMsgReader(pr)
	seen := make([]bool, total)
	for i := 0; i < total; i++ {
		var msg wrapperspb.BytesValue
		if err := r.read(&msg); err != nil {
			_ = pr.CloseWithError(err)
			t.Fatalf("read message %d: %v", i, err)
		}
		if got, want := len(msg.Value), 8+payload; got != want {
			_ = pr.CloseWithError(fmt.Errorf("message %d has length %d", i, got))
			t.Fatalf("message %d length = %d, want %d", i, got, want)
		}
		n := binary.BigEndian.Uint64(msg.Value[:8])
		if n >= total {
			_ = pr.CloseWithError(fmt.Errorf("message %d has sequence %d", i, n))
			t.Fatalf("message %d has sequence %d, want < %d", i, n, total)
		}
		if seen[n] {
			_ = pr.CloseWithError(fmt.Errorf("sequence %d repeated", n))
			t.Fatalf("sequence %d was read twice", n)
		}
		seen[n] = true
	}
	_ = pw.Close()

	for err := range errCh {
		t.Fatalf("write: %v", err)
	}
	for n, ok := range seen {
		if !ok {
			t.Fatalf("sequence %d was never read", n)
		}
	}
}

func TestWireOversizeRejected(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	w := newMsgWriter(pw)

	go func() {
		_ = w.write(wrapperspb.Bytes(make([]byte, maxMessageSize+1)))
		_ = pw.Close()
	}()

	r := newMsgReader(pr)
	var msg wrapperspb.BytesValue
	err := r.read(&msg)
	// The reader rejects the size prefix and stops before consuming the body,
	// so close the read side to unblock the writer.
	_ = pr.CloseWithError(err)

	if err == nil {
		t.Fatal("read of oversize message returned nil error")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("read of oversize message returned io.EOF: %v", err)
	}
}

func TestWireTruncatedMessage(t *testing.T) {
	var buf bytes.Buffer
	if _, err := protodelim.MarshalTo(&buf, wrapperspb.Bytes(bytes.Repeat([]byte{'x'}, 1024))); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	full := buf.Bytes()

	var msg wrapperspb.BytesValue

	r := newMsgReader(bytes.NewReader(full[:len(full)/2]))
	if err := r.read(&msg); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read of truncated message = %v, want io.ErrUnexpectedEOF", err)
	}

	r = newMsgReader(bytes.NewReader(nil))
	if err := r.read(&msg); !errors.Is(err, io.EOF) {
		t.Fatalf("read of empty stream = %v, want io.EOF", err)
	}
}
