package multistream

import (
	"fmt"
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
		// read multistream version
		tok, err := ReadNextToken(l.con)
		if err != nil {
			return 0, err
		}

		if tok != ProtocolID {
			return 0, fmt.Errorf("multistream protocol mismatch ( %s != %s )", tok, ProtocolID)
		}

		// read protocol
		tok, err = ReadNextToken(l.con)
		if err != nil {
			return 0, err
		}

		if tok != l.proto {
			return 0, fmt.Errorf("protocol mismatch in lazy handshake ( %s != %s )", tok, l.proto)
		}

		l.rhandshake = true
	}

	if len(b) == 0 {
		return 0, nil
	}

	return l.con.Read(b)
}

func (l *lazyConn) Write(b []byte) (int, error) {
	if !l.whandshake {
		err := delimWrite(l.con, []byte(ProtocolID))
		if err != nil {
			return 0, err
		}

		err = delimWrite(l.con, []byte(l.proto))
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
