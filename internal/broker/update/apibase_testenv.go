//go:build peers_testenv

package update

import "os"

// testAPIEnv names the variable that redirects APIBase in a test build.
const testAPIEnv = "PEERS_TEST_UPDATE_API"

// APIBase returns the GitHub API base URL, overridden by testAPIEnv when it is
// set. The override exists only in builds carrying the peers_testenv tag.
func APIBase() string {
	if api := os.Getenv(testAPIEnv); api != "" {
		return api
	}
	return DefaultAPI
}
