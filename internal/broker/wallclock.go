package broker

import "time"

// wallClock is the clock the directory uses to decide whether cards and public
// topic announcements have expired or are due for renewal.
//
// It is a variable so builds carrying the peers_testenv tag can replace it
// with a clock advanced by a file, which lets end-to-end tests move directory
// time forward without sleeping.
var wallClock = time.Now
