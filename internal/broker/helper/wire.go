package helper

import (
	"bufio"
	"bytes"
	"io"
	"sync"

	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/proto"
)

// maxMessageSize bounds one framed message so that neither end of a pipe can
// make the other allocate an arbitrary amount of memory.
const maxMessageSize = 4 << 20

// msgWriter writes length-delimited protobuf messages to a pipe. It serializes
// complete messages, so concurrent callers never interleave their bytes.
type msgWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newMsgWriter(w io.Writer) *msgWriter {
	return &msgWriter{w: w}
}

// write encodes msg and writes it with a single Write call. Holding the mutex
// across that call keeps one message's bytes contiguous even when other
// goroutines write concurrently.
func (m *msgWriter) write(msg proto.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var buf bytes.Buffer
	if _, err := protodelim.MarshalTo(&buf, msg); err != nil {
		return err
	}
	n, err := m.w.Write(buf.Bytes())
	if err != nil {
		return err
	}
	if n != buf.Len() {
		return io.ErrShortWrite
	}
	return nil
}

// msgReader reads length-delimited protobuf messages from a pipe.
type msgReader struct {
	r *bufio.Reader
}

func newMsgReader(r io.Reader) *msgReader {
	if br, ok := r.(*bufio.Reader); ok {
		return &msgReader{r: br}
	}
	return &msgReader{r: bufio.NewReader(r)}
}

// read reads one message. It returns io.EOF when the stream ends at a message
// boundary and io.ErrUnexpectedEOF when it ends in the middle of a message.
func (m *msgReader) read(msg proto.Message) error {
	opts := protodelim.UnmarshalOptions{MaxSize: maxMessageSize}
	return opts.UnmarshalFrom(m.r, msg)
}
