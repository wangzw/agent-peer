package inbox

import "testing"

func TestREQAgentMessaging003_NormalizeName(t *testing.T) {
	// REQ-agent-messaging-003
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"trim-and-lower", " API Worker ", "api-worker"},
		{"single-space", "A B", "a-b"},
		{"consecutive-spaces-kept", "a  b", "a--b"},
		{"tab-is-a-control-char", "A\tB", "ab"},
		{"newline-is-a-control-char", "a\nb", "ab"},
		{"zero-width-format-char", "a" + string(rune(0x200b)) + "b", "ab"},
		{"leading-format-char", string(rune(0x200f)) + "Worker", "worker"},
		{"non-ascii-space", "a" + string(rune(0x00a0)) + "b", "a-b"},
		{"uppercase", "API", "api"},
		{"empty", "", ""},
		{"invalid-utf8", string([]byte{'a', 0xff}), "a" + string(rune(0xfffd))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeName(tc.in); got != tc.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
