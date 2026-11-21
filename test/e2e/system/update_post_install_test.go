//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wangzw/peers/test/e2e/harness"
	"github.com/wangzw/peers/test/e2e/syscontainer"
)

const (
	// postUpdateOldVersion is the candidate build this scenario installs as the
	// running release.
	postUpdateOldVersion = "0.4.0"
	// postUpdateNewVersion is the next release. The test builds it itself from
	// the tree with an overlaid unit template.
	postUpdateNewVersion = "0.4.1"

	postUpdateOldArtifact          = "update-post-old-" + postUpdateOldVersion
	postUpdateNewReleaseArtifact   = "update-post-release-" + postUpdateNewVersion + ".tar.gz"
	postUpdateNewChecksumsArtifact = "update-post-checksums-" + postUpdateNewVersion + ".txt"

	// postUpdateUnitPath is the system unit the install script writes.
	postUpdateUnitPath = "/etc/systemd/system/peers.service"
	// postUpdateDropInPath is the drop-in selfUpdateSetSystemUnitEnv writes. The
	// update must leave it alone.
	postUpdateDropInPath = "/etc/systemd/system/peers.service.d/e2e-update-api.conf"
	// postUpdateNextUnitMarker appears only in the unit template of the
	// next-version build. Finding it in the installed unit proves the new
	// binary, not the old one, rewrote the service definition.
	postUpdateNextUnitMarker = "e2e-post-update-next-unit-format"
)

// TestREQUpdate009_UpdateRewritesServiceUnitInNewFormat covers the core of
// REQ-update-009: the running candidate release replaces its binary with a
// next release whose systemd unit template differs, and the next binary
// rewrites the installed unit in its own format before the service manager
// restarts it. The drop-in survives unchanged and the Broker runs the new
// version.
func TestREQUpdate009_UpdateRewritesServiceUnitInNewFormat(t *testing.T) {
	if !syscontainer.InContainer() {
		postUpdateStageHostAssets(t)
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}
	updateRequireFeature(t)

	fake := harness.StartFakeGitHub(t, harness.Release{
		Version:     postUpdateNewVersion,
		ArchiveName: harness.ReleaseAssetName(postUpdateNewVersion, "linux", runtime.GOARCH),
		Archive:     updateReadStaged(t, postUpdateNewReleaseArtifact),
		Checksums:   updateReadStaged(t, postUpdateNewChecksumsArtifact),
	})
	selfUpdateInstallSystemServiceFrom(t, fake, postUpdateOldArtifact, postUpdateOldVersion)

	beforeUnit := postUpdateReadFile(t, postUpdateUnitPath)
	if strings.Contains(beforeUnit, postUpdateNextUnitMarker) {
		t.Fatalf("the installed unit already carries the next-version marker:\n%s", beforeUnit)
	}
	beforeDropIn := postUpdateReadFile(t, postUpdateDropInPath)
	if got := updateBinaryVersion(t, selfUpdateBinary); got != "peers "+postUpdateOldVersion {
		t.Fatalf("the installed binary reports %q, want %q", got, "peers "+postUpdateOldVersion)
	}

	res := syscontainer.RunAs(t, installAccount, selfUpdateAPIEnv(fake), "update", "--yes")
	combined := res.Stdout + res.Stderr
	if res.ExitCode != 0 {
		t.Fatalf("peers update --yes as %s exited %d\nstdout:\n%s\nstderr:\n%s",
			installAccount, res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(combined, postUpdateNewVersion) {
		t.Fatalf("the update output does not name version %s:\n%s", postUpdateNewVersion, combined)
	}

	selfUpdateWaitBrokerVersion(t, selfUpdateSystemSocket, postUpdateNewVersion)
	if got := updateBinaryVersion(t, selfUpdateBinary); got != "peers "+postUpdateNewVersion {
		t.Fatalf("the installed binary reports %q, want %q", got, "peers "+postUpdateNewVersion)
	}

	afterUnit := postUpdateReadFile(t, postUpdateUnitPath)
	if !strings.Contains(afterUnit, postUpdateNextUnitMarker) {
		t.Fatalf("the unit was not rewritten in the next version's format:\n%s", afterUnit)
	}
	if want := "ExecStart=" + selfUpdateBinary + " broker --system --allow-degraded"; !strings.Contains(afterUnit, want) {
		t.Fatalf("the rewritten unit does not contain %q:\n%s", want, afterUnit)
	}
	if got := postUpdateReadFile(t, postUpdateDropInPath); got != beforeDropIn {
		t.Fatalf("the drop-in changed across the update\nbefore:\n%s\nafter:\n%s", beforeDropIn, got)
	}
	if state := selfUpdateUnitState(t, "root", nil, "peers.service"); state != "active" {
		t.Fatalf("peers.service is %q after the update, want active", state)
	}
	selfUpdateWaitAuditEventFor(t, installAccount, postUpdateOldVersion, postUpdateNewVersion)
}

// postUpdateStageHostAssets builds the old release and the next release and
// stages them next to the test binary. It runs on the host.
func postUpdateStageHostAssets(t *testing.T) {
	t.Helper()

	stageInstallArchive(t)

	// The running release reads the fake API base through the test build's
	// hook; the next release is a normal build, as a real release is.
	old := harness.BuildBinaryVersion(t, postUpdateOldVersion, "linux", runtime.GOARCH, "peers_testenv")
	syscontainer.StageReleaseArtifact(t, postUpdateOldArtifact, updateReadFile(t, old), 0o755)

	next := postUpdateBuildNextVersion(t)
	assetName := harness.ReleaseAssetName(postUpdateNewVersion, "linux", runtime.GOARCH)
	archive := harness.ReleaseTarGz(t, next)
	syscontainer.StageReleaseArtifact(t, postUpdateNewReleaseArtifact, archive, 0o644)
	syscontainer.StageReleaseArtifact(t, postUpdateNewChecksumsArtifact, harness.ChecksumLine(assetName, archive), 0o644)
}

// postUpdateBuildNextVersion builds the next release with a Go build overlay
// that changes the systemd system unit template. Nothing in the tree changes;
// the next-version unit format exists only in this test.
func postUpdateBuildNextVersion(t *testing.T) string {
	t.Helper()

	root := harness.ModuleRoot(t)
	unitsPath := filepath.Join(root, "internal", "broker", "service", "units.go")
	data, err := os.ReadFile(unitsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", unitsPath, err)
	}

	const original = "[Service]\nType=simple\nExecStart=%s broker --system"
	replacement := "[Service]\n# " + postUpdateNextUnitMarker + "\nType=simple\nExecStart=%s broker --system"
	if got := strings.Count(string(data), original); got != 1 {
		t.Fatalf("%s has %d matches of the system unit header, want exactly 1; the next-version build cannot change the unit format", unitsPath, got)
	}
	modified := strings.Replace(string(data), original, replacement, 1)

	dir := t.TempDir()
	modifiedPath := filepath.Join(dir, "units.go")
	if err := os.WriteFile(modifiedPath, []byte(modified), 0o644); err != nil {
		t.Fatalf("writing the overlaid units.go: %v", err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	overlay, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{unitsPath: modifiedPath}})
	if err != nil {
		t.Fatalf("encoding the build overlay: %v", err)
	}
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		t.Fatalf("writing the build overlay: %v", err)
	}

	return harness.BuildBinaryVersionOverlay(t, postUpdateNewVersion, "linux", runtime.GOARCH, overlayPath)
}

// postUpdateReadFile reads a file inside the container.
func postUpdateReadFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
