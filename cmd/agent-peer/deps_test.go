package main

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/wangzw/agent-peer/internal/broker/service"
	"github.com/wangzw/agent-peer/internal/broker/update"
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
	updateAPIBase  func() string
	updateLatest   func(ctx context.Context, client *http.Client, api string) (update.Release, error)
	updateNewer    func(current, candidate string) bool
	updateApply    func(ctx context.Context, o update.ApplyOptions) error
	serviceOptions func(system bool) (service.Options, error)
	serviceRestart func(ctx context.Context, opts service.Options) (bool, error)

	// Configured results used when the corresponding override is nil.
	brokerErr      error
	classifyResult startFailure
	mcpErr         error
	statusErr      error
	auditErr       error

	latestRelease    update.Release
	latestErr        error
	newerResult      bool
	applyErr         error
	optionsErr       error
	serviceRestarted bool
	restartErr       error

	// Recorded calls.
	brokerCalls        []brokerOptions
	mcpCalls           int
	statusCalls        []stubStatusCall
	auditCalls         []stubAuditCall
	latestCalls        int
	latestAPI          string
	latestClient       *http.Client
	newerCalls         [][2]string
	applyCalls         []update.ApplyOptions
	serviceOptionCalls []bool
	restartCalls       []service.Options
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
		serviceOptions: func(system bool) (service.Options, error) {
			s.serviceOptionCalls = append(s.serviceOptionCalls, system)
			if s.serviceOptions != nil {
				return s.serviceOptions(system)
			}
			return service.Options{System: system}, s.optionsErr
		},
		serviceRestart: func(ctx context.Context, opts service.Options) (bool, error) {
			s.restartCalls = append(s.restartCalls, opts)
			if s.serviceRestart != nil {
				return s.serviceRestart(ctx, opts)
			}
			return s.serviceRestarted, s.restartErr
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
		{failSystemUnavailable, 1},
		{startFailure(99), 1},
	}
	for _, tt := range tests {
		if got := exitCodeFor(tt.failure); got != tt.want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", tt.failure, got, tt.want)
		}
	}
}
