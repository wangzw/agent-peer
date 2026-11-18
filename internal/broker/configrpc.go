package broker

import (
	"context"
	"errors"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/config"
)

// GetConfig returns configuration values. An empty key returns every key in
// the dictionary order All reports; a non-empty key returns exactly that one
// entry. Secret values are reported as set or unset only, never in clear.
// Access is enforced by the interceptor, not here.
func (b *Broker) GetConfig(ctx context.Context, req *localpb.GetConfigRequest) (*localpb.GetConfigResponse, error) {
	cfg := b.deps.Config
	key := req.GetKey()
	if key == "" {
		entries := cfg.All()
		out := make([]*localpb.ConfigEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, &localpb.ConfigEntry{Key: e.Key, Value: toConfigValue(e.Value)})
		}
		return &localpb.GetConfigResponse{Entries: out}, nil
	}

	v, err := cfg.Get(key)
	if err != nil {
		if errors.Is(err, config.ErrUnknownKey) {
			return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
				"unknown configuration key %q", key))
		}
		return nil, toStatus(Errorf(configErrorCode(err), "%s", err.Error()))
	}
	return &localpb.GetConfigResponse{Entries: []*localpb.ConfigEntry{
		{Key: key, Value: toConfigValue(v)},
	}}, nil
}
