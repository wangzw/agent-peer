package harness

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FastTimers shortens the link and send timers for scenarios that do not need
// the production defaults. The values are the ones decided in the end-to-end
// plan: the reconnect backoff and the dial timeout shrink, the send budget
// shrinks to 2s, and the advertised-address check runs every 500ms.
const FastTimers = `link:
  reconnect_min: 100ms
  reconnect_max: 400ms
  dial_timeout: 500ms
send:
  link_backoff: 100ms
  request_timeout: 2s
broker:
  address_check_interval: 500ms
`

// ConfigYAML merges YAML fragments by key, with later fragments overriding
// earlier leaves, and returns the result in a deterministic key order. Empty
// fragments are ignored.
func ConfigYAML(parts ...string) string {
	merged := map[string]any{}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		var fragment map[string]any
		if err := yaml.Unmarshal([]byte(part), &fragment); err != nil {
			panic(fmt.Sprintf("harness.ConfigYAML: invalid YAML fragment: %v", err))
		}
		mergeConfig(merged, fragment)
	}
	if len(merged) == 0 {
		return ""
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(merged); err != nil {
		panic(fmt.Sprintf("harness.ConfigYAML: encoding merged configuration: %v", err))
	}
	if err := enc.Close(); err != nil {
		panic(fmt.Sprintf("harness.ConfigYAML: encoding merged configuration: %v", err))
	}
	return buf.String()
}

// mergeConfig recursively merges src into dst. Maps merge key by key; any
// other value replaces the value already present.
func mergeConfig(dst, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				mergeConfig(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
}
