package broker

import (
	"errors"
	"strings"

	"github.com/wangzw/peers/internal/config"
)

// dataKind names the data-directory file whose failure put the Broker into
// diagnostics-only. It selects the repair hint in DegradedState.fix.
type dataKind int

const (
	dataKindOther dataKind = iota
	dataKindDataDir
	dataKindIdentity
	dataKindPeers
	dataKindDirectory
	dataKindAudit
)

// dataUnavailableError tags an assembly or component-start failure caused by a
// file under the data directory. Its text is the original error, so startup
// messages stay unchanged, and Unwrap matches both config.ErrDataUnavailable
// and the concrete cause.
type dataUnavailableError struct {
	kind dataKind
	err  error
}

func (e *dataUnavailableError) Error() string { return e.err.Error() }

func (e *dataUnavailableError) Unwrap() []error {
	return []error{config.ErrDataUnavailable, e.err}
}

func wrapDataUnavailable(kind dataKind, err error) error {
	if err == nil {
		return nil
	}
	return &dataUnavailableError{kind: kind, err: err}
}

// classifyDataUnavailable reports whether err belongs to the data-directory
// class and, when it does, which file failed and the paths a Load failure
// carried. The paths are empty for errors wrapped by the broker.
func classifyDataUnavailable(err error) (dataKind, config.PathSet, bool) {
	var de *config.DataUnavailableError
	if errors.As(err, &de) {
		kind := dataKindDataDir
		if de.Paths.AuditLog != "" && strings.Contains(err.Error(), de.Paths.AuditLog) {
			kind = dataKindAudit
		}
		return kind, de.Paths, true
	}
	var be *dataUnavailableError
	if errors.As(err, &be) {
		return be.kind, config.PathSet{}, true
	}
	if errors.Is(err, config.ErrDataUnavailable) {
		return dataKindOther, config.PathSet{}, true
	}
	return dataKindOther, config.PathSet{}, false
}
