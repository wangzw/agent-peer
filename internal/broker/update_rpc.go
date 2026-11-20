package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/broker/update"
	"github.com/wangzw/agent-peer/internal/config"
)

// updateShutdownGrace is how long the Broker waits after answering ApplyUpdate
// before it starts the normal shutdown. gRPC writes the unary response right
// after the handler returns, so the grace covers a client that reads slowly and
// any scheduling delay: the caller learns that restarting is under way before
// the process exits and the local socket closes. It is well below the restart
// delay of the installed units (RestartSec=5 in systemd, launchd's KeepAlive),
// so the service manager starts the new binary promptly.
const updateShutdownGrace = 500 * time.Millisecond

// ApplyUpdate installs the newest formal release over the running binary and
// arranges the restart that makes it take effect. The request names neither a
// version nor a source. Access is enforced by the interceptor, not here: the
// system Broker requires write permission on the configuration file, and the
// user-level Broker allows every local caller, as it does for SetConfig.
//
// The Broker refuses when no service manager runs it, because nothing would
// start the new binary after it exits. Only one update may run at a time.
func (b *Broker) ApplyUpdate(ctx context.Context, _ *localpb.ApplyUpdateRequest) (*localpb.ApplyUpdateResponse, error) {
	caller, ok := CallerFrom(ctx)
	if !ok {
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "caller identity is missing"))
	}

	serviceManager := b.deps.ServiceManager
	if serviceManager == nil {
		serviceManager = detectServiceManager
	}
	if !serviceManager() || b.deps.RequestShutdown == nil {
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_NOT_UNDER_SERVICE_MANAGER,
			"the Broker is not running under a service manager, so it cannot start the new binary again; install it as a service and run the update against that service (for example, \"agent-peer service install\" or \"sudo agent-peer service install --system\")"))
	}

	if !b.updateBusy.CompareAndSwap(false, true) {
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_UPDATE_IN_PROGRESS,
			"another update is already in progress"))
	}
	// A failure releases the slot so the caller may retry. A success keeps it:
	// the process is about to exit and must not start a second download.
	installed := false
	defer func() {
		if !installed {
			b.updateBusy.Store(false)
		}
	}()

	current := b.deps.Version
	latest := b.deps.LatestRelease
	if latest == nil {
		latest = update.Latest
	}
	lookupCtx, cancel := context.WithTimeout(ctx, updateCheckRequestTimeout)
	defer cancel()
	release, err := latest(lookupCtx, b.deps.UpdateHTTPClient, b.deps.UpdateAPI)
	if err != nil {
		b.auditUpdateFailed(caller.Username, "looking up the latest release: "+err.Error())
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL,
			"looking up the latest release failed: %v", err))
	}
	if !update.Newer(current, release.Version) {
		return &localpb.ApplyUpdateResponse{FromVersion: current}, nil
	}

	target, err := installedExecutable()
	if err != nil {
		b.auditUpdateFailed(caller.Username, err.Error())
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "%v", err))
	}
	apply := b.deps.ApplyRelease
	if apply == nil {
		apply = update.Apply
	}
	if err := apply(ctx, update.ApplyOptions{
		Target:  target,
		Version: release.Version,
		API:     b.deps.UpdateAPI,
		Client:  b.deps.UpdateHTTPClient,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}); err != nil {
		b.auditUpdateFailed(caller.Username, "installing "+release.Version+": "+err.Error())
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL,
			"installing %s failed: %v", release.Version, err))
	}

	b.deps.Config.Audit(updateAppliedEvent(caller.Username, current, release.Version))
	installed = true
	// The response is written by gRPC once this handler returns. The grace
	// timer runs on the injected clock, so a test advances it instead of
	// waiting.
	b.deps.Clock.AfterFunc(updateShutdownGrace, b.deps.RequestShutdown)
	return &localpb.ApplyUpdateResponse{
		FromVersion: current,
		ToVersion:   release.Version,
		Restarting:  true,
	}, nil
}

// installedExecutable returns the running binary with symbolic links resolved,
// so the replacement lands on the file the service runs.
func installedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the executable: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving the executable %q: %w", exe, err)
	}
	return real, nil
}

// detectServiceManager reports whether the process runs under the service
// manager of this platform. systemd sets INVOCATION_ID for both user and system
// units; launchd sets XPC_SERVICE_NAME for agents and daemons.
func detectServiceManager() bool {
	return runningUnderServiceManager(runtime.GOOS, os.Getenv)
}

// runningUnderServiceManager implements detectServiceManager with the platform
// and environment lookup injected for tests.
func runningUnderServiceManager(goos string, getenv func(string) string) bool {
	switch goos {
	case "linux":
		return getenv("INVOCATION_ID") != ""
	case "darwin":
		return getenv("XPC_SERVICE_NAME") != ""
	default:
		return false
	}
}

// updateAppliedEvent is the audit record for an installed release. It carries
// the versions only, never a URL or any other content of the download.
func updateAppliedEvent(account, oldVersion, newVersion string) config.Event {
	oldValue := config.Value{Kind: config.KindString, String: oldVersion}
	newValue := config.Value{Kind: config.KindString, String: newVersion}
	return config.Event{
		Event:   config.EventUpdateApplied,
		Account: account,
		Old:     &oldValue,
		New:     &newValue,
	}
}

// auditUpdateFailed records a failed update attempt. detail is the underlying
// error text, which may name a URL; the download itself never enters the log.
func (b *Broker) auditUpdateFailed(account, detail string) {
	b.deps.Config.Audit(config.Event{
		Event:   config.EventUpdateFailed,
		Account: account,
		Detail:  detail,
	})
}
