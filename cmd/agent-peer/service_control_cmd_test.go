package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/broker/service"
)

// controlSeams replaces the start, stop and restart operations, the level
// check and the account lookup for one test. A nil field leaves that seam
// unchanged.
type controlSeams struct {
	options   func(bool) (service.Options, error)
	start     func(service.Options) (service.Result, error)
	stop      func(service.Options) (service.Result, error)
	restart   func(service.Options) (service.Result, error)
	installed func(service.Options) (bool, error)
	euid      func() int
}

func setControlSeams(t *testing.T, s controlSeams) {
	t.Helper()
	previousOptions, previousStart, previousStop := defaultOptionsFn, startFn, stopFn
	previousRestart, previousInstalled, previousEUID := restartFn, installedFn, serviceEUID
	if s.options != nil {
		defaultOptionsFn = s.options
	}
	if s.start != nil {
		startFn = s.start
	}
	if s.stop != nil {
		stopFn = s.stop
	}
	if s.restart != nil {
		restartFn = s.restart
	}
	if s.installed != nil {
		installedFn = s.installed
	}
	if s.euid != nil {
		serviceEUID = s.euid
	}
	t.Cleanup(func() {
		defaultOptionsFn, startFn, stopFn = previousOptions, previousStart, previousStop
		restartFn, installedFn, serviceEUID = previousRestart, previousInstalled, previousEUID
	})
}

func TestServiceControlMapsVerbAndLevel(t *testing.T) {
	cases := []struct {
		command  string
		euid     int
		wantPast string
		wantSys  bool
	}{
		{"start", 1000, "started", false},
		{"stop", 1000, "stopped", false},
		{"restart", 1000, "restarted", false},
		{"start", 0, "started", true},
		{"stop", 0, "stopped", true},
		{"restart", 0, "restarted", true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s euid=%d", tc.command, tc.euid), func(t *testing.T) {
			var (
				optionsSystem bool
				optionsCalls  int
				calls         []string
			)
			operation := func(name string) func(service.Options) (service.Result, error) {
				return func(opts service.Options) (service.Result, error) {
					calls = append(calls, name)
					if opts.System != tc.wantSys {
						t.Errorf("%s got System=%v, want %v", name, opts.System, tc.wantSys)
					}
					return service.Result{}, nil
				}
			}
			setControlSeams(t, controlSeams{
				options: func(system bool) (service.Options, error) {
					optionsCalls++
					optionsSystem = system
					return service.Options{System: system}, nil
				},
				start:   operation("start"),
				stop:    operation("stop"),
				restart: operation("restart"),
				euid:    func() int { return tc.euid },
			})

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), []string{"service", tc.command}); got != exitOK {
				t.Fatalf("run(service %s) = %d, want %d (stderr %q)", tc.command, got, exitOK, stderr.String())
			}
			if optionsCalls != 1 {
				t.Fatalf("options calls = %d, want 1", optionsCalls)
			}
			if optionsSystem != tc.wantSys {
				t.Errorf("options system = %v, want %v", optionsSystem, tc.wantSys)
			}
			if len(calls) != 1 || calls[0] != tc.command {
				t.Errorf("operations called = %v, want [%s]", calls, tc.command)
			}
			level := "user-level"
			if tc.wantSys {
				level = "system-level"
			}
			if want := fmt.Sprintf("Broker service %s (%s)\n", tc.wantPast, level); stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestServiceControlRejectsArguments(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"service", "start", "--system"}, "--system is not accepted"},
		{[]string{"service", "stop", "--system=false"}, "--system is not accepted"},
		{[]string{"service", "restart", "extra"}, `unexpected argument "extra"`},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			setControlSeams(t, controlSeams{
				options: func(bool) (service.Options, error) {
					t.Fatal("default options ran for a usage error")
					return service.Options{}, nil
				},
				start: func(service.Options) (service.Result, error) {
					t.Fatal("start ran for a usage error")
					return service.Result{}, nil
				},
				stop: func(service.Options) (service.Result, error) {
					t.Fatal("stop ran for a usage error")
					return service.Result{}, nil
				},
				restart: func(service.Options) (service.Result, error) {
					t.Fatal("restart ran for a usage error")
					return service.Result{}, nil
				},
			})

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitUsage)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), serviceUsage) {
				t.Errorf("stderr = %q, want the service usage first", stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestServiceControlHelp(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart"} {
		t.Run(command, func(t *testing.T) {
			setControlSeams(t, controlSeams{
				options: func(bool) (service.Options, error) {
					t.Fatal("default options ran for --help")
					return service.Options{}, nil
				},
			})
			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), []string{"service", command, "--help"}); got != exitOK {
				t.Fatalf("run(service %s --help) = %d, want %d", command, got, exitOK)
			}
			if stdout.String() != serviceUsage {
				t.Errorf("stdout = %q, want %q", stdout.String(), serviceUsage)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestServiceControlNotInstalled(t *testing.T) {
	notInstalled := fmt.Errorf("%w (user-level)", service.ErrNotInstalled)

	t.Run("user-level only", func(t *testing.T) {
		var systems []bool
		setControlSeams(t, controlSeams{
			options: func(system bool) (service.Options, error) {
				systems = append(systems, system)
				return service.Options{System: system}, nil
			},
			start: func(service.Options) (service.Result, error) { return service.Result{}, notInstalled },
			installed: func(service.Options) (bool, error) {
				return false, nil
			},
			euid: func() int { return 1000 },
		})

		a, stdout, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "start"}); got != exitFailure {
			t.Fatalf("run(service start) = %d, want %d", got, exitFailure)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want empty", stdout.String())
		}
		want := "agent-peer service start: no user-level service is installed; install it with `agent-peer service install`\n"
		if stderr.String() != want {
			t.Errorf("stderr = %q, want %q", stderr.String(), want)
		}
		if len(systems) != 2 || systems[0] != false || systems[1] != true {
			t.Errorf("options systems = %v, want [false true]", systems)
		}
	})

	t.Run("only the system-level service is installed", func(t *testing.T) {
		setControlSeams(t, controlSeams{
			options: func(system bool) (service.Options, error) {
				return service.Options{System: system}, nil
			},
			start: func(service.Options) (service.Result, error) { return service.Result{}, notInstalled },
			installed: func(opts service.Options) (bool, error) {
				return opts.System, nil
			},
			euid: func() int { return 1000 },
		})

		a, _, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "start"}); got != exitFailure {
			t.Fatalf("run(service start) = %d, want %d", got, exitFailure)
		}
		for _, want := range []string{"no user-level service is installed", "sudo agent-peer service start"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
		}
	})

	t.Run("root at the system level", func(t *testing.T) {
		var systems []bool
		setControlSeams(t, controlSeams{
			options: func(system bool) (service.Options, error) {
				systems = append(systems, system)
				return service.Options{System: system}, nil
			},
			start: func(service.Options) (service.Result, error) {
				return service.Result{}, fmt.Errorf("%w (system-level)", service.ErrNotInstalled)
			},
			installed: func(service.Options) (bool, error) {
				t.Fatal("the system-level installed check ran for root")
				return false, nil
			},
			euid: func() int { return 0 },
		})

		a, _, stderr, _ := newDispatchApp("dev")
		if got := a.run(context.Background(), []string{"service", "start"}); got != exitFailure {
			t.Fatalf("run(service start) = %d, want %d", got, exitFailure)
		}
		if len(systems) != 1 || systems[0] != true {
			t.Errorf("options systems = %v, want [true]", systems)
		}
		for _, want := range []string{"no system-level service is installed", "sudo agent-peer service install --system"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
		}
	})
}

func TestServiceControlDegraded(t *testing.T) {
	cases := []struct {
		name  string
		level service.DegradedLevel
		want  string
	}{
		{"local_only", service.DegradedLocalOnly, "local only"},
		{"diagnostics_only", service.DegradedDiagnosticsOnly, "diagnostics only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := service.DegradedState{
				Level:  tc.level,
				Step:   "listen",
				Reason: "listen :17443: address already in use",
				Fix:    "change broker.listen",
			}
			setControlSeams(t, controlSeams{
				options: func(bool) (service.Options, error) { return service.Options{}, nil },
				start: func(service.Options) (service.Result, error) {
					return service.Result{Domain: "gui/501"}, &service.DegradedError{State: state}
				},
				euid: func() int { return 1000 },
			})

			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), []string{"service", "start"}); got != exitServiceDegraded {
				t.Fatalf("run(service start) = %d, want %d", got, exitServiceDegraded)
			}
			if want := "Broker service started (user-level)\n"; stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			want := fmt.Sprintf("Broker: degraded (%s): %s\nFix: %s\n", tc.want, state.Reason, state.Fix)
			if stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestServiceControlFailure(t *testing.T) {
	setControlSeams(t, controlSeams{
		options: func(bool) (service.Options, error) { return service.Options{}, nil },
		stop: func(service.Options) (service.Result, error) {
			return service.Result{}, errors.New("systemctl failed")
		},
		euid: func() int { return 1000 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"service", "stop"}); got != exitFailure {
		t.Fatalf("run(service stop) = %d, want %d", got, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty after a failure", stdout.String())
	}
	if want := "agent-peer service stop: systemctl failed\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestServiceControlDefaultOptionsError(t *testing.T) {
	var calls int
	setControlSeams(t, controlSeams{
		options: func(bool) (service.Options, error) { return service.Options{}, errors.New("no executable") },
		restart: func(service.Options) (service.Result, error) {
			calls++
			return service.Result{}, nil
		},
		euid: func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"service", "restart"}); got != exitFailure {
		t.Fatalf("run(service restart) = %d, want %d", got, exitFailure)
	}
	if calls != 0 {
		t.Errorf("restart calls = %d, want 0", calls)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if want := "agent-peer service restart: no executable\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}
