//go:build !peers_testenv

package update

// APIBase returns the GitHub API base URL. Release builds always use
// DefaultAPI; builds carrying the peers_testenv tag may override it.
func APIBase() string {
	return DefaultAPI
}
