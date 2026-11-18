package broker

import "testing"

// TestPackageConfigSatisfiesConfigSource checks that the production
// ConfigSource matches the config package's exported signatures at compile
// time. It deliberately calls no config function: the config package is a
// process-wide singleton whose directories cannot be redirected in tests.
func TestPackageConfigSatisfiesConfigSource(t *testing.T) {
	var _ ConfigSource = PackageConfig()
}
