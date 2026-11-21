//go:build !linux

package accountdb

import "time"

// LookupBound bounds one account lookup as far as the rest of the system
// assumes. Outside Linux the lookup goes straight to os/user with no
// subprocess to time out, so this is a sizing assumption rather than an
// enforced limit: five seconds, the same budget the Linux getent path allows
// for a slow network-backed directory.
const LookupBound = 5 * time.Second

// Outside Linux the C library offers no getent, so the exported functions go
// straight to os/user.
var (
	lookupUID   = osUserLookupUID
	lookupName  = osUserLookupName
	lookupGroup = osUserLookupGroup
	groupIDs    = osUserGroupIDs
)
