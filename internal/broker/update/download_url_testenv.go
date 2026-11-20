//go:build agentpeer_testenv

package update

import (
	"net/http"
	"os"
)

// testAPIOverride reports whether this test build was pointed at a fake update
// API. The override is the only reason to accept anything but https: that API
// and its assets are plain HTTP servers on localhost.
func testAPIOverride() bool {
	return os.Getenv(testAPIEnv) != ""
}

// checkDownloadURL accepts any URL only when testAPIEnv names a fake API;
// otherwise it applies the release rule.
func checkDownloadURL(raw string) error {
	if testAPIOverride() {
		return nil
	}
	return strictCheckDownloadURL(raw)
}

// downloadClient leaves redirect handling untouched only when testAPIEnv names
// a fake API; otherwise redirects must stay on https, as in a release build.
func downloadClient(c *http.Client) *http.Client {
	if testAPIOverride() {
		return httpClient(c)
	}
	return strictDownloadClient(c)
}
