//go:build !agentpeer_testenv

package update

// APIBase returns the GitHub API base URL. Release builds always use
// DefaultAPI; builds carrying the agentpeer_testenv tag may override it.
func APIBase() string {
	return DefaultAPI
}
