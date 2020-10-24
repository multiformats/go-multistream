package multistream

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sort"
	"testing"
	"time"
)

type rwcStrict struct {
	writing, reading bool
	t                *testing.T
	rwc              io.ReadWriteCloser
}

func newRwcStrict(t *testing.T, rwc io.ReadWriteCloser) io.ReadWriteCloser {
	return &rwcStrict{t: t, rwc: rwc}
}

func (s *rwcStrict) Read(b []byte) (int, error) {
	if s.reading {
		s.t.Error("concurrent read")
		return 0, fmt.Errorf("concurrent read")
	}
	s.reading = true
	n, err := s.rwc.Read(b)
	s.reading = false
	return n, err
}

func (s *rwcStrict) Write(b []byte) (int, error) {
	if s.writing {
		s.t.Error("concurrent write")
		return 0, fmt.Errorf("concurrent write")
	}
	s.writing = true
	n, err := s.rwc.Write(b)
	s.writing = false
	return n, err
}

func (s *rwcStrict) Close() error {
	return s.rwc.Close()
}

func newPipe(t *testing.T) (io.ReadWriteCloser, io.ReadWriteCloser) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Error(err)
	}
	cchan := make(chan net.Conn)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			t.Error(err)
		}
		cchan <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Error(err)
	}
	return newRwcStrict(t, <-cchan), newRwcStrict(t, c)
}

func TestProtocolNegotiation(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	done := make(chan struct{})
	go func() {
		selected, _, err := mux.Negotiate(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/a" {
			t.Error("incorrect protocol selected")
		}
		close(done)
	}()

	err := SelectProtoOrFail("/a", b)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}

	verifyPipe(t, a, b)
}

func TestProtocolNegotiationLazy(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	var ac LazyConn
	done := make(chan struct{})
	go func() {
		m, selected, _, err := mux.NegotiateLazy(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/a" {
			t.Error("incorrect protocol selected")
		}
		ac = m
		close(done)
	}()

	sel, err := SelectOneOf([]string{"/foo", "/a"}, b)
	if err != nil {
		t.Fatal(err)
	}

	if sel != "/a" {
		t.Fatal("wrong protocol")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}

	verifyPipe(t, ac, b)
}

func TestNegLazyStressRead(t *testing.T) {
	count := 1000

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	message := []byte("this is the message")
	listener := make(chan io.ReadWriteCloser)
	go func() {
		for rwc := range listener {
			m, selected, _, err := mux.NegotiateLazy(rwc)
			if err != nil {
				t.Fatal(err)
				return
			}

			if selected != "/a" {
				t.Fatal("incorrect protocol selected")
				return
			}

			buf := make([]byte, len(message))
			_, err = io.ReadFull(m, buf)
			if err != nil {
				t.Fatal(err)
				return
			}

			if !bytes.Equal(message, buf) {
				t.Fatal("incorrect output: ", buf)
			}
			rwc.Close()
		}
	}()
	defer func() { close(listener) }()

	for i := 0; i < count; i++ {
		a, b := newPipe(t)
		listener <- a

		ms := NewMSSelect(b, "/a")

		_, err := ms.Write(message)
		if err != nil {
			t.Fatal(err)
		}

		b.Close()
	}
}

func TestNegLazyStressWrite(t *testing.T) {
	count := 1000

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	message := []byte("this is the message")
	listener := make(chan io.ReadWriteCloser)
	go func() {
		for rwc := range listener {
			m, selected, _, err := mux.NegotiateLazy(rwc)
			if err != nil {
				t.Fatal(err)
				return
			}

			if selected != "/a" {
				t.Fatal("incorrect protocol selected")
				return
			}

			_, err = m.Read(nil)
			if err != nil {
				t.Fatal(err)
				return
			}

			_, err = m.Write(message)
			if err != nil {
				t.Fatal(err)
				return
			}

		}
	}()

	for i := 0; i < count; i++ {
		a, b := newPipe(t)
		listener <- a

		ms := NewMSSelect(b, "/a")

		buf := make([]byte, len(message))
		_, err := io.ReadFull(ms, buf)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(message, buf) {
			t.Fatal("incorrect output: ", buf)
		}

		a.Close()
		b.Close()
	}
}

func TestInvalidProtocol(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, err := mux.Negotiate(a)
		if err != ErrIncorrectVersion {
			t.Fatal("expected incorrect version error here")
		}
	}()

	ms := NewMultistream(b, "/THIS_IS_WRONG")
	_, err := ms.Read([]byte{0})
	if err == nil {
		t.Fatal("this read should not succeed")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}
}

func TestSelectOne(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	done := make(chan struct{})
	go func() {
		selected, _, err := mux.Negotiate(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/c" {
			t.Error("incorrect protocol selected")
		}
		close(done)
	}()

	sel, err := SelectOneOf([]string{"/d", "/e", "/c"}, b)
	if err != nil {
		t.Fatal(err)
	}

	if sel != "/c" {
		t.Fatal("selected wrong protocol")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}

	verifyPipe(t, a, b)
}

func TestSelectFails(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	go mux.Negotiate(a)

	_, err := SelectOneOf([]string{"/d", "/e"}, b)
	if err != ErrNotSupported {
		t.Fatal("expected to not be supported")
	}
}

func TestRemoveProtocol(t *testing.T) {
	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	protos := mux.Protocols()
	sort.Strings(protos)
	if protos[0] != "/a" || protos[1] != "/b" || protos[2] != "/c" {
		t.Fatal("didnt get expected protocols")
	}

	mux.RemoveHandler("/b")

	protos = mux.Protocols()
	sort.Strings(protos)
	if protos[0] != "/a" || protos[1] != "/c" {
		t.Fatal("didnt get expected protocols")
	}
}

func TestSelectOneAndWrite(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	done := make(chan struct{})
	go func() {
		selected, _, err := mux.Negotiate(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/c" {
			t.Error("incorrect protocol selected")
		}
		close(done)
	}()

	sel, err := SelectOneOf([]string{"/d", "/e", "/c"}, b)
	if err != nil {
		t.Fatal(err)
	}

	if sel != "/c" {
		t.Fatal("selected wrong protocol")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}

	verifyPipe(t, a, b)
}

func TestLazyConns(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	la := NewMSSelect(a, "/c")
	lb := NewMSSelect(b, "/c")

	verifyPipe(t, la, lb)
}

func TestLazyAndMux(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	done := make(chan struct{})
	go func() {
		selected, _, err := mux.Negotiate(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/c" {
			t.Error("incorrect protocol selected")
		}

		msg := make([]byte, 5)
		_, err = a.Read(msg)
		if err != nil {
			t.Error(err)
		}

		close(done)
	}()

	lb := NewMSSelect(b, "/c")

	// do a write to push the handshake through
	_, err := lb.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("failed to complete in time")
	case <-done:
	}

	verifyPipe(t, a, lb)
}

func TestHandleFunc(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", func(p string, rwc io.ReadWriteCloser) error {
		if p != "/c" {
			t.Error("failed to get expected protocol!")
		}
		return nil
	})

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		err := SelectProtoOrFail("/c", a)
		if err != nil {
			t.Error(err)
		}
	}()

	err := mux.Handle(b)
	if err != nil {
		t.Fatal(err)
	}

	<-ch
	verifyPipe(t, a, b)
}

func TestAddHandlerOverride(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/foo", func(p string, rwc io.ReadWriteCloser) error {
		t.Error("shouldnt execute this handler")
		return nil
	})

	mux.AddHandler("/foo", func(p string, rwc io.ReadWriteCloser) error {
		return nil
	})

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		err := SelectProtoOrFail("/foo", a)
		if err != nil {
			t.Error(err)
		}
	}()

	err := mux.Handle(b)
	if err != nil {
		t.Fatal(err)
	}

	<-ch
	verifyPipe(t, a, b)
}

func TestLazyAndMuxWrite(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	done := make(chan struct{})
	go func() {
		selected, _, err := mux.Negotiate(a)
		if err != nil {
			t.Error(err)
		}
		if selected != "/c" {
			t.Error("incorrect protocol selected")
		}

		_, err = a.Write([]byte("hello"))
		if err != nil {
			t.Error(err)
		}

		close(done)
	}()

	lb := NewMSSelect(b, "/c")

	// do a write to push the handshake through
	msg := make([]byte, 5)
	_, err := lb.Read(msg)
	if err != nil {
		t.Fatal(err)
	}

	if string(msg) != "hello" {
		t.Fatal("wrong!")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("failed to complete in time")
	case <-done:
	}

	verifyPipe(t, a, lb)
}

func verifyPipe(t *testing.T, a, b io.ReadWriteCloser) {
	mes := make([]byte, 1024)
	rand.Read(mes)
	go func() {
		b.Write(mes)
		a.Write(mes)
	}()

	buf := make([]byte, len(mes))
	n, err := io.ReadFull(a, buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) {
		t.Fatal("failed to read enough")
	}

	if string(buf) != string(mes) {
		t.Fatalf("somehow read wrong message, expected: %x, was: %x", mes, buf)
	}

	n, err = io.ReadFull(b, buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) {
		t.Fatal("failed to read enough")
	}

	if string(buf) != string(mes) {
		t.Fatal("somehow read wrong message")
	}
}

func TestTooLargeMessage(t *testing.T) {
	buf := new(bytes.Buffer)
	mes := make([]byte, 100*1024)

	err := delimWrite(buf, mes)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ReadNextToken(buf)
	if err == nil {
		t.Fatal("should have failed to read message larger than 64k")
	}
}

// this exercises https://github.com/libp2p/go-libp2p-pnet/issues/31
func TestLargeMessageNegotiate(t *testing.T) {
	mes := make([]byte, 100*1024)

	a, b := newPipe(t)
	err := delimWrite(a, mes)
	if err != nil {
		t.Fatal(err)
	}
	err = SelectProtoOrFail("/foo/bar", b)
	if err == nil {
		t.Error("should have failed to read large message")
	}
}

func TestLs(t *testing.T) {
	t.Run("none-eager", subtestLs(nil, false))
	t.Run("one-eager", subtestLs([]string{"a"}, false))
	t.Run("many-eager", subtestLs([]string{"a", "b", "c", "d", "e"}, false))
	t.Run("empty-eager", subtestLs([]string{"", "a"}, false))

	// lazy variants
	t.Run("none-lazy", subtestLs(nil, true))
	t.Run("one-lazy", subtestLs([]string{"a"}, true))
	t.Run("many-lazy", subtestLs([]string{"a", "b", "c", "d", "e"}, true))
	t.Run("empty-lazy", subtestLs([]string{"", "a"}, true))
}

func subtestLs(protos []string, lazy bool) func(*testing.T) {
	return func(t *testing.T) {
		mr := NewMultistreamMuxer()
		mset := make(map[string]bool)
		for _, p := range protos {
			mr.AddHandler(p, nil)
			mset[p] = true
		}

		c1, c2 := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)

			var proto string
			var err error
			if lazy {
				_, proto, _, err = mr.NegotiateLazy(c2)
			} else {
				proto, _, err = mr.Negotiate(c2)
			}

			c2.Close()
			if err != io.EOF {
				t.Error(err)
			}
			if proto != "" {
				t.Errorf("expected no proto, got %s", proto)
			}
		}()
		defer func() { <-done }()

		items, err := Ls(c1)
		if err != nil {
			t.Fatal(err)
		}
		c1.Close()

		if len(items) != len(protos) {
			t.Fatal("got wrong number of protocols")
		}

		for _, tok := range items {
			if !mset[tok] {
				t.Fatalf("wasnt expecting protocol %s", tok)
			}
		}
	}
}

type readonlyBuffer struct {
	buf io.Reader
}

func (rob *readonlyBuffer) Read(b []byte) (int, error) {
	return rob.buf.Read(b)
}

func (rob *readonlyBuffer) Write(b []byte) (int, error) {
	return 0, fmt.Errorf("cannot write on this pipe")
}

func (rob *readonlyBuffer) Close() error {
	return nil
}

func TestNegotiateFail(t *testing.T) {
	buf := new(bytes.Buffer)

	err := delimWrite(buf, []byte(ProtocolID))
	if err != nil {
		t.Fatal(err)
	}

	err = delimWrite(buf, []byte("foo"))
	if err != nil {
		t.Fatal(err)
	}

	mux := NewMultistreamMuxer()
	mux.AddHandler("foo", nil)

	rob := &readonlyBuffer{bytes.NewReader(buf.Bytes())}
	_, _, err = mux.Negotiate(rob)
	if err == nil {
		t.Fatal("normal negotiate should fail here")
	}

	rob = &readonlyBuffer{bytes.NewReader(buf.Bytes())}
	_, out, _, err := mux.NegotiateLazy(rob)
	if err != nil {
		t.Fatal("expected lazy negoatiate to succeed")
	}

	if out != "foo" {
		t.Fatal("got wrong protocol")
	}
}
