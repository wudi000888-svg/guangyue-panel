package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type smtpReadWriter struct {
	*textproto.Reader
	*textproto.Writer
}

func newSMTPReadWriter(conn net.Conn) *smtpReadWriter {
	return &smtpReadWriter{textproto.NewReader(bufio.NewReader(conn)), textproto.NewWriter(bufio.NewWriter(conn))}
}

type smtpFixture struct {
	settings     EmailSettings
	roots        *x509.CertPool
	messages     chan []byte
	auth         atomic.Int32
	insecureAuth atomic.Bool
}

func newSMTPFixture(t *testing.T, mode string, offerTLS, rejectAuth bool) *smtpFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.fixture.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	f := &smtpFixture{settings: EmailSettings{Enabled: true, Host: "127.0.0.1", Port: portNumber, TLSMode: mode, Username: "fixture-user", Password: "fixture-secret", FromEmail: "sender@example.test", FromName: "测试发件人"}, roots: roots, messages: make(chan []byte, 10)}
	var mu sync.Mutex
	var connections []net.Conn
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, raw)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
				var conn net.Conn = raw
				secure := false
				if mode == "tls" {
					tlsConn := tls.Server(raw, serverTLS)
					if tlsConn.Handshake() != nil {
						return
					}
					conn = tlsConn
					secure = true
				}
				rw := newSMTPReadWriter(conn)
				_ = rw.PrintfLine("220 fixture SMTP")
				for {
					line, err := rw.ReadLine()
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) == 0 {
						return
					}
					command := strings.ToUpper(fields[0])
					switch command {
					case "EHLO", "HELO":
						_ = rw.PrintfLine("250-fixture")
						if offerTLS && !secure {
							_ = rw.PrintfLine("250-STARTTLS")
						}
						_ = rw.PrintfLine("250 AUTH PLAIN LOGIN")
					case "STARTTLS":
						if !offerTLS {
							_ = rw.PrintfLine("502 unavailable")
							continue
						}
						_ = rw.PrintfLine("220 ready")
						tlsConn := tls.Server(conn, serverTLS)
						if tlsConn.Handshake() != nil {
							return
						}
						conn = tlsConn
						secure = true
						rw = newSMTPReadWriter(conn)
					case "AUTH":
						f.auth.Add(1)
						if !secure {
							f.insecureAuth.Store(true)
						}
						if rejectAuth {
							_ = rw.PrintfLine("535 fixture-secret rejected")
							continue
						}
						if len(fields) != 3 || fields[1] != "PLAIN" {
							_ = rw.PrintfLine("504 unsupported")
							continue
						}
						decoded, _ := base64.StdEncoding.DecodeString(fields[2])
						if !bytes.Equal(decoded, []byte("\x00fixture-user\x00fixture-secret")) {
							_ = rw.PrintfLine("535 invalid")
							continue
						}
						_ = rw.PrintfLine("235 authenticated")
					case "MAIL", "RCPT", "RSET":
						_ = rw.PrintfLine("250 OK")
					case "DATA":
						_ = rw.PrintfLine("354 send body")
						body, err := rw.ReadDotBytes()
						if err != nil {
							return
						}
						f.messages <- body
						_ = rw.PrintfLine("250 queued")
					case "QUIT":
						_ = rw.PrintfLine("221 bye")
						return
					default:
						_ = rw.PrintfLine("502 unsupported")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return f
}
func TestEmailSMTPTLSAndMIME(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			f := newSMTPFixture(t, mode, true, false)
			msg, err := buildEmail("stable-message-id", "receiver@example.test", "邮件验证", "<script>alert('bad')</script>", "https://panel.test/#/email/verify?token=fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err = sendSMTP(context.Background(), f.settings, msg, &tls.Config{RootCAs: f.roots}); err != nil {
				t.Fatal(err)
			}
			if f.auth.Load() != 1 || f.insecureAuth.Load() {
				t.Fatal("SMTP auth was absent or sent without TLS")
			}
			body := <-f.messages
			mailMessage, err := mail.ReadMessage(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if mailMessage.Header.Get("Message-Id") != "<stable-message-id@example.test>" {
				t.Fatal("retry message ID not stable")
			}
			_, params, err := mime.ParseMediaType(mailMessage.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			reader := multipart.NewReader(mailMessage.Body, params["boundary"])
			parts := 0
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
				if err != nil {
					t.Fatal(err)
				}
				parts++
				if strings.Contains(part.Header.Get("Content-Type"), "text/html") {
					if strings.Contains(string(decoded), "<script>") || !strings.Contains(string(decoded), "&lt;script&gt;") {
						t.Fatal("HTML template did not escape text")
					}
				}
			}
			if parts != 2 {
				t.Fatal("plain and HTML alternatives required")
			}
		})
	}
}
func TestEmailSMTPRejectsUntrustedTLSAndPlainAuthentication(t *testing.T) {
	for _, test := range []struct {
		name, mode      string
		offerTLS, trust bool
	}{{"implicit-untrusted", "tls", true, false}, {"starttls-untrusted", "starttls", true, false}, {"missing-starttls", "starttls", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			f := newSMTPFixture(t, test.mode, test.offerTLS, false)
			msg, _ := buildEmail("test", "receiver@example.test", "Test", "Test", "")
			var config *tls.Config
			if test.trust {
				config = &tls.Config{RootCAs: f.roots}
			}
			if err := sendSMTP(context.Background(), f.settings, msg, config); err == nil {
				t.Fatal("unsafe SMTP delivery accepted")
			}
			if f.auth.Load() != 0 {
				t.Fatal("credentials sent before valid TLS")
			}
		})
	}
	f := newSMTPFixture(t, "tls", true, false)
	msg, _ := buildEmail("test", "receiver@example.test", "Test", "Test", "")
	if sendSMTP(context.Background(), f.settings, msg, &tls.Config{InsecureSkipVerify: true}) == nil {
		t.Fatal("TLS verification bypass accepted")
	}
	wrongHost := f.settings
	wrongHost.Host = "localhost"
	var hostnameError x509.HostnameError
	if err := sendSMTP(context.Background(), wrongHost, msg, &tls.Config{RootCAs: f.roots}); !errors.As(err, &hostnameError) {
		t.Fatalf("expected SMTP hostname verification failure, got %v", err)
	}
	if f.auth.Load() != 0 {
		t.Fatal("credentials exposed before hostname verification")
	}
}
func TestEmailSMTPAuthenticationFailureIsRedacted(t *testing.T) {
	f := newSMTPFixture(t, "starttls", true, true)
	msg, _ := buildEmail("test", "receiver@example.test", "Test", "Test", "")
	err := sendSMTP(context.Background(), f.settings, msg, &tls.Config{RootCAs: f.roots})
	if err == nil {
		t.Fatal("bad SMTP auth accepted")
	}
	if strings.Contains(emailFailure(err), f.settings.Password) {
		t.Fatal("SMTP credentials leaked in delivery status")
	}
	if len(f.messages) != 0 {
		t.Fatal("message sent after auth rejection")
	}
}
