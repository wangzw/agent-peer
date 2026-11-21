package directory

import "errors"

// Sentinel errors. Callers match them with errors.Is.
var (
	// ErrNotPublic is returned by DeclarePublicTopic when the Broker is not
	// public.
	ErrNotPublic = errors.New("the Broker is not public; set broker.public to true first")
	// ErrQuotaExceeded is returned when a publisher's live, unrevoked public
	// topic declarations reach directory.max_topics_per_publisher.
	ErrQuotaExceeded = errors.New("public topic quota exceeded")
	// ErrInvalidTopic is returned when a public topic name is empty.
	ErrInvalidTopic = errors.New("invalid topic name")
	// ErrInvalidDescription is returned when a public topic description is
	// longer than 256 bytes, is not valid UTF-8 or contains control
	// characters.
	ErrInvalidDescription = errors.New("invalid public topic description")
	// ErrClosed is returned when the Directory is used after Close.
	ErrClosed = errors.New("the directory is closed")
	// ErrNoIdentity is returned when no local identity is available.
	ErrNoIdentity = errors.New("no local identity is available")
)
