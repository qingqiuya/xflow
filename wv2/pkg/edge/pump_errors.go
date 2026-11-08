package edge

import "errors"

var (
	errMessageLoopEnded = errors.New("message loop ended")
	errWaitTimeout      = errors.New("wait timeout")
)
