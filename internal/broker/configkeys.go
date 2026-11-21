package broker

import "github.com/wangzw/peers/internal/config"

// ConfigKeys returns every configuration key in lexicographic order. The
// command line uses it to complete the key argument of `config get` and
// `config set`; the key table itself stays in the config package.
func ConfigKeys() []string { return config.KeyNames() }
