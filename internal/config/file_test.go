package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestConfigFileRoundTripPreservesCommentsAndOrder(t *testing.T) {
	body := `# head comment
broker:
  # alias comment
  alias: old  # inline comment
  public: true

send:
  request_timeout: 20s

custom:
  keep: me
`
	doc, err := parseConfigFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.setKey("broker.alias", stringValue("new-alias")); err != nil {
		t.Fatal(err)
	}
	out, err := doc.bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"# head comment", "# alias comment", "# inline comment", "custom:", "keep: me"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lost %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "public:") > strings.Index(text, "send:") {
		t.Errorf("key order changed:\n%s", text)
	}
	back, err := parseConfigFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.values["broker.alias"].String; got != "new-alias" {
		t.Errorf("alias = %q, want new-alias", got)
	}
	if !back.values["broker.public"].Bool {
		t.Error("public was not preserved")
	}
}

func TestConfigFilePreservesBlockSequenceStyle(t *testing.T) {
	body := `broker:
  advertise_extra:
    - a.example
    - b.example
  listen: ":17443"
`
	doc, err := parseConfigFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.setKey("broker.public", boolValue(true)); err != nil {
		t.Fatal(err)
	}
	out, err := doc.bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "- a.example") || !strings.Contains(text, "- b.example") {
		t.Errorf("block sequence style changed:\n%s", text)
	}
}

func TestConfigFileCommentOnlyFirstSet(t *testing.T) {
	doc, err := parseConfigFile([]byte("# keep this\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.setKey("broker.alias", stringValue("hostA-17443")); err != nil {
		t.Fatal(err)
	}
	out, err := doc.bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "# keep this") {
		t.Errorf("comment-only header lost:\n%s", string(out))
	}
	back, err := parseConfigFile(out)
	if err != nil {
		t.Fatalf("encoded file does not parse: %v\n%s", err, out)
	}
	if back.values["broker.alias"].String != "hostA-17443" {
		t.Errorf("alias = %q", back.values["broker.alias"].String)
	}
}

func TestConfigFileParseErrors(t *testing.T) {
	cases := []string{
		"broker: [\n",
		"broker: plain\n",
		"broker:\n  alias: a\n  alias: b\n",
		"broker:\n  alias: a\n---\nbroker:\n  alias: b\n",
		"base: &b\n  alias: a\nbroker:\n  <<: *b\n",
	}
	for _, body := range cases {
		if _, err := parseConfigFile([]byte(body)); err == nil {
			t.Errorf("expected a parse error for:\n%s", body)
		}
	}
}

func TestConfigFileCreateAndChmod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeConfigFile(path, []byte("broker:\n  alias: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestConfigFileInPlacePreservesMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("broker:\n  alias: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	wantSys := before.Sys().(*syscall.Stat_t)
	if err := writeConfigFile(path, []byte("broker:\n  alias: new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	gotSys := after.Sys().(*syscall.Stat_t)
	if gotSys.Ino != wantSys.Ino {
		t.Errorf("inode changed: %d -> %d", wantSys.Ino, gotSys.Ino)
	}
	if gotSys.Uid != wantSys.Uid || gotSys.Gid != wantSys.Gid {
		t.Errorf("owner changed: %d:%d -> %d:%d", wantSys.Uid, wantSys.Gid, gotSys.Uid, gotSys.Gid)
	}
	if perm := after.Mode().Perm(); perm != 0o640 {
		t.Errorf("mode = %o, want 640", perm)
	}
}

func TestREQBrokerConfig002_SetPreservesFileMetadata(t *testing.T) {
	c, path := newValueCore(t, "broker:\n  alias: old\n", "")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	wantSys := before.Sys().(*syscall.Stat_t)
	c.mu.Lock()
	_, err = c.setLocked(Actor{}, "broker.public", boolValue(true))
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	gotSys := after.Sys().(*syscall.Stat_t)
	if gotSys.Ino != wantSys.Ino || gotSys.Uid != wantSys.Uid || gotSys.Gid != wantSys.Gid {
		t.Errorf("metadata changed: inode %d->%d uid %d->%d gid %d->%d",
			wantSys.Ino, gotSys.Ino, wantSys.Uid, gotSys.Uid, wantSys.Gid, gotSys.Gid)
	}
	if perm := after.Mode().Perm(); perm != 0o640 {
		t.Errorf("mode = %o, want 640", perm)
	}
}

func TestConfigFileWriteFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeConfigFile(filepath.Join(blocker, "config.yaml"), []byte("x: y\n"), 0o600)
	if err == nil {
		t.Fatal("expected a write error")
	}
}

func TestConfigFileNodeFor(t *testing.T) {
	doc, err := parseConfigFile([]byte("broker:\n  alias: a\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n := doc.nodeFor("broker.alias"); n == nil || n.Value != "a" {
		t.Fatalf("nodeFor(alias) = %+v", n)
	}
	if n := doc.nodeFor("broker.public"); n != nil {
		t.Fatalf("nodeFor(public) = %+v, want nil", n)
	}
}
