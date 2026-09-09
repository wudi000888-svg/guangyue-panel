// Package httpapi owns transport contracts shared by site controllers.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const MaxGatewayBytes = 2 << 20

type GatewayRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}
type GatewayResponse struct {
	SiteID string          `json:"site_id"`
	Scope  string          `json:"scope,omitempty"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// AllowedGateway is explicit: federation cannot export recovery keys, mint more
// federation credentials, change passwords, or recursively call another site.
func AllowedGateway(method, path, scope string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.Path != strings.ReplaceAll(u.Path, "//", "/") || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "\\") {
		return false
	}
	if method != "GET" && method != "POST" && method != "PUT" && method != "DELETE" {
		return false
	}
	if scope != "manage" {
		return scope == "read" && method == "GET" && (u.Path == "/api/operations" || u.Path == "/api/dashboard")
	}
	for _, p := range []string{"/api/state", "/api/dashboard", "/api/subscription", "/api/node-quality", "/api/ips", "/api/nodes", "/api/users", "/api/import-sources", "/api/public-pool", "/api/tasks", "/api/settings", "/api/runtime-settings", "/api/operations"} {
		if u.Path == p || strings.HasPrefix(u.Path, p+"/") {
			return true
		}
	}
	return false
}
func ValidateEndpoint(address string, allowLocal bool) error {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid site endpoint")
	}
	if u.Scheme != "https" && !(allowLocal && u.Scheme == "http") {
		return errors.New("site endpoint requires HTTPS")
	}
	if u.Opaque != "" || strings.ContainsAny(u.Host, "\r\n\t\\") {
		return errors.New("invalid site host")
	}
	return nil
}
func publicAddress(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(addr) {
			return false
		}
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}
func NewGatewayTransport(allowLocal bool) *http.Transport {
	return &http.Transport{Proxy: nil, MaxConnsPerHost: 4, MaxIdleConns: 8, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 40 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, errors.New("site DNS lookup failed")
			}
			if len(ips) == 0 {
				return nil, errors.New("site DNS has no addresses")
			}
			for _, ip := range ips {
				if !allowLocal && !publicAddress(ip.IP) {
					return nil, errors.New("site endpoint resolved to a non-public address")
				}
			}
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, errors.New("site connection failed")
		},
	}
}
func CallGateway(ctx context.Context, transport http.RoundTripper, endpoint, token string, in GatewayRequest) (GatewayResponse, error) {
	var out GatewayResponse
	b, err := json.Marshal(in)
	if err != nil || len(b) > MaxGatewayBytes {
		return out, errors.New("site request exceeds limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/api/fleet-gateway", bytes.NewReader(b))
	if err != nil {
		return out, errors.New("invalid site endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "guangyue")
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return out, errors.New("site is unreachable or TLS verification failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, errors.New("site rejected the connection credential or gateway request")
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, MaxGatewayBytes+1))
	if err != nil || len(b) > MaxGatewayBytes {
		return out, errors.New("site response exceeds limit")
	}
	if json.Unmarshal(b, &out) != nil || out.SiteID == "" || out.Status < 100 || out.Status > 599 || !json.Valid(out.Body) {
		return out, errors.New("invalid site gateway response")
	}
	return out, nil
}

// BufferResponse adapts existing HTTP handlers to the bounded gateway envelope.
// Response headers (especially Set-Cookie) never cross the federation boundary.
type BufferResponse struct {
	Code     int
	Headers  http.Header
	Body     bytes.Buffer
	Overflow bool
}

func (b *BufferResponse) Header() http.Header {
	if b.Headers == nil {
		b.Headers = http.Header{}
	}
	return b.Headers
}
func (b *BufferResponse) WriteHeader(code int) {
	if b.Code == 0 {
		b.Code = code
	}
}
func (b *BufferResponse) Write(p []byte) (int, error) {
	if b.Code == 0 {
		b.Code = 200
	}
	if b.Body.Len()+len(p) > MaxGatewayBytes/2 {
		b.Overflow = true
		return 0, errors.New("response limit")
	}
	return b.Body.Write(p)
}
