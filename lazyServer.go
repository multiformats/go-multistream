package multistream

import (
	"io"
	"sync"
)

type lazyServerConn struct {
	waitForHandshake sync.Once
	werr             error

	con io.ReadWriteCloser
}

func (l *lazyServerConn) Write(b []byte) (int, error) {
	l.waitForHandshake.Do(func() { panic("didn't initiate handshake") })
	if l.werr != nil {
		return 0, l.werr
	}
	return l.con.Write(b)
}

func (l *lazyServerConn) Read(b []byte) (int, error) {
	// TODO: The tests require this for some reason. Not sure if it's correct...
	if len(b) == 0 {
		return 0, nil
	}
	return l.con.Read(b)
}

func (l *lazyServerConn) Close() error {
	return l.con.Close()
}
