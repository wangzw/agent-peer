// This file carries no e2e build tag so the label value and its host-side
// test compile without one.
package syscontainer

import "github.com/wangzw/peers/test/e2e/harness"

// ownerEnv carries this process's owner marker into the compose file, which
// puts it in the harness.OwnerLabel label on every service, network and
// volume. CleanResidue reads the label to remove only a dead owner's project.
const ownerEnv = "PEERS_E2E_OWNER"

// ownerLabelValue returns the value of harness.OwnerLabel for this process:
// "ape2e-<pid>-<start>-pidns<inode>". It is the same marker the harness puts in
// its project names, so harness.CleanResidue removes the project's containers,
// networks and volumes only after this process is gone.
func ownerLabelValue() string {
	return harness.OwnerMarker()
}
