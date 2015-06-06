package multistream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

const ProtocolID = "/multistream/1.0.0"

type HandlerFunc func(io.ReadWriteCloser) error

type MultistreamMuxer struct {
	handlerlock sync.Mutex
	handlers    map[string]HandlerFunc
}

func NewMultistreamMuxer() *MultistreamMuxer {
	return &MultistreamMuxer{handlers: make(map[string]HandlerFunc)}
}

func writeUvarint(w io.Writer, i uint64) error {
	varintbuf := make([]byte, 32)
	n := binary.PutUvarint(varintbuf, i)
	_, err := w.Write(varintbuf[:n])
	if err != nil {
		return err
	}
	return nil
}

func delimWrite(w io.Writer, mes []byte) error {
	err := writeUvarint(w, uint64(len(mes)))
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

func (msm *MultistreamMuxer) AddHandler(protocol string, handler HandlerFunc) {
	msm.handlerlock.Lock()
	msm.handlers[protocol] = handler
	msm.handlerlock.Unlock()
}

func (msm *MultistreamMuxer) Handle(rwc io.ReadWriteCloser) error {
	// Send our protocol ID
	err := delimWrite(rwc, []byte(ProtocolID))
	if err != nil {
		return err
	}

	fmt.Println("WROTE HELLO")
loop:
	for {
		// Now read and respond to commands until they send a valid protocol id
		tok, err := ReadNextToken(rwc)
		if err != nil {
			return err
		}

		switch tok {
		case "ls":
			buf := new(bytes.Buffer)
			msm.handlerlock.Lock()
			for proto, _ := range msm.handlers {
				err := delimWrite(buf, []byte(proto))
				if err != nil {
					msm.handlerlock.Unlock()
					return err
				}
			}
			msm.handlerlock.Unlock()
			err := delimWrite(rwc, buf.Bytes())
			if err != nil {
				return err
			}
		default:
			msm.handlerlock.Lock()
			h, ok := msm.handlers[tok]
			msm.handlerlock.Unlock()
			if !ok {
				err := delimWrite(rwc, []byte("na"))
				if err != nil {
					return err
				}
				continue loop
			}

			err := delimWrite(rwc, []byte(tok))
			if err != nil {
				return err
			}
			// hand off processing to the sub-protocol handler
			return h(rwc)
		}
	}
}

func ReadNextToken(rw io.ReadWriter) (string, error) {
	br := &byteReader{rw}
	length, err := binary.ReadUvarint(br)
	if err != nil {
		return "", err
	}

	if length > 64*1024 {
		err := delimWrite(rw, []byte("messages over 64k are not allowed"))
		if err != nil {
			return "", err
		}
		//TODO: should we error out here?
	}

	buf := make([]byte, length)
	_, err = io.ReadFull(rw, buf)
	if err != nil {
		return "", err
	}

	nline, err := br.ReadByte()
	if err != nil {
		return "", err
	}

	if nline != '\n' {
		panic("oh my god oh my god oh my god oh my god")
	}

	return string(buf), nil
}

// byteReader implements the ByteReader interface that ReadUVarint requires
type byteReader struct {
	io.Reader
}

func (br *byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := br.Read(b[:])

	if err != nil {
		return 0, err
	}
	return b[0], nil
}
