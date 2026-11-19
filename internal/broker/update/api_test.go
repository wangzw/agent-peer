package update

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestReturnsNewestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/wangzw/agent-peer/releases/latest"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = io.WriteString(w, `{"tag_name":"v0.2.0","html_url":"https://github.com/wangzw/agent-peer/releases/tag/v0.2.0"}`)
	}))
	t.Cleanup(srv.Close)

	got, err := Latest(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if got.Version != "0.2.0" {
		t.Errorf("Latest().Version = %q, want %q", got.Version, "0.2.0")
	}
	if want := "https://github.com/wangzw/agent-peer/releases/tag/v0.2.0"; got.URL != want {
		t.Errorf("Latest().URL = %q, want %q", got.URL, want)
	}
}

func TestLatestRejectsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("Latest() error = nil, want an error for a failing API")
	}
}

func TestLatestRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `not json`},
		{"missing tag", `{}`},
		{"tag is not a version", `{"tag_name":"latest"}`},
		{"tag is a prerelease", `{"tag_name":"v0.2.0-rc.1"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
				t.Fatal("Latest() error = nil, want an error for invalid content")
			}
		})
	}
}
