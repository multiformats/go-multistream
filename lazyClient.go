package multistream

import (
	"bufio"
	"fmt"
	"io"
	"sync"
)

// Multistream represents in essense a ReadWriteCloser, or a single
// communication wire which supports multiple streams on it. Each
// stream is identified by a protocol tag.
type Multistream interface {
	io.ReadWriteCloser
}

// NewMSSelect returns a new Multistream which is able to perform
// protocol selection with a MultistreamMuxer.
func NewMSSelect(c io.ReadWriteCloser, proto string) Multistream {
	return &lazyClientConn{
		protos: []string{ProtocolID, proto},
		con:    c,
	}
}

// NewMultistream returns a multistream for the given protocol. This will not
// perform any protocol selection. If you are using a MultistreamMuxer, use
// NewMSSelect.
func NewMultistream(c io.ReadWriteCloser, proto string) Multistream {
	return &lazyClientConn{
		protos: []string{proto},
		con:    c,
	}
}

type lazyClientConn struct {
	rhandshakeOnce sync.Once
	rerr           error

	whandshakeOnce sync.Once
	werr           error

	protos []string
	con    io.ReadWriteCloser
}

func (l *lazyClientConn) Read(b []byte) (int, error) {
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

func (l *lazyClientConn) doReadHandshake() {
	for _, proto := range l.protos {
		// read protocol
		tok, err := ReadNextToken(l.con)
		if err != nil {
			l.rerr = err
			return
		}

		if tok != proto {
			l.rerr = fmt.Errorf("protocol mismatch in lazy handshake ( %s != %s )", tok, proto)
			return
		}
	}
}

func (l *lazyClientConn) doWriteHandshake() {
	l.doWriteHandshakeWithExtra(nil)
}

// Perform the write handshake but *also* write some extra data.
func (l *lazyClientConn) doWriteHandshakeWithExtra(extra []byte) int {
	buf := bufio.NewWriter(l.con)
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

func (l *lazyClientConn) Write(b []byte) (int, error) {
	n := 0
	l.whandshakeOnce.Do(func() {
		go l.rhandshakeOnce.Do(l.doReadHandshake)
		n = l.doWriteHandshakeWithExtra(b)
	})
	if l.werr != nil || n > 0 {
		return n, l.werr
	}
	return l.con.Write(b)
}

func (l *lazyClientConn) Close() error {
	return l.con.Close()
}
