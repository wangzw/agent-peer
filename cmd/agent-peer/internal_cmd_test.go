package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// setAccountHelper installs fn as the account-helper seam for one test.
func setAccountHelper(t *testing.T, fn func(context.Context, io.Reader, io.Writer) error) {
	t.Helper()
	previous := runAccountHelperFn
	runAccountHelperFn = fn
	t.Cleanup(func() { runAccountHelperFn = previous })
}

// setAccessCheck installs fn as the access-check seam for one test.
func setAccessCheck(t *testing.T, fn func(io.Reader, io.Writer) error) {
	t.Helper()
	previous := runAccessCheckFn
	runAccessCheckFn = fn
	t.Cleanup(func() { runAccessCheckFn = previous })
}

type helperContextKey struct{}

func TestAccountHelperRoutesStreamsAndContext(t *testing.T) {
	var (
		gotContext context.Context
		gotStdin   io.Reader
		gotStdout  io.Writer
	)
	setAccountHelper(t, func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
		gotContext = ctx
		gotStdin = stdin
		gotStdout = stdout
		_, err := io.WriteString(stdout, "served\n")
		return err
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	ctx := context.WithValue(context.Background(), helperContextKey{}, "marker")
	if got := a.run(ctx, []string{"internal", "account-helper"}); got != exitOK {
		t.Fatalf("run(internal account-helper) = %d, want %d", got, exitOK)
	}
	if gotContext == nil || gotContext.Value(helperContextKey{}) != "marker" {
		t.Fatalf("helper context = %v, want the caller's context", gotContext)
	}
	if gotStdin != a.stdin {
		t.Fatalf("helper stdin = %T, want the app's stdin", gotStdin)
	}
	if gotStdout != a.stdout {
		t.Fatalf("helper stdout = %T, want the app's stdout", gotStdout)
	}
	if want := "served\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestAccessCheckRoutesStreams(t *testing.T) {
	var (
		gotStdin  io.Reader
		gotStdout io.Writer
	)
	setAccessCheck(t, func(stdin io.Reader, stdout io.Writer) error {
		gotStdin = stdin
		gotStdout = stdout
		_, err := io.WriteString(stdout, "checked\n")
		return err
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"internal", "access-check"}); got != exitOK {
		t.Fatalf("run(internal access-check) = %d, want %d", got, exitOK)
	}
	if gotStdin != a.stdin {
		t.Fatalf("access-check stdin = %T, want the app's stdin", gotStdin)
	}
	if gotStdout != a.stdout {
		t.Fatalf("access-check stdout = %T, want the app's stdout", gotStdout)
	}
	if want := "checked\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestInternalHelpersReportErrors(t *testing.T) {
	cases := []struct {
		args    []string
		install func(*testing.T)
		want    string
	}{
		{
			args: []string{"internal", "account-helper"},
			install: func(t *testing.T) {
				setAccountHelper(t, func(context.Context, io.Reader, io.Writer) error {
					return errors.New("boom")
				})
			},
			want: "agent-peer internal account-helper: boom\n",
		},
		{
			args: []string{"internal", "access-check"},
			install: func(t *testing.T) {
				setAccessCheck(t, func(io.Reader, io.Writer) error {
					return errors.New("boom")
				})
			},
			want: "agent-peer internal access-check: boom\n",
		},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			tc.install(t)
			a, stdout, stderr, _ := newDispatchApp("dev")

			if got := a.run(context.Background(), tc.args); got != exitFailure {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitFailure)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if got := stderr.String(); got != tc.want {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInternalHelpersRejectArguments(t *testing.T) {
	cases := []struct {
		args    []string
		install func(*testing.T, *bool)
		want    string
	}{
		{
			args: []string{"internal", "account-helper", "x"},
			install: func(t *testing.T, called *bool) {
				setAccountHelper(t, func(context.Context, io.Reader, io.Writer) error {
					*called = true
					return nil
				})
			},
			want: "agent-peer internal account-helper: unexpected argument \"x\"\n",
		},
		{
			args: []string{"internal", "access-check", "x"},
			install: func(t *testing.T, called *bool) {
				setAccessCheck(t, func(io.Reader, io.Writer) error {
					*called = true
					return nil
				})
			},
			want: "agent-peer internal access-check: unexpected argument \"x\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var called bool
			tc.install(t, &called)
			a, stdout, stderr, _ := newDispatchApp("dev")

			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitUsage)
			}
			if called {
				t.Fatalf("run(%q) reached the helper, want it rejected before dispatch", tc.args)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if got := stderr.String(); got != tc.want {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
			if strings.Contains(stderr.String(), "usage: agent-peer") {
				t.Fatalf("stderr = %q, want the hidden command to print no usage", stderr.String())
			}
		})
	}
}
