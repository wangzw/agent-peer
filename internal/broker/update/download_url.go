//go:build !peers_testenv

package update

import "net/http"

// checkDownloadURL requires a download URL to use https. Hosts are not
// restricted: GitHub redirects assets to storage hosts, and the checksum still
// verifies the content.
func checkDownloadURL(raw string) error {
	return strictCheckDownloadURL(raw)
}

// downloadClient returns a copy of c whose redirects must stay on https.
func downloadClient(c *http.Client) *http.Client {
	return strictDownloadClient(c)
}
