package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/ingressgateway"
)

func run() error {
	configFile := flag.String("config", "/etc/guangyue-gateway.json", "root-managed gateway JSON configuration")
	check := flag.Bool("check", false, "validate configuration without network or filesystem mutations")
	wait := flag.Duration("wait-ready", 0, "wait for a live gateway and published certificate, without issuing certificates")
	flag.Parse()
	if flag.NArg() != 0 || *wait < 0 || *wait > 10*time.Minute || *check && *wait != 0 {
		return fmt.Errorf("invalid gateway arguments")
	}
	config, err := ingressgateway.LoadConfig(*configFile)
	if err != nil {
		return err
	}
	if *check {
		fmt.Println("Gateway configuration valid; public entry TCP 443, no host Nginx dependency")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *wait > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, *wait)
		defer cancel()
		ready, err := ingressgateway.WaitReady(waitCtx, config)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(ready)
	}
	// Bind before constructing persistent state or contacting the CA. Never
	// stop any competing process or fall back to a different public port.
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return fmt.Errorf("TCP 443 unavailable; gateway did not replace any service")
	}
	defer listener.Close()
	server, err := ingressgateway.New(config)
	if err != nil {
		return err
	}
	fmt.Println("Gateway listening on TCP 443; certificate readiness is recorded locally")
	return server.Serve(ctx, listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Gateway: "+err.Error())
		os.Exit(1)
	}
}
