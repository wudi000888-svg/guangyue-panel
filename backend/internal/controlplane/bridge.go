package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type bridgeProcess struct {
	cmd  *exec.Cmd
	done chan error
	hash string
}

func upstreamKey(p object) string { b, _ := json.Marshal(p); return string(b) }
func effectiveExit(n Node) Node {
	if n.Exit == "subscription" {
		n.Exit = "socks5"
		n.Host = "127.0.0.1"
		n.Port = n.BridgePort
		n.Username = "guangyue"
		n.Password = n.BridgePassword
	}
	return n
}

// A single lazy shared process serves all imported resources. Each loopback-only
// authenticated listener is pinned to one outbound; unmatched traffic is rejected.
func bridgeConfig(pools []IPResource, secret string) object {
	proxies, listeners := []object{}, []object{}
	for _, p := range pools {
		if p.Exit != "subscription" || !p.Enabled {
			continue
		}
		outbound := object{}
		for k, v := range p.Upstream {
			outbound[k] = v
		}
		outbound["name"] = p.ID
		proxies = append(proxies, outbound)
		listeners = append(listeners, object{"name": p.ID, "type": "socks", "listen": "127.0.0.1", "port": p.BridgePort, "udp": p.Upstream["udp"] != false, "users": []object{{"username": "guangyue", "password": p.BridgePassword}}, "proxy": p.ID})
	}
	return object{"mode": "rule", "ipv6": false, "log-level": "silent", "external-controller": "127.0.0.1:19190", "secret": secret, "listeners": listeners, "proxies": proxies, "rules": []string{"MATCH,REJECT"}, "profile": object{"store-selected": false, "store-fake-ip": false}, "dns": object{"enable": false}}
}
func (a *App) bridgeSecret() string { return digest("bridge/v1/" + a.cfg.StatsSecret) }
func (a *App) validateBridge(pools []IPResource) error {
	if a.cfg.Dev {
		return nil
	}
	path := filepath.Join(a.cfg.StateDir, "candidate-bridge.json")
	if err := writeJSON(path, bridgeConfig(pools, a.bridgeSecret())); err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := command(12*time.Second, a.cfg.Mihomo, "-t", "-d", a.cfg.StateDir, "-f", path); err != nil {
		return errors.New("订阅节点未通过出口核心校验，请检查协议参数；未写入 IP 池")
	}
	return nil
}
func (a *App) stopBridgeLocked() {
	if a.bridge == nil {
		return
	}
	_ = a.bridge.cmd.Process.Kill()
	<-a.bridge.done
	a.bridge = nil
}
func (a *App) stopBridge() { a.bridgeMu.Lock(); defer a.bridgeMu.Unlock(); a.stopBridgeLocked() }
func (a *App) ensureBridge() error {
	if a.cfg.Dev {
		return nil
	}
	pools, err := a.store.pools()
	if err != nil {
		return err
	}
	cfg := bridgeConfig(pools, a.bridgeSecret())
	b, _ := json.Marshal(cfg)
	hash := digest(string(b))
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	if a.bridge != nil {
		select {
		case <-a.bridge.done:
			a.bridge = nil
		default:
		}
	}
	if len(cfg["listeners"].([]object)) == 0 {
		a.stopBridgeLocked()
		return nil
	}
	if a.bridge != nil && a.bridge.hash == hash {
		return nil
	}
	if err = a.validateBridge(pools); err != nil {
		return err
	}
	path := filepath.Join(a.cfg.StateDir, "bridge.json")
	if err = atomicWrite(path, b, 0600); err != nil {
		return err
	}
	if a.bridge != nil {
		a.bridge.hash = ""
		payload, _ := json.Marshal(object{"payload": string(b)})
		req, _ := http.NewRequest("PUT", "http://127.0.0.1:19190/configs?force=true", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+a.bridgeSecret())
		req.Header.Set("Content-Type", "application/json")
		c := http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
		res, e := c.Do(req)
		if e == nil {
			res.Body.Close()
			if res.StatusCode != 204 {
				e = errors.New("reload rejected")
			}
		}
		if e != nil {
			return errors.New("机场出口核心重载失败")
		}
	} else {
		cmd := exec.Command(a.cfg.Mihomo, "-d", a.cfg.StateDir, "-f", path)
		cmd.Env = append(os.Environ(), "GOMEMLIMIT=64MiB", "GOMAXPROCS=2")
		if err = cmd.Start(); err != nil {
			return errors.New("机场出口核心启动失败")
		}
		p := &bridgeProcess{cmd: cmd, done: make(chan error, 1)}
		a.bridge = p
		go func() { p.done <- cmd.Wait() }()
	}
	for _, p := range pools {
		if p.Exit == "subscription" && p.Enabled {
			if err = waitPort(net.JoinHostPort("127.0.0.1", strconv.Itoa(p.BridgePort))); err != nil {
				return errors.New("机场出口监听器未就绪")
			}
		}
	}
	a.bridge.hash = hash
	return nil
}
