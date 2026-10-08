package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

type emailTransportError struct {
	stage string
	err   error
}

func (e emailTransportError) Error() string { return "SMTP " + e.stage + ": " + e.err.Error() }
func (e emailTransportError) Unwrap() error { return e.err }
func emailFailure(err error) string {
	var transport emailTransportError
	if errors.As(err, &transport) {
		return "SMTP " + transport.stage + "失败，请检查邮件设置和投递服务"
	}
	return "邮件投递失败，请检查邮件设置后重试"
}
func emailWireMessage(v EmailSettings, m emailMessage) ([]byte, error) {
	if strings.ContainsAny(m.Subject+m.ID, "\r\n") {
		return nil, errors.New("invalid message headers")
	}
	if _, err := emailAddress(m.To); err != nil {
		return nil, err
	}
	if _, err := emailAddress(v.FromEmail); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, text string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=UTF-8"}, "Content-Transfer-Encoding": {"base64"}})
		if err != nil {
			return nil, err
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(part.text))
		for len(encoded) > 76 {
			fmt.Fprint(w, encoded[:76]+"\r\n")
			encoded = encoded[76:]
		}
		fmt.Fprint(w, encoded+"\r\n")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	from := (&mail.Address{Name: v.FromName, Address: v.FromEmail}).String()
	domain := strings.SplitN(v.FromEmail, "@", 2)[1]
	fmt.Fprintf(&out, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", from, m.To, mime.QEncoding.Encode("utf-8", m.Subject), time.Now().Format(time.RFC1123Z), m.ID, domain, mw.Boundary())
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// tlsConfig exists only for trusted test roots; certificate/hostname verification
// is mandatory even when custom roots are supplied.
func sendSMTP(ctx context.Context, v EmailSettings, msg emailMessage, tlsConfig *tls.Config) error {
	if !emailReady(v) {
		return errors.New("SMTP is not configured")
	}
	configuration := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: v.Host}
	if tlsConfig != nil {
		configuration = tlsConfig.Clone()
		configuration.ServerName = v.Host
		configuration.MinVersion = tls.VersionTLS12
		if configuration.InsecureSkipVerify {
			return errors.New("TLS certificate verification cannot be disabled")
		}
	}
	data, err := emailWireMessage(v, msg)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", emailEndpoint(v.Host, v.Port))
	if err != nil {
		return emailTransportError{"连接", err}
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if v.TLSMode == "tls" {
		secure := tls.Client(conn, configuration)
		if err = secure.HandshakeContext(ctx); err != nil {
			return emailTransportError{"TLS 验证", err}
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, v.Host)
	if err != nil {
		return emailTransportError{"握手", err}
	}
	defer client.Close()
	if v.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return emailTransportError{"TLS 验证", errors.New("server does not support STARTTLS")}
		}
		if err = client.StartTLS(configuration); err != nil {
			return emailTransportError{"TLS 验证", err}
		}
	}
	if state, ok := client.TLSConnectionState(); !ok || !state.HandshakeComplete {
		return emailTransportError{"TLS 验证", errors.New("secure SMTP connection required")}
	}
	ok, methods := client.Extension("AUTH")
	if !ok {
		return emailTransportError{"认证", errors.New("server does not offer SMTP authentication")}
	}
	var auth smtp.Auth
	if strings.Contains(strings.ToUpper(methods), "PLAIN") {
		auth = smtp.PlainAuth("", v.Username, v.Password, v.Host)
	} else if strings.Contains(strings.ToUpper(methods), "LOGIN") {
		auth = &emailLoginAuth{user: v.Username, password: v.Password}
	} else {
		return emailTransportError{"认证", errors.New("supported authentication unavailable")}
	}
	if err = client.Auth(auth); err != nil {
		return emailTransportError{"认证", err}
	}
	if err = client.Mail(v.FromEmail); err != nil {
		return emailTransportError{"发件人", err}
	}
	if err = client.Rcpt(msg.To); err != nil {
		return emailTransportError{"收件人", err}
	}
	writer, err := client.Data()
	if err != nil {
		return emailTransportError{"传输", err}
	}
	if _, err = writer.Write(data); err != nil {
		return emailTransportError{"传输", err}
	}
	if err = writer.Close(); err != nil {
		return emailTransportError{"接收确认", err}
	}
	// Once DATA has been accepted, a QUIT failure must not cause a duplicate send.
	_ = client.Quit()
	return nil
}

type emailLoginAuth struct {
	user, password string
	step           int
}

func (a *emailLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("TLS required")
	}
	return "LOGIN", nil, nil
}
func (a *emailLoginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	if a.step == 1 {
		return []byte(a.user), nil
	}
	if a.step == 2 {
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected LOGIN challenge")
}
