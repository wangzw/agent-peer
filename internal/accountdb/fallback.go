//go:build !linux

package accountdb

// Outside Linux the C library offers no getent, so the exported functions go
// straight to os/user.
var (
	lookupUID   = osUserLookupUID
	lookupName  = osUserLookupName
	lookupGroup = osUserLookupGroup
	groupIDs    = osUserGroupIDs
)
