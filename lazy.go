package multistream

import (
	"fmt"
	"io"
	"sync"
)

func NewLazyHandshakeConn(c io.ReadWriteCloser, proto string) io.ReadWriteCloser {
	return &lazyConn{
		proto: proto,
		con:   c,
	}
}

type lazyConn struct {
	rhandshake bool // only accessed by 'Read' should not call read async

	rhlock sync.Mutex
	rhsync bool //protected by mutex
	err    error

	whandshake bool
	proto      string
	con        io.ReadWriteCloser
}

func (l *lazyConn) Read(b []byte) (int, error) {
	if !l.rhandshake {
		err := l.readHandshake()
		if err != nil {
			return 0, err
		}

		l.rhandshake = true
	}

	if len(b) == 0 {
		return 0, nil
	}

	return l.con.Read(b)
}

func (l *lazyConn) readHandshake() error {
	l.rhlock.Lock()
	defer l.rhlock.Unlock()

	// if we've already done this, exit
	if l.rhsync {
		return l.err
	}
	l.rhsync = true

	// read multistream version
	tok, err := ReadNextToken(l.con)
	if err != nil {
		l.err = err
		return err
	}

	if tok != ProtocolID {
		l.err = fmt.Errorf("multistream protocol mismatch ( %s != %s )", tok, ProtocolID)
		return l.err
	}

	// read protocol
	tok, err = ReadNextToken(l.con)
	if err != nil {
		l.err = err
		return err
	}

	if tok != l.proto {
		l.err = fmt.Errorf("protocol mismatch in lazy handshake ( %s != %s )", tok, l.proto)
		return l.err
	}

	return nil
}

func (l *lazyConn) Write(b []byte) (int, error) {
	if !l.whandshake {
		go l.readHandshake()
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
