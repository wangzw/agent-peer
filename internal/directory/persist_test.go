package directory

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// persistFailRenameOps fails every rename, leaving the temporary file for the
// caller to clean up the way atomicWrite does.
type persistFailRenameOps struct {
	realFileOps
}

func (persistFailRenameOps) Rename(_, _ string) error {
	return errors.New("rename failed")
}

// persistReadErrorOps makes ReadFile fail with err.
type persistReadErrorOps struct {
	realFileOps
	err error
}

func (o persistReadErrorOps) ReadFile(string) ([]byte, error) { return nil, o.err }

func mustSaveFile(t *testing.T, dir string, highest uint64, recs []fileRecord) {
	t.Helper()
	if err := saveFile(realFileOps{}, dir, highest, recs); err != nil {
		t.Fatalf("saveFile: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	recs := []fileRecord{
		{Raw: []byte{0x01, 0x02, 0x03}, Signature: []byte{0xaa, 0xbb}, Source: ""},
		{Raw: []byte("second"), Signature: []byte("sig-two"), Source: strings.Repeat("ab", 32)},
		{Raw: []byte{0xff}, Signature: nil, Source: strings.Repeat("cd", 32)},
	}
	const highest = 42
	mustSaveFile(t, dir, highest, recs)

	got, gotHighest, err := loadFile(realFileOps{}, dir)
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if gotHighest != highest {
		t.Fatalf("highest = %d, want %d", gotHighest, highest)
	}
	if !reflect.DeepEqual(got, recs) {
		t.Fatalf("records = %#v, want %#v", got, recs)
	}
}

func TestFileModeIs0600(t *testing.T) {
	dir := t.TempDir()
	mustSaveFile(t, dir, 1, nil)

	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want 0600", perm)
	}
}

func TestLoadMissingFile(t *testing.T) {
	recs, highest, err := loadFile(realFileOps{}, t.TempDir())
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if recs != nil || highest != 0 {
		t.Fatalf("got %#v, %d; want nil, 0", recs, highest)
	}
}

func TestLoadPropagatesReadError(t *testing.T) {
	_, _, err := loadFile(persistReadErrorOps{err: fs.ErrPermission}, t.TempDir())
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want fs.ErrPermission", err)
	}
	if errors.Is(err, errUnreadable) {
		t.Fatalf("a read error was reported as errUnreadable: %v", err)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	cases := map[string]string{
		"not json":  `{not json`,
		"format 2":  `{"format":2,"highest_issued":0,"records":[]}`,
		"no format": `{"highest_issued":3,"records":[]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, fileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("write file: %v", err)
			}
			recs, highest, err := loadFile(realFileOps{}, dir)
			if !errors.Is(err, errUnreadable) {
				t.Fatalf("err = %v, want errUnreadable", err)
			}
			if recs != nil || highest != 0 {
				t.Fatalf("got %#v, %d; want nil, 0", recs, highest)
			}
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("corrupt file was removed: %v", statErr)
			}
		})
	}
}

func TestAtomicWriteFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	oldRecs := []fileRecord{{Raw: []byte("old"), Signature: []byte("old-sig"), Source: ""}}
	mustSaveFile(t, dir, 7, oldRecs)

	path := filepath.Join(dir, fileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read old file: %v", err)
	}

	err = saveFile(persistFailRenameOps{}, dir, 8, []fileRecord{{Raw: []byte("new")}})
	if err == nil {
		t.Fatal("saveFile succeeded, want rename error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after failure: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("file changed after failed save: %q -> %q", before, after)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), fileName+".tmp-") {
			t.Fatalf("temporary file %q left behind", e.Name())
		}
	}
}

func TestSaveIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	recs := []fileRecord{
		{Raw: []byte("one"), Signature: []byte("sig-one"), Source: strings.Repeat("01", 32)},
		{Raw: []byte("two"), Signature: []byte("sig-two"), Source: ""},
	}
	mustSaveFile(t, dir, 5, recs)
	first, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	mustSaveFile(t, dir, 5, recs)
	second, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("file bytes differ between identical saves:\n%s\n%s", first, second)
	}
}
