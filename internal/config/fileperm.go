package config

import "io/fs"

// rootOnlyWritableFor decides from a file's owner and permission bits whether
// only root can write it. The caller supplies the owner so the rule can be
// tested without a root-owned file, which a non-root test process cannot
// create. Group and other write bits are refused. This is the shared part of
// the rule; the Darwin caller also refuses any security ACL, which can widen
// write access past the mode bits.
func rootOnlyWritableFor(uid uint32, mode fs.FileMode) bool {
	return uid == 0 && mode.Perm()&0o022 == 0
}
