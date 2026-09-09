package controlplane

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// A website's ordinary TLS handshake is not sufficient: REALITY also depends on
// the target's record layout. Probe the deployed, unmodified core with isolated
// credentials and a real inner HTTPS request before admitting a new SNI target.
func probeRealityCore(ctx context.Context, c Config, sni, ip string) error {
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	ports := []int{}
	for range 2 {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return errors.New("SNI 验证监听器不可用")
		}
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
		l.Close()
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	id, sid := uuid(), digest(randomToken(8))[:16]
	users := []object{{"id": id, "flow": "xtls-rprx-vision", "encryption": "none"}}
	doc := object{
		"log": object{"loglevel": "none", "access": "none"},
		"inbounds": []object{
			{"tag": "server", "listen": "127.0.0.1", "port": ports[0], "protocol": "vless", "settings": object{"decryption": "none", "clients": []object{{"id": id, "flow": "xtls-rprx-vision"}}}, "streamSettings": object{"network": "tcp", "security": "reality", "realitySettings": object{"target": net.JoinHostPort(ip, "443"), "serverNames": []string{sni}, "privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "shortIds": []string{sid}}}},
			{"tag": "client", "listen": "127.0.0.1", "port": ports[1], "protocol": "socks", "settings": object{"auth": "noauth"}},
		},
		"outbounds": []object{
			{"tag": "deny", "protocol": "blackhole"},
			{"tag": "internet", "protocol": "freedom", "settings": object{"domainStrategy": "UseIPv4"}},
			{"tag": "test", "protocol": "vless", "settings": object{"vnext": []object{{"address": "127.0.0.1", "port": ports[0], "users": users}}}, "streamSettings": object{"network": "tcp", "security": "reality", "realitySettings": object{"serverName": sni, "fingerprint": "chrome", "publicKey": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "shortId": sid}}},
		},
		"routing": object{"rules": []object{{"type": "field", "inboundTag": []string{"client"}, "outboundTag": "test"}, {"type": "field", "inboundTag": []string{"server"}, "outboundTag": "internet"}}},
	}
	dir, err := os.MkdirTemp(c.StateDir, "reality-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err := writeJSON(path, doc); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.Xray, "run", "-c", path)
	cmd.Env = append(os.Environ(), "GOMEMLIMIT=24MiB", "GOMAXPROCS=1")
	if err := cmd.Start(); err != nil {
		return errors.New("SNI 验证核心启动失败")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[1]))
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("SNI 实际握手验证超时")
		case <-time.After(50 * time.Millisecond):
		}
	}
	dial, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		return err
	}
	transport := &http.Transport{DialContext: dial.(proxy.ContextDialer).DialContext, TLSHandshakeTimeout: 8 * time.Second}
	defer transport.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	response, err := (&http.Client{Transport: transport, Timeout: 12 * time.Second}).Do(request)
	if err == nil {
		defer response.Body.Close()
		body, e := io.ReadAll(io.LimitReader(response.Body, 4096))
		if e == nil && response.StatusCode == 200 && len(body) > 0 {
			return nil
		}
	}
	return errors.New("SNI 未通过实际 REALITY 握手与 HTTPS 验证；目标证书报文或网络可能不兼容，请换用其他域名")
}
