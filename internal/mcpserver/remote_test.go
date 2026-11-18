package mcpserver

import "testing"

// TestNormalizeRemote covers REQ-topics-017: the same repository written in
// different forms must normalize to the same topic name, and an address that
// does not name a remote repository must not produce one.
func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		// The three common forms must agree. Each takes a different branch of
		// the function, so a difference in the result shows up here.
		{
			name: "scp-like form",
			raw:  "git@github.com:org/repo.git",
			want: "github.com/org/repo",
			ok:   true,
		},
		{
			name: "ssh URL",
			raw:  "ssh://git@github.com/org/repo.git",
			want: "github.com/org/repo",
			ok:   true,
		},
		{
			name: "https URL",
			raw:  "https://github.com/org/repo",
			want: "github.com/org/repo",
			ok:   true,
		},
		{
			name: "http URL",
			raw:  "http://host/x",
			want: "host/x",
			ok:   true,
		},
		{
			name: "ssh URL with user and port",
			raw:  "ssh://git@host:2222/org/repo",
			want: "host/org/repo",
			ok:   true,
		},
		// User information and the port must be dropped, the host must be
		// lower-cased, the path must keep its case, and the trailing slash
		// must not stop the .git suffix from being removed.
		{
			name: "URL with user, port, mixed case and trailing slash",
			raw:  "https://user:p%40ss@GitHub.COM:8443/Org/Repo.git/",
			want: "github.com/Org/Repo",
			ok:   true,
		},
		// The digits after the colon in the scp-like form are part of the
		// path, not a port.
		{
			name: "scp-like form with a numeric path prefix",
			raw:  "git@host:2222/org/repo.git",
			want: "host/2222/org/repo",
			ok:   true,
		},
		// Only the last "@" of the host part separates the user name from the
		// host; an earlier "@" belongs to the user name.
		{
			name: "scp-like form with more than one @",
			raw:  "user@a@host:org/repo",
			want: "host/org/repo",
			ok:   true,
		},
		// An IPv6 host must lose its brackets and its port.
		{
			name: "URL with IPv6 host",
			raw:  "ssh://[::1]:22/org/repo",
			want: "::1/org/repo",
			ok:   true,
		},
		// The git scheme is accepted.
		{
			name: "git URL",
			raw:  "git://host/org/repo",
			want: "host/org/repo",
			ok:   true,
		},
		// The scheme is matched case-insensitively.
		{
			name: "upper-case scheme",
			raw:  "HTTPS://Host/x",
			want: "host/x",
			ok:   true,
		},
		// Surrounding whitespace is ignored.
		{
			name: "surrounding whitespace",
			raw:  "  git@github.com:org/repo.git\n",
			want: "github.com/org/repo",
			ok:   true,
		},
		// A file URL and local paths must not produce a topic.
		{
			name: "file URL",
			raw:  "file:///srv/repo.git",
			want: "",
			ok:   false,
		},
		{
			name: "absolute local path",
			raw:  "/srv/repo.git",
			want: "",
			ok:   false,
		},
		{
			name: "relative local path",
			raw:  "../repo",
			want: "",
			ok:   false,
		},
		// A colon after the first slash does not make this the scp-like form.
		{
			name: "local path with a colon in a directory name",
			raw:  "./a:b/c",
			want: "",
			ok:   false,
		},
		// Schemes outside the accepted set are rejected.
		{
			name: "unlisted scheme",
			raw:  "ftp://host/x",
			want: "",
			ok:   false,
		},
		// A URL with no repository path must not yield "host/".
		{
			name: "URL without a path",
			raw:  "https://host",
			want: "",
			ok:   false,
		},
		{
			name: "URL whose path is only the .git suffix",
			raw:  "https://host/.git",
			want: "",
			ok:   false,
		},
		{
			name: "empty string",
			raw:  "",
			want: "",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeRemote(tt.raw)
			if ok != tt.ok {
				t.Fatalf("normalizeRemote(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("normalizeRemote(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
