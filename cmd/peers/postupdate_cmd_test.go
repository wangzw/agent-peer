package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/broker/service"
)

// setPostUpdateSeams installs the option and operation seams for one test. A
// nil function leaves that seam unchanged.
func setPostUpdateSeams(
	t *testing.T,
	options func(bool) (service.Options, error),
	post func(service.Options) error,
) {
	t.Helper()
	previousOptions, previousPost := defaultOptionsFn, postUpdateFn
	if options != nil {
		defaultOptionsFn = options
	}
	if post != nil {
		postUpdateFn = post
	}
	t.Cleanup(func() {
		defaultOptionsFn = previousOptions
		postUpdateFn = previousPost
	})
}

func TestPostUpdateCmdMapsLevel(t *testing.T) {
	cases := []struct {
		level      string
		wantSystem bool
	}{
		{"user", false},
		{"system", true},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var (
				optionsSystem bool
				gotSystem     bool
				calls         int
			)
			setPostUpdateSeams(t,
				func(system bool) (service.Options, error) {
					optionsSystem = system
					return service.Options{System: system, Exe: "/usr/local/bin/peers"}, nil
				},
				func(opts service.Options) error {
					calls++
					gotSystem = opts.System
					return nil
				},
			)

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), []string{"internal", "post-update", "--level", tc.level}); got != exitOK {
				t.Fatalf("run(internal post-update --level %s) = %d, want %d", tc.level, got, exitOK)
			}
			if optionsSystem != tc.wantSystem {
				t.Errorf("DefaultOptions(system) got %t, want %t", optionsSystem, tc.wantSystem)
			}
			if calls != 1 || gotSystem != tc.wantSystem {
				t.Errorf("PostUpdate calls = %d with system %t, want one call with %t", calls, gotSystem, tc.wantSystem)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestPostUpdateCmdRejectsUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing level",
			args: []string{"internal", "post-update"},
			want: "peers internal post-update: missing --level; want system or user\n",
		},
		{
			name: "invalid level",
			args: []string{"internal", "post-update", "--level", "host"},
			want: "peers internal post-update: invalid --level \"host\"; want system or user\n",
		},
		{
			name: "unexpected argument",
			args: []string{"internal", "post-update", "--level", "user", "extra"},
			want: "peers internal post-update: unexpected argument \"extra\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			setPostUpdateSeams(t, nil, func(service.Options) error {
				called = true
				return nil
			})

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitUsage)
			}
			if called {
				t.Fatalf("run(%q) reached PostUpdate, want it rejected before dispatch", tc.args)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if got := stderr.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
			if strings.Contains(stderr.String(), "usage: peers") {
				t.Errorf("stderr = %q, want the hidden command to print no usage", stderr.String())
			}
		})
	}
}

func TestPostUpdateCmdReportsFailure(t *testing.T) {
	setPostUpdateSeams(t,
		func(bool) (service.Options, error) { return service.Options{Exe: "/usr/local/bin/peers"}, nil },
		func(service.Options) error { return errors.New("boom") },
	)

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"internal", "post-update", "--level", "system"}); got != exitFailure {
		t.Fatalf("run(internal post-update) = %d, want %d", got, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if want := "peers internal post-update: boom\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestPostUpdateCmdReportsOptionFailure(t *testing.T) {
	var called bool
	setPostUpdateSeams(t,
		func(bool) (service.Options, error) { return service.Options{}, errors.New("no executable") },
		func(service.Options) error {
			called = true
			return nil
		},
	)

	a, _, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"internal", "post-update", "--level", "user"}); got != exitFailure {
		t.Fatalf("run(internal post-update) = %d, want %d", got, exitFailure)
	}
	if called {
		t.Fatal("PostUpdate ran although the options could not be built")
	}
	if want := "peers internal post-update: no executable\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}
