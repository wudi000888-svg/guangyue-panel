// Synthetic loopback Reality target for disposable native installation CI.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("GY_ONECLICK_INTEGRATION") != "1" {
		os.Stderr.WriteString("Disposable native CI fixture only\n")
		os.Exit(2)
	}
	state := flag.String("state", "", "private synthetic fixture directory")
	flag.Parse()
	if *state == "" {
		panic("private fixture directory required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "www.cloudflare.com"}, DNSNames: []string{"www.cloudflare.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	if err = os.WriteFile(filepath.Join(*state, "cert.pem"), certificate, 0600); err != nil {
		panic(err)
	}
	if err = os.WriteFile(filepath.Join(*state, "key.pem"), private, 0600); err != nil {
		panic(err)
	}
	pair, err := tls.X509KeyPair(certificate, private)
	if err != nil {
		panic(err)
	}
	// Neither the bind address nor a production target is caller selectable.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	configuration := &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}, CurvePreferences: []tls.CurveID{tls.X25519}}
	server := &http.Server{TLSConfig: configuration, ReadHeaderTimeout: 4 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("synthetic native CI Reality target")) })}
	json.NewEncoder(os.Stdout).Encode(map[string]string{"address": listener.Addr().String()})
	if err = server.ServeTLS(listener, "", ""); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
