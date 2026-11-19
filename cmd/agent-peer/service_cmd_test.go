package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wangzw/agent-peer/internal/broker/service"
)

// setServiceSeams installs the option and operation seams for one test. A nil
// function leaves that seam unchanged.
func setServiceSeams(
	t *testing.T,
	options func(bool) (service.Options, error),
	install func(service.Options) (service.Result, error),
	uninstall func(service.Options) (service.Result, error),
) {
	t.Helper()
	previousOptions, previousInstall, previousUninstall := defaultOptionsFn, installFn, uninstallFn
	if options != nil {
		defaultOptionsFn = options
	}
	if install != nil {
		installFn = install
	}
	if uninstall != nil {
		uninstallFn = uninstall
	}
	t.Cleanup(func() {
		defaultOptionsFn = previousOptions
		installFn = previousInstall
		uninstallFn = previousUninstall
	})
}

func TestServiceInstallMapsSystemFlag(t *testing.T) {
	cases := []struct {
		args       []string
		wantSystem bool
	}{
		{[]string{"service", "install"}, false},
		{[]string{"service", "install", "--system"}, true},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var (
				optionsSystem bool
				optionsCalls  int
				installed     service.Options
				installCalls  int
			)
			setServiceSeams(t,
				func(system bool) (service.Options, error) {
					optionsCalls++
					optionsSystem = system
					return service.Options{System: system, Exe: "/usr/local/bin/agent-peer"}, nil
				},
				func(opts service.Options) (service.Result, error) {
					installCalls++
					installed = opts
					return service.Result{UnitPath: "/x/agent-peer.service"}, nil
				},
				nil,
			)

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), tc.args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitOK, stderr.String())
			}
			if optionsCalls != 1 || installCalls != 1 {
				t.Fatalf("option calls = %d, install calls = %d, want 1 each", optionsCalls, installCalls)
			}
			if optionsSystem != tc.wantSystem || installed.System != tc.wantSystem {
				t.Fatalf("system = %v (options) / %v (install), want %v", optionsSystem, installed.System, tc.wantSystem)
			}
			if want := "installed and started agent-peer.service\n"; stdout.String() != want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestServiceInstallPrintsTheLaunchdDomain(t *testing.T) {
	setServiceSeams(t,
		func(bool) (service.Options, error) { return service.Options{}, nil },
		func(service.Options) (service.Result, error) {
			return service.Result{Domain: "gui/501"}, nil
		},
		nil,
	)

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"service", "install"}); got != exitOK {
		t.Fatalf("run(service install) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "installed and started io.github.wangzw.agent-peer in gui/501\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestServiceUninstall(t *testing.T) {
	cases := []struct {
		name    string
		result  service.Result
		system  bool
		want    string
		wantArg bool
	}{
		{
			name:   "removed",
			result: service.Result{Removed: true},
			want:   "uninstalled agent-peer.service\n",
		},
		{
			name:   "removed from a launchd domain",
			result: service.Result{Removed: true, Domain: "user/501"},
			want:   "uninstalled io.github.wangzw.agent-peer from user/501\n",
		},
		{
			name:   "system",
			result: service.Result{Removed: true},
			system: true,
			want:   "uninstalled agent-peer.service\n",
		},
		{
			name:   "not installed",
			result: service.Result{},
			want:   "agent-peer service is not installed\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotSystem     bool
				uninstallCall int
			)
			setServiceSeams(t,
				func(system bool) (service.Options, error) {
					gotSystem = system
					return service.Options{System: system}, nil
				},
				nil,
				func(opts service.Options) (service.Result, error) {
					uninstallCall++
					return tc.result, nil
				},
			)

			args := []string{"service", "uninstall"}
			if tc.system {
				args = append(args, "--system")
			}
			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", args, got, exitOK, stderr.String())
			}
			if uninstallCall != 1 {
				t.Fatalf("uninstall calls = %d, want 1", uninstallCall)
			}
			if gotSystem != tc.system {
				t.Fatalf("system = %v, want %v", gotSystem, tc.system)
			}
			if stdout.String() != tc.want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tc.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestServiceReportsErrors(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		var installCalls int
		setServiceSeams(t,
			func(bool) (service.Options, error) { return service.Options{}, errors.New("no executable") },
			func(service.Options) (service.Result, error) {
				installCalls++
				return service.Result{}, nil
			},
			nil,
		)

		a, stdout, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "install"}); got != exitFailure {
			t.Fatalf("run(service install) = %d, want %d", got, exitFailure)
		}
		if installCalls != 0 {
			t.Fatalf("install calls = %d, want 0", installCalls)
		}
		if want := "agent-peer service install: no executable\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("install", func(t *testing.T) {
		setServiceSeams(t,
			func(bool) (service.Options, error) { return service.Options{}, nil },
			func(service.Options) (service.Result, error) {
				return service.Result{}, errors.New("systemctl failed")
			},
			nil,
		)

		a, stdout, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "install", "--system"}); got != exitFailure {
			t.Fatalf("run(service install --system) = %d, want %d", got, exitFailure)
		}
		if want := "agent-peer service install: systemctl failed\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want no success line after a failure", stdout.String())
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		setServiceSeams(t,
			func(bool) (service.Options, error) { return service.Options{}, nil },
			nil,
			func(service.Options) (service.Result, error) {
				return service.Result{}, errors.New("systemctl failed")
			},
		)

		a, stdout, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "uninstall"}); got != exitFailure {
			t.Fatalf("run(service uninstall) = %d, want %d", got, exitFailure)
		}
		if want := "agent-peer service uninstall: systemctl failed\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})
}

func TestServiceUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"service"}, "agent-peer service: missing command\n"},
		{[]string{"service", "bogus"}, "agent-peer service: unknown command \"bogus\"\n"},
		{[]string{"service", "install", "extra"}, "agent-peer service install: unexpected argument \"extra\"\n"},
		{[]string{"service", "install", "--bogus"}, "agent-peer service install: flag provided but not defined: -bogus\n"},
		{[]string{"service", "uninstall", "extra"}, "agent-peer service uninstall: unexpected argument \"extra\"\n"},
		{[]string{"service", "uninstall", "--bogus"}, "agent-peer service uninstall: flag provided but not defined: -bogus\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var calls int
			setServiceSeams(t,
				func(bool) (service.Options, error) {
					t.Fatal("default options ran for a usage error")
					return service.Options{}, nil
				},
				func(service.Options) (service.Result, error) {
					calls++
					return service.Result{}, nil
				},
				func(service.Options) (service.Result, error) {
					calls++
					return service.Result{}, nil
				},
			)

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitUsage, stderr.String())
			}
			if calls != 0 {
				t.Fatalf("operation calls = %d, want 0", calls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), serviceUsage) {
				t.Fatalf("stderr = %q, want the service usage first", stderr.String())
			}
			if got := brokerLastLine(stderr.String()); got != strings.TrimSuffix(tc.want, "\n") {
				t.Fatalf("last stderr line = %q, want %q", got, strings.TrimSuffix(tc.want, "\n"))
			}
		})
	}
}

func TestServiceHelpPrintsUsage(t *testing.T) {
	cases := [][]string{
		{"service", "--help"},
		{"service", "install", "--help"},
		{"service", "uninstall", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			setServiceSeams(t,
				func(bool) (service.Options, error) {
					t.Fatal("default options ran for --help")
					return service.Options{}, nil
				},
				func(service.Options) (service.Result, error) {
					t.Fatal("install ran for --help")
					return service.Result{}, nil
				},
				func(service.Options) (service.Result, error) {
					t.Fatal("uninstall ran for --help")
					return service.Result{}, nil
				},
			)

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d", args, got, exitOK)
			}
			if stdout.String() != serviceUsage {
				t.Fatalf("stdout = %q, want %q", stdout.String(), serviceUsage)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}
