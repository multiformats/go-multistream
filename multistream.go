// Package multistream implements a simple stream router for the
// multistream-select protocoli. The protocol is defined at
// https://github.com/multiformats/multistream-select
package multistream

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ErrTooLarge is an error to signal that an incoming message was too large
var ErrTooLarge = errors.New("incoming message was too large")

// ProtocolID identifies the multistream protocol itself and makes sure
// the multistream muxers on both sides of a channel can work with each other.
const ProtocolID = "/multistream/1.0.0"

// HandlerFunc is a user-provided function used by the MultistreamMuxer to
// handle a protocol/stream.
type HandlerFunc func(protocol string, rwc io.ReadWriteCloser) error

type handler struct {
	prefix, exclusive bool
	handler           HandlerFunc
}

// MultistreamMuxer is a muxer for multistream. Depending on the stream
// protocol tag it will select the right handler and hand the stream off to it.
type MultistreamMuxer struct {
	handlerlock sync.RWMutex
	handlers    map[string]*handler
}

// NewMultistreamMuxer creates a muxer.
func NewMultistreamMuxer() *MultistreamMuxer {
	return new(MultistreamMuxer)
}

func writeUvarint(w io.Writer, i uint64) error {
	varintbuf := make([]byte, 16)
	n := binary.PutUvarint(varintbuf, i)
	_, err := w.Write(varintbuf[:n])
	if err != nil {
		return err
	}
	return nil
}

func delimWriteBuffered(w io.Writer, mes []byte) error {
	bw := bufio.NewWriter(w)
	err := delimWrite(bw, mes)
	if err != nil {
		return err
	}

	return bw.Flush()
}

func delimWrite(w io.Writer, mes []byte) error {
	err := writeUvarint(w, uint64(len(mes)+1))
	if err != nil {
		return err
	}

	_, err = w.Write(mes)
	if err != nil {
		return err
	}

	_, err = w.Write([]byte{'\n'})
	if err != nil {
		return err
	}
	return nil
}

// Ls is a Multistream muxer command which returns the list of handler names
// available on a muxer.
func Ls(rw io.ReadWriter) ([]string, error) {
	err := delimWriteBuffered(rw, []byte("ls"))
	if err != nil {
		return nil, err
	}

	n, err := binary.ReadUvarint(&byteReader{rw})
	if err != nil {
		return nil, err
	}

	var out []string
	for i := uint64(0); i < n; i++ {
		val, err := lpReadBuf(rw)
		if err != nil {
			return nil, err
		}
		out = append(out, string(val))
	}

	return out, nil
}

type Options struct {
	Prefix, Override, Exclusive bool
}

func (opts *Options) Apply(options ...Option) error {
	for _, opt := range options {
		if err := opt(opts); err != nil {
			return err
		}
	}
	return nil
}

// Option is a stream handler option.
type Option func(*Options) error

// Prefix configures the protocol handler to handle all protocols prefixed
// with the specified protocol name.
//
// Note: This only works for paths. That is, `/a/b` is a protocol-prefix of
// `/a/b/c` but not `/a/bad`.
//
// Defaults to false.
func Prefix(isPrefix bool) Option {
	return func(opts *Options) error {
		opts.Prefix = isPrefix
		return nil
	}
}

// Exclusive configures the protocol handler as the *exclusive* handler for the
// specified protocol and all sub-protocols.
//
// Defaults to false.
func Exclusive(exclusive bool) Option {
	return func(opts *Options) error {
		opts.Exclusive = exclusive
		return nil
	}
}

// Override configures the protocol handler to *override* any existing protocol
// handlers.
//
// If the Exclusive option is passed, any sub-protocol handlers will be
// removed before registering this protocol handler.
//
// Protocol registration will fail if there exists a protocol that:
//   * Is a strict prefix of this protocol.
//   * Has the Exclusive option set.
// Not doing so would either (a) leave some protocols unhandled or (b) break the expected behavior of Exclusive
//
// Defaults to false.
func Override(override bool) Option {
	return func(opts *Options) error {
		opts.Override = override
		return nil
	}
}

// AddHandler attaches a new protocol handler to the muxer, overriding any
// existing handlers.
func (msm *MultistreamMuxer) AddHandler(protocol string, handlerFunc HandlerFunc, options ...Option) error {
	var opts Options
	if err := opts.Apply(options...); err != nil {
		return err
	}
	msm.handlerlock.Lock()
	defer msm.handlerlock.Unlock()

	if msm.handlers == nil {
		msm.handlers = make(map[string]*handler, 1)
	}

	if _, ok := msm.handlers[protocol]; ok {
		// Handle exact match case.
		if !opts.Override {
			// If we haven't specified override, bail.
			return fmt.Errorf("protocol %q already registered", protocol)
		}
		delete(msm.handlers, protocol)

		// If we have successfully overridden the protocol, we *know*
		// there can't be any exclusive prefixes registered.
	} else if currentName, currentHandler := msm.findHandlerLocked(protocol); currentHandler != nil && currentHandler.exclusive {
		// If there *is* an exclusive strict-prefix registered, we can't
		// register this protocol (even if we've specified override),
		// bail.
		return fmt.Errorf("when registering protocol %q, found conflicting exclusive protocol %q", protocol, currentName)
	}

	// If we're registering this protocol exclusively, check for any
	// already-registered sub-protocols.
	if opts.Exclusive {
		prefix := protocol + "/"
		for existing := range msm.handlers {
			if !strings.HasPrefix(existing, prefix) {
				continue
			}
			if !opts.Override {
				return fmt.Errorf("when registering exclusive protocol %q, found conflicting protocol %q", protocol, existing)
			}
			delete(msm.handlers, existing)
		}
	}
	msm.handlers[protocol] = &handler{
		handler:   handlerFunc,
		prefix:    opts.Prefix,
		exclusive: opts.Exclusive,
	}
	return nil
}

// RemoveHandler removes the handler with the given name from the muxer.
func (msm *MultistreamMuxer) RemoveHandler(protocol string) {
	msm.handlerlock.Lock()
	defer msm.handlerlock.Unlock()
	delete(msm.handlers, protocol)
}

// Protocols returns the list of handler-names added to this this muxer.
func (msm *MultistreamMuxer) Protocols() []string {
	msm.handlerlock.RLock()
	out := make([]string, 0, len(msm.handlers))
	for k := range msm.handlers {
		out = append(out, k)
	}
	msm.handlerlock.RUnlock()
	return out
}

// ErrIncorrectVersion is an error reported when the muxer protocol negotiation
// fails because of a ProtocolID mismatch.
var ErrIncorrectVersion = errors.New("client connected with incorrect version")

// findHandler tries to find a handler for the given protocol
func (msm *MultistreamMuxer) findHandler(proto string) (name string, h *handler) {
	msm.handlerlock.Lock()
	defer msm.handlerlock.Unlock()
	return msm.findHandlerLocked(proto)
}

// findHandlerLocked is a version of findHandler that expects the lock to already have been taken.
func (msm *MultistreamMuxer) findHandlerLocked(proto string) (name string, h *handler) {
	handler, ok := msm.handlers[proto]
	if ok {
		return name, handler
	}
	for {
		idx := strings.LastIndexByte(proto, '/')
		if idx < 0 {
			break
		}
		proto = proto[:idx]
		handler, ok := msm.handlers[proto]
		if !ok {
			continue
		}
		// Found a handler but it doesn't handle sub-protocols, bailing.
		if !handler.prefix {
			break
		}
		return name, handler
	}
	return "", nil
}

// NegotiateLazy performs protocol selection and returns
// a multistream, the protocol used, the handler and an error. It is lazy
// because the write-handshake is performed on a subroutine, allowing this
// to return before that handshake is completed.
func (msm *MultistreamMuxer) NegotiateLazy(rwc io.ReadWriteCloser) (Multistream, string, HandlerFunc, error) {
	pval := make(chan string, 1)
	writeErr := make(chan error, 1)
	defer close(pval)

	lzc := &lazyServerConn{
		con: rwc,
	}

	started := make(chan struct{})
	go lzc.waitForHandshake.Do(func() {
		close(started)

		defer close(writeErr)

		if err := delimWriteBuffered(rwc, []byte(ProtocolID)); err != nil {
			lzc.werr = err
			writeErr <- err
			return
		}

		for proto := range pval {
			if err := delimWriteBuffered(rwc, []byte(proto)); err != nil {
				lzc.werr = err
				writeErr <- err
				return
			}
		}
	})
	<-started

	line, err := ReadNextToken(rwc)
	if err != nil {
		return nil, "", nil, err
	}

	if line != ProtocolID {
		rwc.Close()
		return nil, "", nil, ErrIncorrectVersion
	}

loop:
	for {
		// Now read and respond to commands until they send a valid protocol id
		tok, err := ReadNextToken(rwc)
		if err != nil {
			rwc.Close()
			return nil, "", nil, err
		}

		switch tok {
		case "ls":
			select {
			case pval <- "ls":
			case err := <-writeErr:
				rwc.Close()
				return nil, "", nil, err
			}
		default:
			_, h := msm.findHandler(tok)
			if h == nil {
				select {
				case pval <- "na":
				case err := <-writeErr:
					rwc.Close()
					return nil, "", nil, err
				}
				continue loop
			}

			select {
			case pval <- tok:
			case <-writeErr:
				// explicitly ignore this error. It will be returned to any
				// writers and if we don't plan on writing anything, we still
				// want to complete the handshake
			}

			// hand off processing to the sub-protocol handler
			return lzc, tok, h.handler, nil
		}
	}
}

// Negotiate performs protocol selection and returns the protocol name and
// the matching handler function for it (or an error).
func (msm *MultistreamMuxer) Negotiate(rwc io.ReadWriteCloser) (string, HandlerFunc, error) {
	// Send our protocol ID
	err := delimWriteBuffered(rwc, []byte(ProtocolID))
	if err != nil {
		return "", nil, err
	}

	line, err := ReadNextToken(rwc)
	if err != nil {
		return "", nil, err
	}

	if line != ProtocolID {
		rwc.Close()
		return "", nil, ErrIncorrectVersion
	}

loop:
	for {
		// Now read and respond to commands until they send a valid protocol id
		tok, err := ReadNextToken(rwc)
		if err != nil {
			return "", nil, err
		}

		switch tok {
		case "ls":
			err := msm.Ls(rwc)
			if err != nil {
				return "", nil, err
			}
		default:
			_, h := msm.findHandler(tok)
			if h == nil {
				err := delimWriteBuffered(rwc, []byte("na"))
				if err != nil {
					return "", nil, err
				}
				continue loop
			}

			err := delimWriteBuffered(rwc, []byte(tok))
			if err != nil {
				return "", nil, err
			}

			// hand off processing to the sub-protocol handler
			return tok, h.handler, nil
		}
	}

}

// Ls implements the "ls" command which writes the list of
// supported protocols to the given Writer.
func (msm *MultistreamMuxer) Ls(w io.Writer) error {
	buf := new(bytes.Buffer)
	msm.handlerlock.RLock()
	err := writeUvarint(buf, uint64(len(msm.handlers)))
	if err != nil {
		msm.handlerlock.RUnlock()
		return err
	}

	for k := range msm.handlers {
		err := delimWrite(buf, []byte(k))
		if err != nil {
			msm.handlerlock.RUnlock()
			return err
		}
	}
	msm.handlerlock.RUnlock()
	ll := make([]byte, 16)
	nw := binary.PutUvarint(ll, uint64(buf.Len()))

	r := io.MultiReader(bytes.NewReader(ll[:nw]), buf)

	_, err = io.Copy(w, r)
	return err
}

// Handle performs protocol negotiation on a ReadWriteCloser
// (i.e. a connection). It will find a matching handler for the
// incoming protocol and pass the ReadWriteCloser to it.
func (msm *MultistreamMuxer) Handle(rwc io.ReadWriteCloser) error {
	p, h, err := msm.Negotiate(rwc)
	if err != nil {
		return err
	}
	return h(p, rwc)
}

// ReadNextToken extracts a token from a ReadWriter. It is used during
// protocol negotiation and returns a string.
func ReadNextToken(rw io.ReadWriter) (string, error) {
	tok, err := ReadNextTokenBytes(rw)
	if err != nil {
		return "", err
	}

	return string(tok), nil
}

// ReadNextTokenBytes extracts a token from a ReadWriter. It is used
// during protocol negotiation and returns a byte slice.
func ReadNextTokenBytes(rw io.ReadWriter) ([]byte, error) {
	data, err := lpReadBuf(rw)
	switch err {
	case nil:
		return data, nil
	case ErrTooLarge:
		err := delimWriteBuffered(rw, []byte("messages over 64k are not allowed"))
		if err != nil {
			return nil, err
		}
		return nil, ErrTooLarge
	default:
		return nil, err
	}
}

func lpReadBuf(r io.Reader) ([]byte, error) {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = &byteReader{r}
	}

	length, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, err
	}

	if length > 64*1024 {
		return nil, ErrTooLarge
	}

	buf := make([]byte, length)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}

	if len(buf) == 0 || buf[length-1] != '\n' {
		return nil, errors.New("message did not have trailing newline")
	}

	// slice off the trailing newline
	buf = buf[:length-1]

	return buf, nil

}

// byteReader implements the ByteReader interface that ReadUVarint requires
type byteReader struct {
	io.Reader
}

func (br *byteReader) ReadByte() (byte, error) {
	var b [1]byte
	n, err := br.Read(b[:])
	if n == 1 {
		return b[0], nil
	}
	if err == nil {
		if n != 0 {
			panic("read more bytes than buffer size")
		}
		err = io.ErrNoProgress
	}
	return 0, err
}
