package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	ccsock "github.com/PeterSR/claude-code-socket-transport"
)

const (
	// frameWriteTimeout bounds one frame write, from connecting to the receiver
	// closing the connection after it has taken the line.
	frameWriteTimeout = 5 * time.Second

	// frameEnvelopeTag is the element a receiving Claude Code session recognizes
	// as an attributed peer message.
	frameEnvelopeTag = "cross-session-message"
)

func init() {
	register("frame", cmdFrame)
}

func cmdFrame(args []string) error {
	return runFrame(args, os.Stdout)
}

// verbatimFrame mirrors the transport library's userFrame, including field
// order. The receiver decodes JSON, but matching the library keeps the bytes
// comparable with what the Broker actually sends.
type verbatimFrame struct {
	MsgV     int             `json:"msgV"`
	MsgID    string          `json:"msg_id"`
	Type     string          `json:"type"`
	Message  verbatimPayload `json:"message"`
	Priority string          `json:"priority"`
}

type verbatimPayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// buildVerbatimFrame returns one newline-terminated frame whose envelope carries
// fromName byte for byte: no cleaning and no quoting, so a probe can observe
// exactly how the receiver treats a hostile or unusual sender name. An empty
// fromName omits the attribute, as the transport library does with no name.
func buildVerbatimFrame(fromName, body, msgID string) ([]byte, error) {
	var envelope strings.Builder
	envelope.WriteString("<")
	envelope.WriteString(frameEnvelopeTag)
	if fromName != "" {
		envelope.WriteString(` from-name="`)
		envelope.WriteString(fromName)
		envelope.WriteString(`"`)
	}
	envelope.WriteString(">\n")
	envelope.WriteString(escapeFrameBody(body))
	envelope.WriteString("\n</")
	envelope.WriteString(frameEnvelopeTag)
	envelope.WriteString(">")

	encoded, err := json.Marshal(verbatimFrame{
		MsgV:     1,
		MsgID:    msgID,
		Type:     "user",
		Message:  verbatimPayload{Role: "user", Content: envelope.String()},
		Priority: "next",
	})
	if err != nil {
		return nil, fmt.Errorf("encoding frame: %w", err)
	}
	return append(encoded, '\n'), nil
}

// escapeFrameBody neutralizes a closing envelope tag in the body so the body
// cannot end the attribution envelope early. REQ-agent-messaging-023.
func escapeFrameBody(body string) string {
	return strings.ReplaceAll(body, "</"+frameEnvelopeTag, `<\/`+frameEnvelopeTag)
}

// runFrame implements "rsprobe frame --socket S --from-name N --body B
// [--mode verbatim|library] [--count K] [--fork-each] [--interval D]".
func runFrame(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("frame", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socket := fs.String("socket", "", "path to the session's inbox socket")
	fromName := fs.String("from-name", "", "from-name attribute")
	body := fs.String("body", "", "message body; {i} becomes the frame number and {pid} the writer's pid")
	mode := fs.String("mode", "verbatim", "how to write the frame: verbatim or library")
	count := fs.Int("count", 1, "number of frames to write")
	forkEach := fs.Bool("fork-each", false, "write every frame from a separate child process")
	interval := fs.Duration("interval", 0, "delay between frames")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *socket == "" {
		return errors.New("--socket is required")
	}
	if *mode != "verbatim" && *mode != "library" {
		return fmt.Errorf("unknown mode %q, want verbatim or library", *mode)
	}
	if *count < 1 {
		return fmt.Errorf("--count must be at least 1, got %d", *count)
	}

	if *forkEach {
		return runFrameForkEach(stdout, *socket, *fromName, *body, *mode, *count, *interval)
	}

	failed := 0
	for seq := 1; seq <= *count; seq++ {
		expanded := expandFrameBody(*body, seq, os.Getpid())
		msgID, elapsed, err := writeOneFrame(*mode, *socket, *fromName, expanded)
		if werr := writeFrameLine(stdout, seq, msgID, os.Getpid(), err, elapsed); werr != nil {
			return werr
		}
		if err != nil {
			failed++
		}
		if seq < *count && *interval > 0 {
			time.Sleep(*interval)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d frames failed", failed, *count)
	}
	return nil
}

// runFrameForkEach writes every frame through a fresh child process so each
// frame's writer has its own pid, which is what the receiver's peer-credential
// view reports. {i} is expanded here because the child always runs with
// --count 1; {pid} is left for the child, whose pid is the one that matters.
func runFrameForkEach(stdout io.Writer, socket, fromName, body, mode string, count int, interval time.Duration) error {
	failed := 0
	for seq := 1; seq <= count; seq++ {
		expanded := strings.ReplaceAll(body, "{i}", strconv.Itoa(seq))
		childArgs := []string{
			"frame",
			"--socket", socket,
			"--from-name", fromName,
			"--body", expanded,
			"--mode", mode,
			"--count", "1",
			"--interval", "0",
		}
		cmd := exec.Command(os.Args[0], childArgs...)
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		line := strings.TrimSuffix(string(out), "\n")
		if line == "" {
			line = "- 0 error:child produced no output 0"
		} else if fields := strings.SplitN(line, " ", 2); len(fields) == 2 {
			// The child numbered its single frame 1; renumber it here.
			line = fields[1]
		}
		if _, werr := fmt.Fprintf(stdout, "%d %s\n", seq, line); werr != nil {
			return werr
		}
		if err != nil {
			failed++
		}
		if seq < count && interval > 0 {
			time.Sleep(interval)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d frames failed", failed, count)
	}
	return nil
}

// writeOneFrame writes a single frame and reports its message id, elapsed time
// and error.
func writeOneFrame(mode, socketPath, fromName, body string) (string, time.Duration, error) {
	msgID, err := newFrameUUID()
	if err != nil {
		return "", 0, err
	}
	started := time.Now()
	switch mode {
	case "verbatim":
		line, err := buildVerbatimFrame(fromName, body, msgID)
		if err != nil {
			return msgID, time.Since(started), err
		}
		err = writeVerbatimFrame(socketPath, line)
		return msgID, time.Since(started), err
	default: // library
		client := &ccsock.Client{NoAuth: true, Timeout: frameWriteTimeout}
		_, err := client.SendToSocket(context.Background(), socketPath, ccsock.Message{
			Text:     body,
			Priority: ccsock.PriorityNext,
			FromName: fromName,
			MsgID:    msgID,
		})
		return msgID, time.Since(started), err
	}
}

// writeVerbatimFrame connects, writes one complete line, half-closes and waits
// for the receiver to close its side. Waiting turns a receiver that rejects the
// frame into an error instead of a silent success.
func writeVerbatimFrame(socketPath string, line []byte) error {
	conn, err := net.DialTimeout("unix", socketPath, frameWriteTimeout)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(frameWriteTimeout)); err != nil {
		return fmt.Errorf("setting deadline: %w", err)
	}
	if _, err := conn.Write(line); err != nil {
		return fmt.Errorf("writing: %w", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("%s is not a unix socket", socketPath)
	}
	if err := unixConn.CloseWrite(); err != nil {
		return fmt.Errorf("half-closing: %w", err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		return fmt.Errorf("waiting for the receiver to close: %w", err)
	}
	return nil
}

// expandFrameBody substitutes the per-frame placeholders.
func expandFrameBody(body string, seq, pid int) string {
	body = strings.ReplaceAll(body, "{i}", strconv.Itoa(seq))
	return strings.ReplaceAll(body, "{pid}", strconv.Itoa(pid))
}

// writeFrameLine prints one result line: sequence, message id, writer pid,
// outcome and elapsed milliseconds. Error text stays on the line, so newlines
// in it are folded into spaces.
func writeFrameLine(w io.Writer, seq int, msgID string, pid int, err error, elapsed time.Duration) error {
	status := "ok"
	if err != nil {
		status = "error:" + strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error())
	}
	_, werr := fmt.Fprintf(w, "%d %s %d %s %d\n", seq, msgID, pid, status, elapsed.Milliseconds())
	return werr
}

// newFrameUUID returns a random version 4 UUID in canonical form.
func newFrameUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating message id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
