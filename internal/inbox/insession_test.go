package inbox

import "testing"

func TestInSession(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	if InSession() {
		t.Fatalf("InSession() = true with an empty CLAUDE_CODE_MESSAGING_SOCKET")
	}
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/session/inbox.sock")
	if !InSession() {
		t.Fatalf("InSession() = false with CLAUDE_CODE_MESSAGING_SOCKET set")
	}
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	if InSession() {
		t.Fatalf("InSession() = true after clearing CLAUDE_CODE_MESSAGING_SOCKET")
	}
}
