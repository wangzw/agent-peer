package main

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/wangzw/peers/internal/broker/update"
)

// stubStatusCall records one call to runStatus.
type stubStatusCall struct {
	stdout io.Writer
}

// stubAuditCall records one call to runAudit.
type stubAuditCall struct {
	stdout io.Writer
	limit  uint32
}

// stubDeps is the in-package test double for deps. A test configures results
// either with the override function fields (used when a test needs to observe
// the context or block) or with the plain result fields. Every call is
// recorded before the override runs, so a test can assert both that a
// subcommand reached its dependency and with which arguments.
type stubDeps struct {
	// Overrides. When nil, the stub records the call and returns the matching
	// configured result below.
	runBroker         func(ctx context.Context, o brokerOptions) error
	classifyBrokerErr func(err error) startFailure
	runMCP            func(ctx context.Context) error
	runStatus         func(ctx context.Context, stdout io.Writer) error
	runAudit          func(ctx context.Context, stdout io.Writer, limit uint32) error

	// Update command overrides. When nil, the stub records the call and uses
	// the configured results below.
	updateAPIBase        func() string
	updateLatest         func(ctx context.Context, client *http.Client, api string) (update.Release, error)
	updateNewer          func(current, candidate string) bool
	updateApply          func(ctx context.Context, o update.ApplyOptions) error
	applyUpdateViaBroker func(ctx context.Context) (brokerUpdateResult, error)
	brokerUpdateStatus   func(ctx context.Context) (brokerUpdateStatus, error)
	restartService       func(ctx context.Context) error

	// Configured results used when the corresponding override is nil.
	brokerErr      error
	classifyResult startFailure
	mcpErr         error
	statusErr      error
	auditErr       error

	latestRelease   update.Release
	latestErr       error
	newerResult     bool
	applyErr        error
	brokerUpdate    brokerUpdateResult
	brokerUpdateErr error
	brokerStatus    brokerUpdateStatus
	brokerStatusErr error

	// Recorded calls.
	brokerCalls       []brokerOptions
	mcpCalls          int
	statusCalls       []stubStatusCall
	auditCalls        []stubAuditCall
	latestCalls       int
	latestAPI         string
	latestClient      *http.Client
	newerCalls        [][2]string
	applyCalls        []update.ApplyOptions
	brokerApplyCalls  int
	brokerStatusCalls int
	// restartServiceCalls counts service operations reached through the seam.
	restartServiceCalls int
}

// deps returns a deps whose function fields record into s and then delegate to
// the configured override or result.
func (s *stubDeps) deps() deps {
	return deps{
		runBroker: func(ctx context.Context, o brokerOptions) error {
			s.brokerCalls = append(s.brokerCalls, o)
			if s.runBroker != nil {
				return s.runBroker(ctx, o)
			}
			return s.brokerErr
		},
		classifyBrokerErr: func(err error) startFailure {
			if s.classifyBrokerErr != nil {
				return s.classifyBrokerErr(err)
			}
			return s.classifyResult
		},
		runMCP: func(ctx context.Context) error {
			s.mcpCalls++
			if s.runMCP != nil {
				return s.runMCP(ctx)
			}
			return s.mcpErr
		},
		runStatus: func(ctx context.Context, stdout io.Writer) error {
			s.statusCalls = append(s.statusCalls, stubStatusCall{stdout: stdout})
			if s.runStatus != nil {
				return s.runStatus(ctx, stdout)
			}
			return s.statusErr
		},
		runAudit: func(ctx context.Context, stdout io.Writer, limit uint32) error {
			s.auditCalls = append(s.auditCalls, stubAuditCall{stdout: stdout, limit: limit})
			if s.runAudit != nil {
				return s.runAudit(ctx, stdout, limit)
			}
			return s.auditErr
		},
		peerNames: func(context.Context) ([]string, error) { return nil, nil },
		updateAPIBase: func() string {
			if s.updateAPIBase != nil {
				return s.updateAPIBase()
			}
			return "https://api.example"
		},
		updateLatest: func(ctx context.Context, client *http.Client, api string) (update.Release, error) {
			s.latestCalls++
			s.latestAPI = api
			s.latestClient = client
			if s.updateLatest != nil {
				return s.updateLatest(ctx, client, api)
			}
			return s.latestRelease, s.latestErr
		},
		updateNewer: func(current, candidate string) bool {
			s.newerCalls = append(s.newerCalls, [2]string{current, candidate})
			if s.updateNewer != nil {
				return s.updateNewer(current, candidate)
			}
			return s.newerResult
		},
		updateApply: func(ctx context.Context, o update.ApplyOptions) error {
			s.applyCalls = append(s.applyCalls, o)
			if s.updateApply != nil {
				return s.updateApply(ctx, o)
			}
			return s.applyErr
		},
		applyUpdateViaBroker: func(ctx context.Context) (brokerUpdateResult, error) {
			s.brokerApplyCalls++
			if s.applyUpdateViaBroker != nil {
				return s.applyUpdateViaBroker(ctx)
			}
			return s.brokerUpdate, s.brokerUpdateErr
		},
		brokerUpdateStatus: func(ctx context.Context) (brokerUpdateStatus, error) {
			s.brokerStatusCalls++
			if s.brokerUpdateStatus != nil {
				return s.brokerUpdateStatus(ctx)
			}
			return s.brokerStatus, s.brokerStatusErr
		},
		restartService: func(ctx context.Context) error {
			s.restartServiceCalls++
			if s.restartService != nil {
				return s.restartService(ctx)
			}
			return nil
		},
	}
}

func TestExitCodeForIsDistinctPerStartupFailure(t *testing.T) {
	failures := []startFailure{
		failAlreadyRunning,
		failListenUnavailable,
		failInvalidConfig,
		failInSession,
		failNotRoot,
		failRuntimeDir,
	}
	seen := make(map[int]startFailure, len(failures))
	for _, f := range failures {
		code := exitCodeFor(f)
		if code == 0 || code == 1 || code == 2 {
			t.Errorf("exitCodeFor(%v) = %d, want a dedicated startup failure code", f, code)
		}
		if prev, ok := seen[code]; ok {
			t.Errorf("exitCodeFor(%v) and exitCodeFor(%v) both return %d", f, prev, code)
		}
		seen[code] = f
	}
}

func TestExitCodeForTable(t *testing.T) {
	tests := []struct {
		failure startFailure
		want    int
	}{
		{failOther, 1},
		{failAlreadyRunning, 3},
		{failListenUnavailable, 4},
		{failInvalidConfig, 5},
		{failInSession, 6},
		{failNotRoot, 7},
		{failRuntimeDir, 8},
		{failInvalidListen, 2},
		{startFailure(99), 1},
	}
	for _, tt := range tests {
		if got := exitCodeFor(tt.failure); got != tt.want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", tt.failure, got, tt.want)
		}
	}
}

// TestExitServiceDegraded pins the code the service commands return for a
// degraded Broker. It is distinct from the generic failure and usage codes so
// the install script can tell "installed but degraded" from both.
func TestExitServiceDegraded(t *testing.T) {
	if exitServiceDegraded != 3 {
		t.Fatalf("exitServiceDegraded = %d, want 3", exitServiceDegraded)
	}
	for _, other := range []struct {
		name string
		code int
	}{
		{"exitFailure", exitFailure},
		{"exitUsage", exitUsage},
	} {
		if exitServiceDegraded == other.code {
			t.Errorf("exitServiceDegraded shares %d with %s", other.code, other.name)
		}
	}
	if exitServiceDegraded != exitAlreadyRunning {
		t.Errorf("exitServiceDegraded = %d, want the documented shared value %d of exitAlreadyRunning", exitServiceDegraded, exitAlreadyRunning)
	}
	// Only failAlreadyRunning reaches the shared value; no other broker start
	// failure may report a degraded service command's code.
	for f := failOther; f <= failInvalidListen; f++ {
		if got := exitCodeFor(f); got == exitServiceDegraded && f != failAlreadyRunning {
			t.Errorf("exitCodeFor(%d) = %d, want only failAlreadyRunning to share %d", f, got, exitServiceDegraded)
		}
	}
}
