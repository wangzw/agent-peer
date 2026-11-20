//go:build agentpeer_testenv

package update

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// unsetTestAPIEnv clears testAPIEnv for the test and restores the previous
// state when the test ends. t.Setenv cannot express "not set", and the strict
// case is about an absent override, so the test removes the variable outright.
func unsetTestAPIEnv(t *testing.T) {
	t.Helper()
	original, had := os.LookupEnv(testAPIEnv)
	if err := os.Unsetenv(testAPIEnv); err != nil {
		t.Fatalf("unsetting %s: %v", testAPIEnv, err)
	}
	t.Cleanup(func() {
		if !had {
			_ = os.Unsetenv(testAPIEnv)
			return
		}
		_ = os.Setenv(testAPIEnv, original)
	})
}

// TestTestBuildRejectsHTTPWithoutOverride proves that a test build without the
// fake API variable keeps the release rule: an http download URL is refused.
func TestTestBuildRejectsHTTPWithoutOverride(t *testing.T) {
	unsetTestAPIEnv(t)
	err := checkDownloadURL("http://127.0.0.1:18123/download/asset")
	if err == nil {
		t.Fatal("checkDownloadURL() = nil for an http URL without the test override, want an error")
	}
	if got := err.Error(); !strings.Contains(got, "https") {
		t.Fatalf("checkDownloadURL() error = %q, want it to mention https", got)
	}
}

// TestTestBuildAcceptsHTTPWithOverride proves that setting the fake API
// variable is what relaxes the check, so the local plain HTTP test server works.
func TestTestBuildAcceptsHTTPWithOverride(t *testing.T) {
	t.Setenv(testAPIEnv, "http://127.0.0.1:18123")
	if err := checkDownloadURL("http://127.0.0.1:18123/download/asset"); err != nil {
		t.Fatalf("checkDownloadURL() with the test override = %v, want nil", err)
	}
}

// TestTestBuildKeepsHTTPSAccepted proves that the strict rule still accepts
// https, so tightening the test build does not reject real download URLs.
func TestTestBuildKeepsHTTPSAccepted(t *testing.T) {
	unsetTestAPIEnv(t)
	if err := checkDownloadURL("https://example.com/download/asset"); err != nil {
		t.Fatalf("checkDownloadURL() for an https URL = %v, want nil", err)
	}
}

// TestTestBuildRedirectCheckFollowsOverride proves that the redirect rule is
// gated by the same variable as the initial URL check.
func TestTestBuildRedirectCheckFollowsOverride(t *testing.T) {
	target, err := url.Parse("http://127.0.0.1:18123/download/asset")
	if err != nil {
		t.Fatalf("parsing the redirect target: %v", err)
	}
	req := &http.Request{URL: target}

	unsetTestAPIEnv(t)
	strict := downloadClient(nil)
	if strict.CheckRedirect == nil {
		t.Fatal("downloadClient() without the test override has no redirect check")
	}
	if err := strict.CheckRedirect(req, nil); err == nil {
		t.Fatal("downloadClient() without the test override followed an http redirect, want an error")
	}

	t.Setenv(testAPIEnv, "http://127.0.0.1:18123")
	relaxed := downloadClient(nil)
	if relaxed.CheckRedirect != nil {
		t.Fatal("downloadClient() with the test override restricts redirects, want none")
	}
}
