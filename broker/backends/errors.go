package backends

import "errors"

// ErrNoMessageAvailable is returned by a non-blocking receive/browse when there is no message to return.
var ErrNoMessageAvailable = errors.New("no message available")

// ErrBrowseUnsupported is returned by BrowseBackend.Browse when the backend
// does not support stateful browsing. Callers should fall back to the plain
// Receive loop when they see this error.
var ErrBrowseUnsupported = errors.New("browse not supported by this backend")
