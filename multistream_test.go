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
		t.Fatal(err)
	}
	cchan := make(chan net.Conn)
	errChan := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errChan <- err
			return
		}
		cchan <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errChan:
		t.Fatal(err)
		return nil, nil
	case rwc := <-cchan:
		return newRwcStrict(t, rwc), newRwcStrict(t, c)
	}
}

func TestProtocolNegotiation(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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

	verifyPipe(t, a, b)
}

func TestProtocolNegotiationUnsupported(t *testing.T) {
	a, b := newPipe(t)
	mux := NewMultistreamMuxer[string]()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.Negotiate(a)
	}()

	c := NewMSSelect(b, "/foo")
	c.Write([]byte("foo protocol data"))
	_, err := c.Read([]byte{0})
	if err != ErrNotSupported {
		t.Fatalf("expected protocol /foo to be unsupported, got: %v", err)
	}
	c.Close()
	<-done
}

func TestNegLazyStressRead(t *testing.T) {
	const count = 75

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	message := []byte("this is the message")
	listener := make(chan io.ReadWriteCloser)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for rwc := range listener {
			selected, _, err := mux.Negotiate(rwc)
			if err != nil {
				t.Error(err)
				return
			}

			if selected != "/a" {
				t.Error("incorrect protocol selected")
				return
			}

			buf := make([]byte, len(message))
			if _, err := io.ReadFull(rwc, buf); err != nil {
				t.Error(err)
				return
			}

			if !bytes.Equal(message, buf) {
				t.Error("incorrect output: ", buf)
			}
			rwc.Close()
		}
	}()

	for i := 0; i < count; i++ {
		a, b := newPipe(t)
		listener <- a

		ms := NewMSSelect(b, "/a")

		_, err := ms.Write(message)
		if err != nil {
			t.Fatal(err)
		}

		defer b.Close()
	}
	close(listener)
	<-done
}

func TestNegLazyStressWrite(t *testing.T) {
	const count = 100

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	message := []byte("this is the message")
	listener := make(chan io.ReadWriteCloser)
	go func() {
		for rwc := range listener {
			selected, _, err := mux.Negotiate(rwc)
			if err != nil {
				t.Error(err)
				return
			}

			if selected != "/a" {
				t.Error("incorrect protocol selected")
				return
			}

			if _, err := rwc.Write(message); err != nil {
				t.Error(err)
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

	mux := NewMultistreamMuxer[string]()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, err := mux.Negotiate(a)
		if err != ErrIncorrectVersion {
			t.Error("expected incorrect version error here")
		}
	}()

	ms := NewMultistream(b, "/THIS_IS_WRONG")
	_, err := ms.Read([]byte{0})
	if err == nil {
		t.Error("this read should not succeed")
	}

	select {
	case <-time.After(time.Second):
		t.Error("protocol negotiation didnt complete")
	case <-done:
	}
}

func TestSelectOne(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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
	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	la := NewMSSelect(a, "/c")
	lb := NewMSSelect(b, "/c")

	verifyPipe(t, la, lb)
}

func TestLazyAndMux(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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

	mux := NewMultistreamMuxer[string]()
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

	_, err = ReadNextToken[string](buf)
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

func TestNegotiatThenWriteFail(t *testing.T) {
	buf := new(bytes.Buffer)

	err := delimWrite(buf, []byte(ProtocolID))
	if err != nil {
		t.Fatal(err)
	}

	err = delimWrite(buf, []byte("foo"))
	if err != nil {
		t.Fatal(err)
	}

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("foo", nil)

	rob := &readonlyBuffer{bytes.NewReader(buf.Bytes())}
	_, _, err = mux.Negotiate(rob)
	if err != nil {
		t.Fatal("Negotiate should not fail here")
	}

	_, err = rob.Write([]byte("app data"))
	if err == nil {
		t.Fatal("Write should fail here")
	}

}

type mockStream struct {
	expectWrite [][]byte
	toRead      [][]byte
}

func (s *mockStream) Close() error {
	return nil
}

func (s *mockStream) Write(p []byte) (n int, err error) {
	if len(s.expectWrite) == 0 {
		return 0, fmt.Errorf("no more writes expected")
	}

	if !bytes.Equal(s.expectWrite[0], p) {
		return 0, fmt.Errorf("unexpected write")
	}

	s.expectWrite = s.expectWrite[1:]
	return len(p), nil
}

func (s *mockStream) Read(p []byte) (n int, err error) {
	if len(s.toRead) == 0 {
		return 0, fmt.Errorf("no more reads expected")
	}

	if len(p) < len(s.toRead[0]) {
		copy(p, s.toRead[0])
		s.toRead[0] = s.toRead[0][len(p):]
		n = len(p)
	} else {
		copy(p, s.toRead[0])
		n = len(s.toRead[0])
		s.toRead = s.toRead[1:]
	}

	return n, nil
}

func TestNegotiatePeerSendsAndCloses(t *testing.T) {
	// Tests the case where a peer will negotiate a protocol, send data, then close the stream immediately
	var buf bytes.Buffer
	err := delimWrite(&buf, []byte(ProtocolID))
	if err != nil {
		t.Fatal(err)
	}
	delimtedProtocolID := make([]byte, buf.Len())
	copy(delimtedProtocolID, buf.Bytes())

	err = delimWrite(&buf, []byte("foo"))
	if err != nil {
		t.Fatal(err)
	}
	err = delimWrite(&buf, []byte("somedata"))
	if err != nil {
		t.Fatal(err)
	}

	type testCase = struct {
		name string
		s    *mockStream
	}

	testCases := []testCase{
		{
			name: "Able to echo multistream protocol id, but not app protocol id",
			s: &mockStream{
				// We mock the closed stream by only expecting a single write. The
				// mockstream will error on any more writes (same as writing to a closed
				// stream)
				expectWrite: [][]byte{delimtedProtocolID},
				toRead:      [][]byte{buf.Bytes()},
			},
		},
		{
			name: "Not able to write anything. Stream closes too fast",
			s: &mockStream{
				expectWrite: [][]byte{},
				toRead:      [][]byte{buf.Bytes()},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mux := NewMultistreamMuxer[string]()
			mux.AddHandler("foo", nil)
			_, _, err = mux.Negotiate(tc.s)
			if err != nil {
				t.Fatal("Negotiate should not fail here", err)
			}
		})
	}
}

func TestSimopenClientServer(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("/a", nil)

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

	proto, server, err := SelectWithSimopenOrFail([]string{"/a"}, b)
	if err != nil {
		t.Fatal(err)
	}

	if proto != "/a" {
		t.Fatal("wrong protocol selected")
	}

	if server {
		t.Fatal("expected to be client")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didn't complete")
	case <-done:
	}

	verifyPipe(t, a, b)
}

func TestSimopenClientServerFail(t *testing.T) {
	a, b := newPipe(t)

	mux := NewMultistreamMuxer[string]()
	mux.AddHandler("/a", nil)

	done := make(chan struct{})
	go func() {
		_, _, err := mux.Negotiate(a)
		if err != io.EOF {
			t.Error(err)
		}
		close(done)
	}()

	_, _, err := SelectWithSimopenOrFail([]string{"/b"}, b)
	if err != ErrNotSupported {
		t.Fatal(err)
	}
	b.Close()

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didn't complete")
	case <-done:
	}
}

func TestSimopenClientClient(t *testing.T) {
	a, b := newPipe(t)

	done := make(chan bool, 1)
	go func() {
		proto, server, err := SelectWithSimopenOrFail([]string{"/a"}, b)
		if err != nil {
			t.Error(err)
		}
		if proto != "/a" {
			t.Error("wrong protocol selected")
		}
		done <- server
	}()

	proto, servera, err := SelectWithSimopenOrFail([]string{"/a"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if proto != "/a" {
		t.Fatal("wrong protocol selected")
	}

	var serverb bool
	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didn't complete")

	case serverb = <-done:
	}

	if servera == serverb {
		t.Fatal("client selection failed")
	}

	verifyPipe(t, a, b)
}

func TestSimopenClientClient2(t *testing.T) {
	a, b := newPipe(t)

	done := make(chan bool, 1)
	go func() {
		proto, server, err := SelectWithSimopenOrFail([]string{"/a", "/b"}, b)
		if err != nil {
			t.Error(err)
		}
		if proto != "/b" {
			t.Error("wrong protocol selected")
		}
		done <- server
	}()

	proto, servera, err := SelectWithSimopenOrFail([]string{"/b"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if proto != "/b" {
		t.Fatal("wrong protocol selected")
	}

	var serverb bool
	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didn't complete")

	case serverb = <-done:
	}

	if servera == serverb {
		t.Fatal("client selection failed")
	}

	verifyPipe(t, a, b)
}

func TestSimopenClientClientFail(t *testing.T) {
	a, b := newPipe(t)

	done := make(chan struct{})
	go func() {
		_, _, err := SelectWithSimopenOrFail([]string{"/a"}, b)
		if err != ErrNotSupported {
			t.Error(err)
		}
		b.Close()
		close(done)
	}()

	_, _, err := SelectWithSimopenOrFail([]string{"/b"}, a)
	if err != ErrNotSupported {
		t.Fatal(err)
	}
	a.Close()

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didn't complete")

	case <-done:
	}
}
