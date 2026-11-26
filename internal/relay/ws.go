package relay

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA

	maxWSMessageBytes = 16 * 1024 * 1024 // 16 MB cap to prevent memory exhaustion
)

var ErrWSUnauthorized = errors.New("websocket handshake unauthorized (401/403)")

// WSConn is a minimal, zero-dependency RFC 6455 client WebSocket connection.
type WSConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	closed  bool
	closeMu sync.Mutex
}

// WrapWSConn wraps an already upgraded net.Conn as a client WSConn (useful for tests).
func WrapWSConn(conn net.Conn) *WSConn {
	return &WSConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

// DialWS opens a WebSocket connection to wsURL with the provided headers.
func DialWS(ctx context.Context, wsURL string, headers http.Header) (*WSConn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if u.Scheme == "wss" {
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				ServerName: u.Hostname(),
				MinVersion: tls.VersionTLS12,
			},
		}
		conn, err = tlsDialer.DialContext(ctx, "tcp", host)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", host, err)
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	secKey := base64.StdEncoding.EncodeToString(keyBytes)

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}

	var reqBuf strings.Builder
	fmt.Fprintf(&reqBuf, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&reqBuf, "Host: %s\r\n", u.Host)
	reqBuf.WriteString("Upgrade: websocket\r\n")
	reqBuf.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&reqBuf, "Sec-WebSocket-Key: %s\r\n", secKey)
	reqBuf.WriteString("Sec-WebSocket-Version: 13\r\n")
	for k, vals := range headers {
		for _, v := range vals {
			fmt.Fprintf(&reqBuf, "%s: %s\r\n", k, v)
		}
	}
	reqBuf.WriteString("\r\n")

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}

	if _, err := io.WriteString(conn, reqBuf.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("writing handshake request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("reading handshake response: %w", err)
	}
	_ = resp.Body.Close()
	_ = conn.SetDeadline(time.Time{})

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = conn.Close()
		return nil, ErrWSUnauthorized
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected WebSocket handshake HTTP %d", resp.StatusCode)
	}

	expectedAccept := computeAcceptKey(secKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		_ = conn.Close()
		return nil, errors.New("invalid Sec-WebSocket-Accept header from server")
	}

	return &WSConn{
		conn:   conn,
		reader: br,
	}, nil
}

func computeAcceptKey(key string) string {
	const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.Sum([]byte(key + magicGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ReadMessage reads the next complete text or binary message, transparently answering Ping frames.
func (c *WSConn) ReadMessage(ctx context.Context) ([]byte, error) {
	var msgBuf []byte
	for {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}

		switch opcode {
		case wsOpPing:
			_ = c.writeFrame(wsOpPong, payload)
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			_ = c.writeFrame(wsOpClose, nil)
			return nil, io.EOF
		case wsOpText, wsOpBinary, wsOpContinuation:
			if len(msgBuf)+len(payload) > maxWSMessageBytes {
				return nil, fmt.Errorf("WebSocket message exceeds %d byte limit", maxWSMessageBytes)
			}
			msgBuf = append(msgBuf, payload...)
			if fin {
				return msgBuf, nil
			}
		}
	}
}

// WriteText sends a masked text frame to the server.
func (c *WSConn) WriteText(data []byte) error {
	return c.writeFrame(wsOpText, data)
}

// WritePing sends a masked ping frame to the server.
func (c *WSConn) WritePing() error {
	return c.writeFrame(wsOpPing, nil)
}

// Close sends a close frame and closes the underlying socket.
func (c *WSConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()

	_ = c.writeFrame(wsOpClose, nil)
	return c.conn.Close()
}

func (c *WSConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(c.reader, hdr[:]); err != nil {
		return false, 0, nil, err
	}

	fin = (hdr[0] & 0x80) != 0
	opcode = hdr[0] & 0x0F
	masked := (hdr[1] & 0x80) != 0
	length := uint64(hdr[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.reader, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.reader, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	if length > maxWSMessageBytes {
		return false, 0, nil, fmt.Errorf("frame length %d exceeds limit", length)
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(c.reader, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}

	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(c.reader, payload); err != nil {
			return false, 0, nil, err
		}
	}

	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *WSConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var frame []byte
	frame = append(frame, 0x80|opcode)

	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 65535:
		frame = append(frame, 0x80|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		frame = append(frame, ext[:]...)
	default:
		frame = append(frame, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		frame = append(frame, ext[:]...)
	}

	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	frame = append(frame, maskKey[:]...)

	maskedPayload := make([]byte, n)
	for i := 0; i < n; i++ {
		maskedPayload[i] = payload[i] ^ maskKey[i%4]
	}
	frame = append(frame, maskedPayload...)

	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := c.conn.Write(frame)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}
