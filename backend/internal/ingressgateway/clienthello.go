package ingressgateway

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

const maxHelloBytes = 65536
const maxHelloRecords = 128

type clientHello struct {
	serverName string
	protocols  []string
	raw        []byte
}

// Read complete records, including a ClientHello fragmented over TLS records.
// Preserve every consumed byte for replay to the original TLS/Reality peer.
// Bounds apply to records, total wire size and declared handshake size.
func readClientHello(r io.Reader) (clientHello, error) {
	var out clientHello
	body := make([]byte, 0, 4096)
	wanted := 0
	for records := 0; records < maxHelloRecords; records++ {
		var h [5]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return out, err
		}
		size := int(binary.BigEndian.Uint16(h[3:]))
		if h[0] != 22 || h[1] != 3 || h[2] > 3 || size == 0 || size > 18432 || len(out.raw)+5+size > maxHelloBytes {
			return out, errors.New("invalid or oversized TLS ClientHello record")
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(r, b); err != nil {
			return out, err
		}
		out.raw = append(out.raw, h[:]...)
		out.raw = append(out.raw, b...)
		body = append(body, b...)
		if len(body) >= 4 && wanted == 0 {
			if body[0] != 1 {
				return out, errors.New("first TLS handshake must be ClientHello")
			}
			wanted = 4 + int(body[1])<<16 + int(body[2])<<8 + int(body[3])
			if wanted < 42 || wanted > maxHelloBytes-5 {
				return out, errors.New("invalid ClientHello length")
			}
		}
		if wanted > 0 && len(body) >= wanted {
			name, protocols, err := parseHelloBody(body[4:wanted])
			out.serverName, out.protocols = name, protocols
			return out, err
		}
	}
	return out, errors.New("too many TLS handshake fragments")
}

type helloCursor []byte

func (c *helloCursor) take(n int) ([]byte, bool) {
	if n < 0 || n > len(*c) {
		return nil, false
	}
	b := (*c)[:n]
	*c = (*c)[n:]
	return b, true
}
func (c *helloCursor) u8() (int, bool) {
	b, ok := c.take(1)
	if !ok {
		return 0, false
	}
	return int(b[0]), true
}
func (c *helloCursor) u16() (int, bool) {
	b, ok := c.take(2)
	if !ok {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(b)), true
}

func parseHelloBody(b []byte) (string, []string, error) {
	fail := errors.New("malformed TLS ClientHello")
	c := helloCursor(b)
	if _, ok := c.take(34); !ok {
		return "", nil, fail
	}
	n, ok := c.u8()
	if !ok || n > 32 {
		return "", nil, fail
	}
	if _, ok = c.take(n); !ok {
		return "", nil, fail
	}
	n, ok = c.u16()
	if !ok || n < 2 || n%2 != 0 {
		return "", nil, fail
	}
	if _, ok = c.take(n); !ok {
		return "", nil, fail
	}
	n, ok = c.u8()
	if !ok || n == 0 {
		return "", nil, fail
	}
	if _, ok = c.take(n); !ok {
		return "", nil, fail
	}
	n, ok = c.u16()
	if !ok || n != len(c) {
		return "", nil, fail
	}
	name := ""
	var protocols []string
	seen := map[int]bool{}
	for len(c) > 0 {
		kind, ok := c.u16()
		if !ok {
			return "", nil, fail
		}
		n, ok = c.u16()
		if !ok {
			return "", nil, fail
		}
		value, ok := c.take(n)
		if !ok || seen[kind] {
			return "", nil, fail
		}
		seen[kind] = true
		e := helloCursor(value)
		switch kind {
		case 0:
			length, ok := e.u16()
			if !ok || length != len(e) {
				return "", nil, fail
			}
			for len(e) > 0 {
				typ, ok := e.u8()
				if !ok || typ != 0 || name != "" {
					return "", nil, fail
				}
				length, ok = e.u16()
				if !ok {
					return "", nil, fail
				}
				v, ok := e.take(length)
				if !ok {
					return "", nil, fail
				}
				name = strings.ToLower(string(v))
				if !validDomain(name) {
					return "", nil, errors.New("invalid TLS server name")
				}
			}
		case 16:
			length, ok := e.u16()
			if !ok || length != len(e) || length == 0 {
				return "", nil, fail
			}
			for len(e) > 0 {
				length, ok = e.u8()
				if !ok || length == 0 {
					return "", nil, fail
				}
				v, ok := e.take(length)
				if !ok {
					return "", nil, fail
				}
				protocols = append(protocols, string(v))
			}
		}
	}
	if name == "" {
		return "", nil, errors.New("TLS server name is required")
	}
	for _, p := range protocols {
		if p == "acme-tls/1" && len(protocols) != 1 {
			return "", nil, errors.New("ACME ALPN cannot be mixed with application protocols")
		}
	}
	return name, protocols, nil
}
