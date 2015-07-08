package multistream

import (
	"errors"
	"io"
)

func NewLazyHandshakeConn(c io.ReadWriteCloser, proto string) io.ReadWriteCloser {
	return &lazyConn{
		proto: proto,
		con:   c,
	}
}

type lazyConn struct {
	rhandshake bool
	whandshake bool
	proto      string
	con        io.ReadWriteCloser
}

func (l *lazyConn) Read(b []byte) (int, error) {
	if !l.rhandshake {
		tok, err := ReadNextToken(l.con)
		if err != nil {
			return 0, err
		}

		if tok != l.proto {
			return 0, errors.New("protocol mismatch in lazy handshake!")
		}

		l.rhandshake = true
	}

	return l.con.Read(b)
}

func (l *lazyConn) Write(b []byte) (int, error) {
	if !l.whandshake {
		err := delimWrite(l.con, []byte(l.proto))
		if err != nil {
			return 0, err
		}

		l.whandshake = true
	}

	return l.con.Write(b)
}

func (l *lazyConn) Close() error {
	return l.con.Close()
}
