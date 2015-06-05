package multistream

import (
	"encoding/binary"
	"io"
)

const ProtocolID = "/multistream/version1.0.0"

type HandlerFunc func(io.ReadWriteCloser) error

type MultistreamMuxer struct {
	Handlers map[string]HandlerFunc
}

func NewMultistreamMuxer() *MultistreamMuxer {
	return &MultistreamMuxer{Handlers: make(map[string]HandlerFunc)}
}

func delimWrite(w io.Writer, mes []byte) error {
	varintbuf := make([]byte, 32)
	n := binary.PutUvarint(varintbuf, uint64(len(mes)))
	_, err := w.Write(varintbuf[:n])
	if err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	if err != nil {
		return err
	}
	return nil
}

func (msm *MultistreamMuxer) Handle(rwc io.ReadWriteCloser) error {
	// Send our protocol ID
	err := delimWrite(rwc, []byte(ProtocolID))
	if err != nil {
		return err
	}

loop:
	for {
		// Now read and respond to commands until they send a valid protocol id
		tok, err := ReadNextToken(rwc)
		if err != nil {
			return err
		}

		switch tok {
		case "ls":
			for proto, _ := range msm.Handlers {
				delimWrite(rwc, []byte(proto))
			}
		default:
			h, ok := msm.Handlers[tok]
			if !ok {
				delimWrite(rwc, []byte("no such protocol"))
				continue loop
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
