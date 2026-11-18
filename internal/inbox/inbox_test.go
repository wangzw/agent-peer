package inbox

import (
	"testing"
)

// TestInboxAPI_Shapes pins the exported signatures the broker depends on.
func TestInboxAPI_Shapes(t *testing.T) {
	api := struct {
		lookup func(configDir, socketPath string) (Session, error)
		reread func(s Session) (Session, error)
		watch  func(configDir string, onChange func(int)) (stop func(), err error)
	}{
		lookup: Lookup,
		reread: Reread,
		watch:  Watch,
	}
	if api.lookup == nil || api.reread == nil || api.watch == nil {
		t.Fatal("an exported function is missing")
	}
}
