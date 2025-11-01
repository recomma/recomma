package debugmode

import "errors"

// ErrUnavailable indicates the binary was built without debug mode support.
var ErrUnavailable = errors.New("debug mode was not compiled into this binary")
