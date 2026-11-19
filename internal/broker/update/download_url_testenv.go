//go:build agentpeer_testenv

package update

import "net/http"

// checkDownloadURL accepts any URL in a test build, where the fake API and its
// assets are plain HTTP servers on localhost.
func checkDownloadURL(string) error {
	return nil
}

// downloadClient leaves redirect handling untouched in a test build.
func downloadClient(c *http.Client) *http.Client {
	return httpClient(c)
}
