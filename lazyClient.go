package multistream

import (
	"fmt"
	"io"
)

// NewMSSelect returns a new Multistream which is able to perform
// protocol selection with a MultistreamMuxer.
func NewMSSelect[T StringLike](c io.ReadWriteCloser, proto T) LazyConn {
	return &lazyClientConn[T]{
		protos: []T{ProtocolID, proto},
		con:    c,

		rhandshakeOnce: newOnce(),
		whandshakeOnce: newOnce(),
	}
}

// NewMultistream returns a multistream for the given protocol. This will not
// perform any protocol selection. If you are using a MultistreamMuxer, use
// NewMSSelect.
func NewMultistream[T StringLike](c io.ReadWriteCloser, proto T) LazyConn {
	return &lazyClientConn[T]{
		protos: []T{proto},
		con:    c,

		rhandshakeOnce: newOnce(),
		whandshakeOnce: newOnce(),
	}
}

// once is a sync.Once that can be used by synctest.
// For Multistream, it is a bit better than sync.Once because it doesn't
// spin when acquiring the lock.
type once struct {
	sem chan struct{}
}

func newOnce() *once {
	o := once{
		sem: make(chan struct{}, 1),
	}
	o.sem <- struct{}{}
	return &o
}

func (o *once) Do(f func()) {
	// We only ever pull a single value from the channel. But we want to block
	// Do until the first call to Do has completed. The first call will close
	// the channel, so by checking if it's closed we know we don't need to do
	// anything.
	_, ok := <-o.sem
	if !ok {
		return
	}
	defer close(o.sem)
	f()
}

// lazyClientConn is a ReadWriteCloser adapter that lazily negotiates a protocol
// using multistream-select on first use.
//
// It *does not* block writes waiting for the other end to respond. Instead, it
// simply assumes the negotiation went successfully and starts writing data.
// See: https://github.com/multiformats/go-multistream/issues/20
type lazyClientConn[T StringLike] struct {
	// Used to ensure we only trigger the write half of the handshake once.
	rhandshakeOnce *once
	rerr           error

	// Used to ensure we only trigger the read half of the handshake once.
	whandshakeOnce *once
	werr           error

	// The sequence of protocols to negotiate.
	protos []T

	// The inner connection.
	con io.ReadWriteCloser
}

// Read reads data from the io.ReadWriteCloser.
//
// If the protocol hasn't yet been negotiated, this method triggers the write
// half of the handshake and then waits for the read half to complete.
//
// It returns an error if the read half of the handshake fails.
func (l *lazyClientConn[T]) Read(b []byte) (int, error) {
	l.rhandshakeOnce.Do(func() {
		go l.whandshakeOnce.Do(l.doWriteHandshake)
		l.doReadHandshake()
	})
	if l.rerr != nil {
		return 0, l.rerr
	}
	if len(b) == 0 {
		return 0, nil
	}

	return l.con.Read(b)
}

func (l *lazyClientConn[T]) doReadHandshake() {
	for _, proto := range l.protos {
		// read protocol
		tok, err := ReadNextToken[T](l.con)
		if err != nil {
			l.rerr = err
			return
		}

		if tok == "na" {
			l.rerr = ErrNotSupported[T]{[]T{proto}}
			return
		}
		if tok != proto {
			l.rerr = fmt.Errorf("protocol mismatch in lazy handshake ( %s != %s )", tok, proto)
			return
		}
	}
}

func (l *lazyClientConn[T]) doWriteHandshake() {
	l.doWriteHandshakeWithData(nil)
}

// Perform the write handshake but *also* write some extra data.
func (l *lazyClientConn[T]) doWriteHandshakeWithData(extra []byte) int {
	buf := getWriter(l.con)
	defer putWriter(buf)

	for _, proto := range l.protos {
		l.werr = delimWrite(buf, []byte(proto))
		if l.werr != nil {
			return 0
		}
	}

	n := 0
	if len(extra) > 0 {
		n, l.werr = buf.Write(extra)
		if l.werr != nil {
			return n
		}
	}
	l.werr = buf.Flush()
	return n
}

// Write writes the given buffer to the underlying connection.
//
// If the protocol has not yet been negotiated, write waits for the write half
// of the handshake to complete triggers (but does not wait for) the read half.
//
// Write *also* ignores errors from the read half of the handshake (in case the
// stream is actually write only).
func (l *lazyClientConn[T]) Write(b []byte) (int, error) {
	n := 0
	l.whandshakeOnce.Do(func() {
		go l.rhandshakeOnce.Do(l.doReadHandshake)
		n = l.doWriteHandshakeWithData(b)
	})
	if l.werr != nil || n > 0 {
		return n, l.werr
	}
	return l.con.Write(b)
}

// Close closes the underlying io.ReadWriteCloser after finishing the handshake.
func (l *lazyClientConn[T]) Close() error {
	// As the client, we flush the handshake on close to cover an
	// interesting edge-case where the server only speaks a single protocol
	// and responds eagerly with that protocol before waiting for out
	// handshake.
	//
	// Again, we must not read the error because the other end may have
	// closed the stream for reading. I mean, we're the initiator so that's
	// strange... but it's still allowed
	_ = l.Flush()

	// Finish reading the handshake before we close the connection/stream. This
	// is necessary so that the other side can finish sending its response to our
	// multistream header before we tell it we are done reading.
	//
	// Example:
	// We open a QUIC stream, write the protocol `/a`, send 1 byte of application
	// data, and immediately close.
	//
	// This can result in a single packet that contains the stream data along
	// with a STOP_SENDING frame. The other side may be unable to negotiate
	// multistream select since it can't write to the stream anymore and may
	// drop the stream.
	//
	// Note: We currently handle this case in Go(https://github.com/multiformats/go-multistream/pull/87), but rust-libp2p does not.
	l.rhandshakeOnce.Do(l.doReadHandshake)
	return l.con.Close()
}

// Flush sends the handshake.
func (l *lazyClientConn[T]) Flush() error {
	l.whandshakeOnce.Do(func() {
		go l.rhandshakeOnce.Do(l.doReadHandshake)
		l.doWriteHandshake()
	})
	return l.werr
}
