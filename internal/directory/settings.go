package directory

import (
	"errors"
	"time"

	"github.com/wangzw/peers/internal/config"
)

// configSettings is the production Settings implementation. Every method reads
// the current value, so a configuration change is visible on the next call.
// The three read seams may be replaced in tests; nil means the corresponding
// config package function.
type configSettings struct {
	get        func(string) (config.Value, error)
	advertised func() []config.AdvertisedAddr
	paths      func() config.PathSet
}

var _ Settings = configSettings{}

// value reads one key through the get seam, falling back to config.Get.
func (s configSettings) value(key string) (config.Value, error) {
	if s.get != nil {
		return s.get(key)
	}
	return config.Get(key)
}

// Public returns broker.public. It returns false when config is not loaded.
func (s configSettings) Public() bool {
	v, err := s.value("broker.public")
	if err != nil {
		return false
	}
	return v.Bool
}

// Alias returns broker.alias. It returns an empty string when config is not
// loaded.
func (s configSettings) Alias() string {
	v, err := s.value("broker.alias")
	if err != nil {
		return ""
	}
	return v.String
}

// Advertised returns a copy of the current advertised address list.
func (s configSettings) Advertised() []config.AdvertisedAddr {
	src := s.advertised
	if src == nil {
		src = config.Advertised
	}
	addrs := src()
	if addrs == nil {
		return nil
	}
	return append([]config.AdvertisedAddr(nil), addrs...)
}

// DataDir returns the directory that holds directory.json. It returns an
// error when config has no data directory, which is the case before Load.
func (s configSettings) DataDir() (string, error) {
	paths := s.paths
	if paths == nil {
		paths = config.Paths
	}
	dir := paths().DataDir
	if dir == "" {
		return "", errors.New("directory: config has no data directory")
	}
	return dir, nil
}

// ReconcileInterval returns directory.reconcile_interval, or ten minutes when
// config is not loaded.
func (s configSettings) ReconcileInterval() time.Duration {
	v, err := s.value("directory.reconcile_interval")
	if err != nil {
		return 10 * time.Minute
	}
	return v.Duration
}

// MaxRecords returns directory.max_records, or 1000 when config is not loaded.
func (s configSettings) MaxRecords() int {
	v, err := s.value("directory.max_records")
	if err != nil {
		return 1000
	}
	return int(v.Int)
}

// MaxRecordsPerLink returns directory.max_records_per_link, or 250 when config
// is not loaded.
func (s configSettings) MaxRecordsPerLink() int {
	v, err := s.value("directory.max_records_per_link")
	if err != nil {
		return 250
	}
	return int(v.Int)
}

// MaxTopicsPerPublisher returns directory.max_topics_per_publisher, or 16 when
// config is not loaded.
func (s configSettings) MaxTopicsPerPublisher() int {
	v, err := s.value("directory.max_topics_per_publisher")
	if err != nil {
		return 16
	}
	return int(v.Int)
}
