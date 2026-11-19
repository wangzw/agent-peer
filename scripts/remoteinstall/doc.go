// Package remoteinstall holds the tests that drive scripts/remote-install.sh
// with fake command implementations. The fakes replace curl, wget, uname and
// the checksum tools through PATH, and serve a small release tree from the
// local file system, so every download path can run without network access.
package remoteinstall
