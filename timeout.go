package multistream

import (
	"fmt"
	"io"
	"time"
)

// NegotiationTimeout is the maximum time a protocol negotiation atempt is
// allowed to be inflight before it fails.
var NegotiationTimeout = 30 * time.Second

// setDeadline attempts to set a read and write deadline on the underlying IO
// object, if it supports it.
func setDeadline(rwc io.ReadWriteCloser) (func(), error) {
	// rwc could be:
	// - a net.Conn or a libp2p Stream, both of which satisfy this interface.
	// - something else (e.g. testing), in which case we skip over setting
	//   a deadline.
	type deadline interface {
		SetDeadline(time.Time) error
	}
	if d, ok := rwc.(deadline); ok {
		if err := d.SetDeadline(time.Now().Add(NegotiationTimeout)); err != nil {
			// this should not happen; if it does, something is broken and we
			// should fail immediately.
			return nil, fmt.Errorf("failed while setting a deadline: %w", err)
		}
		return func() { d.SetDeadline(time.Time{}) }, nil
	}
	return func() {}, nil
}
