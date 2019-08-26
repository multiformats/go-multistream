package multistream

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"math/big"
	"strings"
)

// ErrNotSupported is the error returned when the muxer does not support
// the protocol specified for the handshake.
var ErrNotSupported = errors.New("protocol not supported")

// ErrNoProtocols is the error returned when the no protocols have been
// specified.
var ErrNoProtocols = errors.New("no protocols specified")

// SelectProtoOrFail performs the initial multistream handshake
// to inform the muxer of the protocol that will be used to communicate
// on this ReadWriteCloser. It returns an error if, for example,
// the muxer does not know how to handle this protocol.
func SelectProtoOrFail(proto string, rwc io.ReadWriteCloser) error {
	errCh := make(chan error, 1)
	go func() {
		var buf bytes.Buffer
		delimWrite(&buf, []byte(ProtocolID))
		delimWrite(&buf, []byte(proto))
		_, err := io.Copy(rwc, &buf)
		errCh <- err
	}()
	// We have to read *both* errors.
	err1 := readMultistreamHeader(rwc)
	err2 := readProto(proto, rwc)
	if werr := <-errCh; werr != nil {
		return werr
	}
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}

// SelectOneOf will perform handshakes with the protocols on the given slice
// until it finds one which is supported by the muxer.
func SelectOneOf(protos []string, rwc io.ReadWriteCloser) (string, error) {
	if len(protos) == 0 {
		return "", ErrNoProtocols
	}

	// Use SelectProtoOrFail to pipeline the /multistream/1.0.0 handshake
	// with an attempt to negotiate the first protocol. If that fails, we
	// can continue negotiating the rest of the protocols normally.
	//
	// This saves us a round trip.
	switch err := SelectProtoOrFail(protos[0], rwc); err {
	case nil:
		return protos[0], nil
	case ErrNotSupported: // try others
	default:
		return "", err
	}
	for _, p := range protos[1:] {
		err := trySelect(p, rwc)
		switch err {
		case nil:
			return p, nil
		case ErrNotSupported:
		default:
			return "", err
		}
	}
	return "", ErrNotSupported
}

// Performs protocol negotiation with the simultaneous open extension; the returned boolean
// indicator will be true if we should act as a server.
func SelectWithSimopen(protos []string, rwc io.ReadWriteCloser) (string, bool, error) {
	if len(protos) == 0 {
		return "", false, ErrNoProtocols
	}

	var buf bytes.Buffer
	delimWrite(&buf, []byte(ProtocolID))
	delimWrite(&buf, []byte("iamclient"))
	delimWrite(&buf, []byte(protos[0]))
	_, err := io.Copy(rwc, &buf)
	if err != nil {
		return "", false, err
	}

	err = readMultistreamHeader(rwc)
	if err != nil {
		return "", false, err
	}

	tok, err := ReadNextToken(rwc)
	if err != nil {
		return "", false, err
	}

	switch tok {
	case "iamclient":
		// simultaneous open
		return simOpen(protos, rwc)

	case "na":
		// client open
		proto, err := clientOpen(protos, rwc)
		if err != nil {
			return "", false, err
		}

		return proto, false, nil

	default:
		return "", false, errors.New("unexpected response: " + tok)
	}
}

func clientOpen(protos []string, rwc io.ReadWriteCloser) (string, error) {
	// check to see if we selected the pipelined protocol
	tok, err := ReadNextToken(rwc)
	if err != nil {
		return "", err
	}

	switch tok {
	case protos[0]:
		return tok, nil
	case "na":
		// try the other protos
		for _, p := range protos[1:] {
			err = trySelect(p, rwc)
			switch err {
			case nil:
				return p, nil
			case ErrNotSupported:
			default:
				return "", err
			}
		}

		return "", ErrNotSupported
	default:
		return "", errors.New("unexpected response: " + tok)
	}
}

func simOpen(protos []string, rwc io.ReadWriteCloser) (string, bool, error) {
	retries := 3

again:
	mynonce := make([]byte, 32)
	_, err := rand.Read(mynonce)
	if err != nil {
		return "", false, err
	}

	myselect := []byte("select:" + string(mynonce))
	err = delimWriteBuffered(rwc, myselect)
	if err != nil {
		return "", false, err
	}

	var peerselect string
	for {
		tok, err := ReadNextToken(rwc)
		if err != nil {
			return "", false, err
		}

		// this skips pipelined protocol negoatiation
		// keep reading until the token starts with select:
		if strings.HasPrefix(tok, "select:") {
			peerselect = tok
			break
		}
	}

	peernonce := []byte(peerselect[7:])

	var mybig, peerbig big.Int
	var iamserver bool
	mybig.SetBytes(mynonce)
	peerbig.SetBytes(peernonce)

	switch mybig.Cmp(&peerbig) {
	case -1:
		// peer nonce bigger, he is client
		iamserver = true

	case 1:
		// my nonce bigger, i am client
		iamserver = false

	case 0:
		// wtf, the world is ending! try again.
		if retries > 0 {
			retries--
			goto again
		}

		return "", false, errors.New("failed client selection; identical nonces!")

	default:
		return "", false, errors.New("wut? bigint.Cmp returned unexpected value")
	}

	var proto string
	if iamserver {
		proto, err = simOpenSelectServer(protos, rwc)
	} else {
		proto, err = simOpenSelectClient(protos, rwc)
	}

	return proto, iamserver, err
}

func simOpenSelectServer(protos []string, rwc io.ReadWriteCloser) (string, error) {
	err := delimWriteBuffered(rwc, []byte("responder"))
	if err != nil {
		return "", err
	}

	tok, err := ReadNextToken(rwc)
	if err != nil {
		return "", err
	}
	if tok != "initiator" {
		return "", errors.New("unexpected response: " + tok)
	}

	for {
		tok, err = ReadNextToken(rwc)
		if err != nil {
			return "", err
		}

		for _, p := range protos {
			if tok == p {
				err = delimWriteBuffered(rwc, []byte(p))
				if err != nil {
					return "", err
				}

				return p, nil
			}
		}

		err = delimWriteBuffered(rwc, []byte("na"))
		if err != nil {
			return "", err
		}
	}

}

func simOpenSelectClient(protos []string, rwc io.ReadWriteCloser) (string, error) {
	err := delimWriteBuffered(rwc, []byte("initiator"))
	if err != nil {
		return "", err
	}

	tok, err := ReadNextToken(rwc)
	if err != nil {
		return "", err
	}
	if tok != "responder" {
		return "", errors.New("unexpected response: " + tok)
	}

	for _, p := range protos {
		err = trySelect(p, rwc)
		switch err {
		case nil:
			return p, nil

		case ErrNotSupported:
		default:
			return "", err
		}
	}

	return "", ErrNotSupported
}

func handshake(rw io.ReadWriter) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- delimWriteBuffered(rw, []byte(ProtocolID))
	}()

	if err := readMultistreamHeader(rw); err != nil {
		return err
	}
	return <-errCh
}

func readMultistreamHeader(r io.Reader) error {
	tok, err := ReadNextToken(r)
	if err != nil {
		return err
	}

	if tok != ProtocolID {
		return errors.New("received mismatch in protocol id")
	}
	return nil
}

func trySelect(proto string, rwc io.ReadWriteCloser) error {
	err := delimWriteBuffered(rwc, []byte(proto))
	if err != nil {
		return err
	}
	return readProto(proto, rwc)
}

func readProto(proto string, r io.Reader) error {
	tok, err := ReadNextToken(r)
	if err != nil {
		return err
	}

	switch tok {
	case proto:
		return nil
	case "na":
		return ErrNotSupported
	default:
		return errors.New("unrecognized response: " + tok)
	}
}
