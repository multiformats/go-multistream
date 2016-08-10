package multistream

import (
	"crypto/rand"
	"io"
	"net"
	"sort"
	"testing"
	"time"
)

func TestProtocolNegotiation(t *testing.T) {
	a, b := net.Pipe()

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

func TestInvalidProtocol(t *testing.T) {
	a, b := net.Pipe()

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
	_, err := ms.Write(nil)
	if err == nil {
		t.Fatal("this write should not succeed")
	}

	select {
	case <-time.After(time.Second):
		t.Fatal("protocol negotiation didnt complete")
	case <-done:
	}
}

func TestSelectOne(t *testing.T) {
	a, b := net.Pipe()

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
	a, b := net.Pipe()

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
	a, b := net.Pipe()

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
	a, b := net.Pipe()

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", nil)

	la := NewMSSelect(a, "/c")
	lb := NewMSSelect(b, "/c")

	verifyPipe(t, la, lb)
}

func TestLazyAndMux(t *testing.T) {
	a, b := net.Pipe()

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
	a, b := net.Pipe()

	mux := NewMultistreamMuxer()
	mux.AddHandler("/a", nil)
	mux.AddHandler("/b", nil)
	mux.AddHandler("/c", func(p string, rwc io.ReadWriteCloser) error {
		if p != "/c" {
			t.Error("failed to get expected protocol!")
		}
		return nil
	})

	go func() {
		err := SelectProtoOrFail("/c", a)
		if err != nil {
			t.Error(err)
		}
	}()

	err := mux.Handle(b)
	if err != nil {
		t.Fatal(err)
	}

	verifyPipe(t, a, b)
}

func TestLazyAndMuxWrite(t *testing.T) {
	a, b := net.Pipe()

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

func verifyPipe(t *testing.T, a, b io.ReadWriter) {
	mes := make([]byte, 1024)
	rand.Read(mes)
	go func() {
		b.Write(mes)
		a.Write(mes)
	}()

	buf := make([]byte, len(mes))
	n, err := a.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) {
		t.Fatal("failed to read enough")
	}

	if string(buf) != string(mes) {
		t.Fatal("somehow read wrong message")
	}

	n, err = b.Read(buf)
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
