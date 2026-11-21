package broker

import (
	"context"
	"errors"

	"github.com/wangzw/peers/internal/broker/localpb"
	"github.com/wangzw/peers/internal/config"
	"github.com/wangzw/peers/internal/pairing"
)

// connectError maps the error a pairing attempt returns to the local
// interface error code and message. It returns nil for context.Canceled, which
// toStatus reports as Canceled; callers must pass the original error to
// toStatus in that case.
func connectError(address, localAlias string, err error) *Error {
	switch {
	case errors.Is(err, pairing.ErrInvalidAddress):
		return Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"invalid address %q: write it as address:port or hostname(address:port); an IPv6 address needs brackets", address)
	case errors.Is(err, pairing.ErrInvalidAlias):
		// pairing reports every unusable local alias with one sentinel. A
		// character outside the set is named first, so a multi-byte alias is
		// not described as merely too long; otherwise an alias over the limit
		// says so.
		if !aliasCharsValid(localAlias) {
			return Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
				"invalid local alias: use 1 to %d letters, digits, '.', '-' or '_'", config.AliasMaxLen)
		}
		if len(localAlias) > config.AliasMaxLen {
			return Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
				"invalid local alias: longer than %d characters; use 1 to %d letters, digits, '.', '-' or '_'",
				config.AliasMaxLen, config.AliasMaxLen)
		}
		return Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"invalid local alias: use 1 to %d letters, digits, '.', '-' or '_'", config.AliasMaxLen)
	case errors.Is(err, pairing.ErrNeedsLocalAlias):
		return Errorf(localpb.ErrorCode_ERROR_CODE_NEEDS_LOCAL_ALIAS,
			"the other Broker's alias is already this Broker's alias or a peer's alias; retry with local_alias set to a new name for it")
	}

	// A *PairingError unwraps to ErrPairingRejected, so it has to be checked
	// before the generic rejected branch. Its message starts with
	// "Not connected: ", which mcpserver adds itself, so only Detail is used.
	var pe *pairing.PairingError
	if errors.As(err, &pe) {
		detail := pe.Detail
		if detail == "" {
			detail = "pairing was refused"
		}
		return Errorf(localpb.ErrorCode_ERROR_CODE_PAIRING_REJECTED, "%s", detail)
	}

	switch {
	case errors.Is(err, pairing.ErrPairingRejected):
		return Errorf(localpb.ErrorCode_ERROR_CODE_PAIRING_REJECTED, "%s", err.Error())
	case errors.Is(err, pairing.ErrProtocol):
		return Errorf(localpb.ErrorCode_ERROR_CODE_PAIRING_REJECTED,
			"the other side broke the pairing protocol: %v", err)
	case errors.Is(err, pairing.ErrPeerUnreachable):
		return Errorf(localpb.ErrorCode_ERROR_CODE_PEER_UNREACHABLE,
			"cannot reach %s; check the address and that the other Broker listens there", address)
	case errors.Is(err, context.Canceled):
		return nil
	default:
		return Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "pairing failed inside this Broker: %v", err)
	}
}

// pairingKeyError maps the error from creating a one-time pairing key.
func pairingKeyError(err error) *Error {
	if errors.Is(err, pairing.ErrNotListening) {
		return Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"this Broker does not listen (broker.listen is empty), so the other Broker cannot dial it; ask the other side to create a pairing key and pair from here with connect_broker")
	}
	return Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "creating a pairing key failed: %v", err)
}

// unpairError maps the error from Unpair. Unpair fails only when pairing is
// not open or the peer table cannot be written.
func unpairError(err error) *Error {
	return Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "unpairing failed: %v", err)
}
