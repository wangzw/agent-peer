//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
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

	// postUpdateNoStepVersion is a next release whose binary has dropped the
	// post-update command. It answers the hidden command with the reserved exit
	// code 2, which the previous binary reads as "no post-update step".
	postUpdateNoStepVersion = "0.4.2"
	// postUpdateFailVersion is a next release whose post-update step rewrites
	// the service definition and then fails.
	postUpdateFailVersion = "0.4.3"

	postUpdateNoStepReleaseArtifact   = "update-post-nostep-" + postUpdateNoStepVersion + ".tar.gz"
	postUpdateNoStepChecksumsArtifact = "update-post-nostep-checksums-" + postUpdateNoStepVersion + ".txt"
	postUpdateFailReleaseArtifact     = "update-post-fail-" + postUpdateFailVersion + ".tar.gz"
	postUpdateFailChecksumsArtifact   = "update-post-fail-checksums-" + postUpdateFailVersion + ".txt"

	// postUpdateUnitPath is the system unit the install script writes.
	postUpdateUnitPath = "/etc/systemd/system/peers.service"
	// postUpdateDropInPath is the drop-in selfUpdateSetSystemUnitEnv writes. The
	// update must leave it alone.
	postUpdateDropInPath = "/etc/systemd/system/peers.service.d/e2e-update-api.conf"
	// postUpdateNextUnitMarker is the name of an environment assignment only the
	// next-version unit template carries. `systemctl show -p Environment` reads
	// it from the unit systemd has loaded, so finding it there proves both that
	// the new binary rewrote the unit and that daemon-reload ran.
	postUpdateNextUnitMarker = "PEERS_E2E_POST_UPDATE"
	// postUpdateNextUnitEnv is the assignment itself, as systemctl prints it.
	postUpdateNextUnitEnv = postUpdateNextUnitMarker + "=next-format"
)

// TestREQUpdate009_UpdateRewritesServiceUnitInNewFormat covers the core of
// REQ-update-009: the running candidate release replaces its binary with a
// next release whose systemd unit template differs, and the next binary
// rewrites the installed unit in its own format before the service manager
// restarts it. The next template sets an environment variable the old template
// does not, and `systemctl show -p Environment` must report it as loaded, so
// the test fails when the rewrite or the daemon-reload did not take effect.
// The drop-in survives unchanged and the Broker runs the new version.
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
	if want := "ExecStart=" + selfUpdateBinary + " service run --system --allow-degraded"; !strings.Contains(afterUnit, want) {
		t.Fatalf("the rewritten unit does not contain %q:\n%s", want, afterUnit)
	}
	if got := postUpdateReadFile(t, postUpdateDropInPath); got != beforeDropIn {
		t.Fatalf("the drop-in changed across the update\nbefore:\n%s\nafter:\n%s", beforeDropIn, got)
	}
	env := runWithEnv(t, "root", nil, "systemctl", "show", "-p", "Environment", "peers.service")
	if env.ExitCode != 0 {
		t.Fatalf("systemctl show -p Environment peers.service: exit %d\nstdout:\n%s\nstderr:\n%s",
			env.ExitCode, env.Stdout, env.Stderr)
	}
	if !strings.Contains(env.Stdout, postUpdateNextUnitEnv) {
		t.Fatalf("systemd did not load the next version's unit environment %q; the daemon-reload did not take effect:\n%s",
			postUpdateNextUnitEnv, env.Stdout)
	}
	if state := selfUpdateUnitState(t, "root", nil, "peers.service"); state != "active" {
		t.Fatalf("peers.service is %q after the update, want active", state)
	}
	selfUpdateWaitAuditEventFor(t, installAccount, postUpdateOldVersion, postUpdateNewVersion)
}

// postUpdateNoStepInternalCmd is the registration block the next release of
// TestREQUpdate010 drops from cmd/peers/internal_cmd.go. Without it the binary
// answers "peers internal post-update" with the unknown-command exit code 2,
// the value the update contract reserves for "no post-update step".
const postUpdateNoStepInternalCmd = "\t\t&cobra.Command{\n" +
	"\t\t\tUse:                \"post-update\",\n" +
	"\t\t\tHidden:             true,\n" +
	"\t\t\tDisableFlagParsing: true,\n" +
	"\t\t\tRunE: func(cmd *cobra.Command, args []string) error {\n" +
	"\t\t\t\treturn commandResult(a.postUpdateCmd(cmd.Context(), args))\n" +
	"\t\t\t},\n" +
	"\t\t},\n"

// postUpdateFailSystemUnit is the system unit header the next release of
// TestREQUpdate011 rewrites. The added environment assignment makes the new
// definition observably different from the installed one.
const postUpdateFailSystemUnit = "[Service]\nType=simple\nExecStart=%s service run --system --allow-degraded"

// postUpdateFailUnitMarker is the environment assignment only the failing next
// release's unit carries. After the rollback it must be gone.
const postUpdateFailUnitMarker = "PEERS_E2E_POST_UPDATE_FAIL=next-format"

// The next release of TestREQUpdate011 keeps the rewrite and the daemon-reload
// but returns postUpdateFailRewriteError instead of success, so it dies without
// running its own restore. postUpdateFailAfterRewrite is the tail of the Linux
// branch of service.PostUpdate that the overlay replaces.
const (
	postUpdateFailAfterRewrite = "\t\tif err := runSystemctlCommand(opts, &res, \"daemon-reload\"); err != nil {\n" +
		"\t\t\treturn restoreAfterFailure(opts, path, saved, err)\n" +
		"\t\t}\n" +
		"\t\treturn nil"
	postUpdateFailRewriteError       = "the next release rewrote the service definition and then failed: e2e"
	postUpdateFailAfterRewriteEdited = "\t\tif err := runSystemctlCommand(opts, &res, \"daemon-reload\"); err != nil {\n" +
		"\t\t\treturn restoreAfterFailure(opts, path, saved, err)\n" +
		"\t\t}\n" +
		"\t\treturn fmt.Errorf(\"" + postUpdateFailRewriteError + "\")"
)

// TestREQUpdate010_UpdateWithoutPostUpdateStep covers REQ-update-010: a next
// release whose binary no longer has "internal post-update" answers the hidden
// command with the reserved exit code 2, and the previous binary continues the
// update as if no step were needed. The update succeeds, the unit is left as
// installed, and the Broker runs the new version.
func TestREQUpdate010_UpdateWithoutPostUpdateStep(t *testing.T) {
	if !syscontainer.InContainer() {
		postUpdateStageOld(t)
		next := postUpdateBuildOverlayVersion(t, postUpdateNoStepVersion, postUpdateSourceEdit{
			path:        "cmd/peers/internal_cmd.go",
			original:    postUpdateNoStepInternalCmd,
			replacement: "",
		})
		postUpdateStageNext(t, postUpdateNoStepVersion, postUpdateNoStepReleaseArtifact,
			postUpdateNoStepChecksumsArtifact, next)
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}
	updateRequireFeature(t)

	fake := harness.StartFakeGitHub(t, harness.Release{
		Version:     postUpdateNoStepVersion,
		ArchiveName: harness.ReleaseAssetName(postUpdateNoStepVersion, "linux", runtime.GOARCH),
		Archive:     updateReadStaged(t, postUpdateNoStepReleaseArtifact),
		Checksums:   updateReadStaged(t, postUpdateNoStepChecksumsArtifact),
	})
	selfUpdateInstallSystemServiceFrom(t, fake, postUpdateOldArtifact, postUpdateOldVersion)

	beforeUnit := postUpdateReadFile(t, postUpdateUnitPath)

	res := syscontainer.RunAs(t, installAccount, selfUpdateAPIEnv(fake), "update", "--yes")
	combined := res.Stdout + res.Stderr
	if res.ExitCode != 0 {
		t.Fatalf("peers update --yes as %s exited %d\nstdout:\n%s\nstderr:\n%s",
			installAccount, res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(combined, postUpdateNoStepVersion) {
		t.Fatalf("the update output does not name version %s:\n%s", postUpdateNoStepVersion, combined)
	}

	selfUpdateWaitBrokerVersion(t, selfUpdateSystemSocket, postUpdateNoStepVersion)
	if got := updateBinaryVersion(t, selfUpdateBinary); got != "peers "+postUpdateNoStepVersion {
		t.Fatalf("the installed binary reports %q, want %q", got, "peers "+postUpdateNoStepVersion)
	}
	if got := postUpdateReadFile(t, postUpdateUnitPath); got != beforeUnit {
		t.Fatalf("the unit changed although the next release has no post-update step\nbefore:\n%s\nafter:\n%s", beforeUnit, got)
	}
	if state := selfUpdateUnitState(t, "root", nil, "peers.service"); state != "active" {
		t.Fatalf("peers.service is %q after the update, want active", state)
	}

	// The next binary must answer with the reserved code, so the update above
	// exercised "no step" rather than a step that happened to succeed.
	step := runWithEnv(t, "root", nil, selfUpdateBinary, "internal", "post-update", "--level", "system")
	if step.ExitCode != 2 {
		t.Fatalf("the next binary's internal post-update exited %d, want the reserved 2\nstdout:\n%s\nstderr:\n%s",
			step.ExitCode, step.Stdout, step.Stderr)
	}
}

// TestREQUpdate011_FailedPostUpdateRollsBack covers the non-zero half of
// REQ-update-011: the next release's post-update step rewrites the service
// definition and then fails with exit code 1. The update fails, the previous
// binary and the service definition come back byte for byte, the Broker keeps
// running with its old pid, and the command output carries the step's reason.
// The timeout half is covered by TestInstallPostUpdateTimeoutRollsBack in
// internal/broker/update.
func TestREQUpdate011_FailedPostUpdateRollsBack(t *testing.T) {
	if !syscontainer.InContainer() {
		postUpdateStageOld(t)
		next := postUpdateBuildOverlayVersion(t, postUpdateFailVersion,
			postUpdateSourceEdit{
				path:        "internal/broker/service/units.go",
				original:    postUpdateFailSystemUnit,
				replacement: "[Service]\nEnvironment=\"" + postUpdateFailUnitMarker + "\"\nType=simple\nExecStart=%s service run --system --allow-degraded",
			},
			postUpdateSourceEdit{
				path:        "internal/broker/service/postupdate.go",
				original:    postUpdateFailAfterRewrite,
				replacement: postUpdateFailAfterRewriteEdited,
			},
		)
		postUpdateStageNext(t, postUpdateFailVersion, postUpdateFailReleaseArtifact,
			postUpdateFailChecksumsArtifact, next)
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}
	updateRequireFeature(t)

	fake := harness.StartFakeGitHub(t, harness.Release{
		Version:     postUpdateFailVersion,
		ArchiveName: harness.ReleaseAssetName(postUpdateFailVersion, "linux", runtime.GOARCH),
		Archive:     updateReadStaged(t, postUpdateFailReleaseArtifact),
		Checksums:   updateReadStaged(t, postUpdateFailChecksumsArtifact),
	})
	selfUpdateInstallSystemServiceFrom(t, fake, postUpdateOldArtifact, postUpdateOldVersion)

	beforeUnit := postUpdateReadFile(t, postUpdateUnitPath)
	if strings.Contains(beforeUnit, postUpdateFailUnitMarker) {
		t.Fatalf("the installed unit already carries the failing next release's marker:\n%s", beforeUnit)
	}
	beforeHash := updateFileHash(t, selfUpdateBinary)
	beforePID, ok := systemServiceMainPID(t)
	if !ok {
		t.Fatal("systemctl did not report the system Broker's MainPID before the update")
	}

	res := syscontainer.RunAs(t, installAccount, selfUpdateAPIEnv(fake), "update", "--yes")
	combined := res.Stdout + res.Stderr
	if res.ExitCode == 0 {
		t.Fatalf("the update succeeded although the next post-update step failed\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
	}
	if !strings.Contains(combined, "post-update") {
		t.Fatalf("the failure output does not name the post-update step:\n%s", combined)
	}
	if !strings.Contains(combined, postUpdateFailRewriteError) {
		t.Fatalf("the failure output does not carry the step's reason:\n%s", combined)
	}

	if got := updateFileHash(t, selfUpdateBinary); got != beforeHash {
		t.Fatalf("the binary at %s changed after the failed update", selfUpdateBinary)
	}
	if got := updateBinaryVersion(t, selfUpdateBinary); got != "peers "+postUpdateOldVersion {
		t.Fatalf("the installed binary reports %q, want %q", got, "peers "+postUpdateOldVersion)
	}
	if got := postUpdateReadFile(t, postUpdateUnitPath); got != beforeUnit {
		t.Fatalf("the service definition was not restored after the failed post-update step\nbefore:\n%s\nafter:\n%s",
			beforeUnit, got)
	}
	if pid, ok := systemServiceMainPID(t); !ok || pid != beforePID {
		t.Fatalf("the system Broker MainPID changed from %d to %d after the failed update; it restarted", beforePID, pid)
	}
	selfUpdateWaitBrokerVersion(t, selfUpdateSystemSocket, postUpdateOldVersion)
	if state := selfUpdateUnitState(t, "root", nil, "peers.service"); state != "active" {
		t.Fatalf("peers.service is %q after the failed update, want active", state)
	}
}

// postUpdateStageHostAssets builds the old release and the next release of
// TestREQUpdate009 and stages them next to the test binary. It runs on the host.
func postUpdateStageHostAssets(t *testing.T) {
	t.Helper()

	postUpdateStageOld(t)
	postUpdateStageNext(t, postUpdateNewVersion, postUpdateNewReleaseArtifact,
		postUpdateNewChecksumsArtifact, postUpdateBuildNextVersion(t))
}

// postUpdateStageOld stages the install script and the running release. It runs
// on the host.
func postUpdateStageOld(t *testing.T) {
	t.Helper()

	stageInstallArchive(t)

	// The running release reads the fake API base through the test build's
	// hook; a next release is a normal build, as a real release is.
	old := harness.BuildBinaryVersion(t, postUpdateOldVersion, "linux", runtime.GOARCH, "peers_testenv")
	syscontainer.StageReleaseArtifact(t, postUpdateOldArtifact, updateReadFile(t, old), 0o755)
}

// postUpdateStageNext stages binary as version's release archive and checksum
// line. It runs on the host.
func postUpdateStageNext(t *testing.T, version, releaseArtifact, checksumsArtifact, binary string) {
	t.Helper()

	assetName := harness.ReleaseAssetName(version, "linux", runtime.GOARCH)
	archive := harness.ReleaseTarGz(t, binary)
	syscontainer.StageReleaseArtifact(t, releaseArtifact, archive, 0o644)
	syscontainer.StageReleaseArtifact(t, checksumsArtifact, harness.ChecksumLine(assetName, archive), 0o644)
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

	const original = "[Service]\nType=simple\nExecStart=%s service run --system --allow-degraded"
	replacement := "[Service]\nEnvironment=\"" + postUpdateNextUnitEnv + "\"\nType=simple\nExecStart=%s service run --system --allow-degraded"
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

// postUpdateSourceEdit replaces one occurrence of original in a source file
// with replacement for the next-version build.
type postUpdateSourceEdit struct {
	path        string
	original    string
	replacement string
}

// postUpdateBuildOverlayVersion builds version with an overlay that substitutes
// edited copies of the named source files. Each edit must match exactly one
// occurrence, so a tree change that renames the text fails the test instead of
// silently building an unmodified next release.
func postUpdateBuildOverlayVersion(t *testing.T, version string, edits ...postUpdateSourceEdit) string {
	t.Helper()

	root := harness.ModuleRoot(t)
	dir := t.TempDir()
	replace := make(map[string]string, len(edits))
	for i, edit := range edits {
		source := filepath.Join(root, edit.path)
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("reading %s: %v", source, err)
		}
		if got := strings.Count(string(data), edit.original); got != 1 {
			t.Fatalf("%s has %d matches of the text the next-version build replaces, want exactly 1", source, got)
		}
		modifiedPath := filepath.Join(dir, fmt.Sprintf("%d-%s", i, filepath.Base(edit.path)))
		modified := strings.Replace(string(data), edit.original, edit.replacement, 1)
		if err := os.WriteFile(modifiedPath, []byte(modified), 0o644); err != nil {
			t.Fatalf("writing the overlaid %s: %v", edit.path, err)
		}
		replace[source] = modifiedPath
	}

	overlayPath := filepath.Join(dir, "overlay.json")
	overlay, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replace})
	if err != nil {
		t.Fatalf("encoding the build overlay: %v", err)
	}
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		t.Fatalf("writing the build overlay: %v", err)
	}
	return harness.BuildBinaryVersionOverlay(t, version, "linux", runtime.GOARCH, overlayPath)
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
