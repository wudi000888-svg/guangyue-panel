package ingressgateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/net/http2"
)

const maxConnections = 256
const maxPendingHellos = 64
const helloTimeout = 5 * time.Second
const relayIdleTimeout = 5 * time.Minute

type replayConn struct {
	net.Conn
	reader io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type leasedConn struct {
	net.Conn
	onClose func()
	once    sync.Once
}

func (c *leasedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.onClose); return err }

type dispatchListener struct {
	addr  net.Addr
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *dispatchListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case conn := <-l.conns:
		return conn, nil
	}
}
func (l *dispatchListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *dispatchListener) Addr() net.Addr { return l.addr }

type Server struct {
	config       Config
	certificates *certificateStore
	http         *http.Server
	transport    *http.Transport
}

func New(config Config) (*Server, error) {
	config.Defaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	certs, err := newCertificateStore(config)
	if err != nil {
		return nil, err
	}
	upstream, _ := url.Parse(config.PanelUpstream)
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConns: 64, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 128, IdleConnTimeout: 60 * time.Second,
		ResponseHeaderTimeout: 100 * time.Second, DisableCompression: true}
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(upstream)
			p.Out.Host = p.In.Host
			// ReverseProxy's Rewrite already removes client-supplied X-Forwarded
			// headers. Also remove Forwarded and replace X-Real-IP explicitly.
			p.Out.Header.Del("Forwarded")
			p.Out.Header.Del("X-Real-IP")
			p.Out.Header.Del("X-Forwarded-For")
			p.Out.Header.Del("X-Forwarded-Host")
			p.Out.Header.Del("X-Forwarded-Proto")
			host, _, _ := net.SplitHostPort(p.In.RemoteAddr)
			if ip := net.ParseIP(host); ip != nil {
				p.Out.Header.Set("X-Real-IP", ip.String())
				p.Out.Header.Set("X-Forwarded-For", ip.String())
			}
			p.Out.Header.Set("X-Forwarded-Proto", "https")
			p.Out.Header.Set("X-Forwarded-Host", p.In.Host)
		}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Panel temporarily unavailable", http.StatusBadGateway)
		}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, p, err := net.SplitHostPort(host); err == nil {
			if p != "443" {
				http.Error(w, "Unrecognized host", 421)
				return
			}
			host = h
		}
		if !config.owns(host) || r.TLS == nil || strings.ToLower(r.TLS.ServerName) != host {
			http.Error(w, "Unrecognized host", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if maintenancePending() && !allowDuringMaintenance(r.Method, r.URL.Path) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "面板正在更新，请稍后重试", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	httpServer := &http.Server{Handler: handler, TLSConfig: certs.tlsConfig(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	if err := http2.ConfigureServer(httpServer, &http2.Server{MaxConcurrentStreams: 32, MaxReadFrameSize: 16384, IdleTimeout: 60 * time.Second}); err != nil {
		return nil, err
	}
	return &Server{config: config, certificates: certs, http: httpServer, transport: transport}, nil
}

// Serve accepts an injected listener for tests; the public CLI only binds :443.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer listener.Close()
	dispatch := &dispatchListener{addr: listener.Addr(), conns: make(chan net.Conn), done: make(chan struct{})}
	defer dispatch.Close()
	defer s.transport.CloseIdleConnections()
	var connMu sync.Mutex
	conns := map[*leasedConn]struct{}{}
	total := make(chan struct{}, maxConnections)
	hellos := make(chan struct{}, maxPendingHellos)
	var workers sync.WaitGroup
	httpDone := make(chan error, 1)
	go func() { httpDone <- s.http.Serve(dispatch); cancel(); _ = listener.Close() }()
	certDone := make(chan struct{})
	go func() { s.certificates.run(ctx); close(certDone) }()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		_ = dispatch.Close()
		_ = s.http.Close()
		connMu.Lock()
		pending := make([]*leasedConn, 0, len(conns))
		for c := range conns {
			pending = append(pending, c)
		}
		connMu.Unlock()
		for _, c := range pending {
			_ = c.Close()
		}
	}()
	var result error
	for {
		raw, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				result = err
			}
			break
		}
		select {
		case total <- struct{}{}:
		default:
			_ = raw.Close()
			continue
		}
		select {
		case hellos <- struct{}{}:
		default:
			<-total
			_ = raw.Close()
			continue
		}
		conn := &leasedConn{Conn: raw}
		conn.onClose = func() { connMu.Lock(); delete(conns, conn); connMu.Unlock(); <-total }
		connMu.Lock()
		conns[conn] = struct{}{}
		connMu.Unlock()
		workers.Add(1)
		go func(c *leasedConn) {
			defer workers.Done()
			// The connection may have arrived just as shutdown's connection
			// snapshot was taken. Cancellation must still unblock its first read.
			stopClose := context.AfterFunc(ctx, func() { _ = c.Close() })
			defer stopClose()
			_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
			hello, err := readClientHello(c)
			<-hellos
			if err != nil {
				_ = c.Close()
				return
			}
			_ = c.SetDeadline(time.Time{})
			replay := &replayConn{Conn: c, reader: io.MultiReader(bytes.NewReader(hello.raw), c)}
			if s.config.owns(hello.serverName) {
				tlsConn := tls.Server(replay, s.http.TLSConfig)
				if len(hello.protocols) == 1 && hello.protocols[0] == acme.ALPNProto {
					defer tlsConn.Close()
					handshakeCtx, stop := context.WithTimeout(ctx, helloTimeout)
					defer stop()
					_ = tlsConn.HandshakeContext(handshakeCtx)
					return
				}
				select {
				case dispatch.conns <- tlsConn:
				case <-ctx.Done():
					_ = tlsConn.Close()
				}
				return
			}
			// ACME is never answered or forwarded for third-party Reality SNI.
			for _, p := range hello.protocols {
				if p == acme.ALPNProto {
					_ = c.Close()
					return
				}
			}
			s.relay(ctx, replay, hello.serverName)
		}(conn)
	}
	cancel()
	workers.Wait()
	_ = s.http.Close()
	<-httpDone
	<-certDone
	return result
}

func (s *Server) realityTarget(name string) (string, string, error) {
	if !validDomain(name) || s.config.owns(name) {
		return "", "", errors.New("invalid Reality SNI")
	}
	if name == s.config.RealitySNI {
		return "tcp", s.config.RealityUpstream, nil
	}
	path := filepath.Join(s.config.RealitySocketDir, name+".sock")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("unrecognized Reality SNI")
	}
	return "unix", path, nil
}

func proxyHeader(conn net.Conn) ([]byte, error) {
	src, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, errors.New("missing peer TCP address")
	}
	dst, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return nil, errors.New("missing local TCP address")
	}
	proto := "TCP6"
	if src.IP.To4() != nil && dst.IP.To4() != nil {
		proto = "TCP4"
	} else if (src.IP.To4() == nil) != (dst.IP.To4() == nil) {
		return nil, errors.New("peer address family mismatch")
	}
	if src.IP == nil || dst.IP == nil || src.Port < 1 || dst.Port < 1 {
		return nil, errors.New("invalid TCP address")
	}
	return []byte(fmt.Sprintf("PROXY %s %s %s %d %d\r\n", proto, src.IP.String(), dst.IP.String(), src.Port, dst.Port)), nil
}

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(relayIdleTimeout))
	return c.Conn.Read(p)
}
func (c idleConn) Write(p []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(relayIdleTimeout))
	return c.Conn.Write(p)
}

func (s *Server) relay(ctx context.Context, client net.Conn, name string) {
	defer client.Close()
	if maintenancePending() {
		return
	}
	network, address, err := s.realityTarget(name)
	if err != nil {
		return
	}
	header, err := proxyHeader(client)
	if err != nil {
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	backend, err := (&net.Dialer{}).DialContext(dialCtx, network, address)
	if err != nil {
		return
	}
	defer backend.Close()
	_ = backend.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err = backend.Write(header); err != nil {
		return
	}
	_ = backend.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(idleConn{client}, idleConn{backend})
		_ = client.Close()
		_ = backend.Close()
		close(done)
	}()
	_, _ = io.Copy(idleConn{backend}, idleConn{client})
	_ = backend.Close()
	_ = client.Close()
	<-done
}
