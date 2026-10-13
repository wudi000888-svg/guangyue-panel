package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestContainerSupervisorCommandWhitelist(t *testing.T) {
	t.Setenv("GUANGYUE_CONTAINER", "untrusted")
	t.Setenv("GUANGYUE_LOG_WRAPPER", "untrusted")
	for _, name := range containerManagedServices {
		cmd, err := containerWrapperCommand(name)
		if err != nil || len(cmd.Args) != 3 || cmd.Args[0] != "/opt/guangyue-personal/bin/guangyue" || cmd.Args[1] != "-run-core" || cmd.Args[2] != name {
			t.Fatal("supervisor changed the fixed wrapper command")
		}
		if !strings.Contains(strings.Join(cmd.Env, "\n"), "GUANGYUE_CONTAINER=1") || strings.Contains(strings.Join(cmd.Env, "\n"), "GUANGYUE_LOG_WRAPPER=") {
			t.Fatal("wrapper deployment environment was not isolated")
		}
	}
	for _, name := range []string{"sh", "../../sh", "xray --config /tmp/a", "nginx", ""} {
		if _, err := containerWrapperCommand(name); err == nil {
			t.Fatalf("arbitrary command accepted: %q", name)
		}
	}
}

func TestContainerRuntimeDirectoryIsPrivateAndExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	lock, err := containerRuntimeLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := containerRuntimeLock(dir); err == nil {
		second.Close()
		t.Fatal("two supervisors obtained the same directory")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if second, err := containerRuntimeLock(dir); err == nil {
		second.Close()
		t.Fatal("public runtime directory was accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if second, err := containerRuntimeLock(link); err == nil {
		second.Close()
		t.Fatal("symlinked runtime directory was accepted")
	}
}

func TestContainerSupervisorProcessFixture(t *testing.T) {
	name := os.Getenv("GUANGYUE_SUPERVISOR_FIXTURE")
	if name == "" {
		return
	}
	dir := os.Getenv("GUANGYUE_SUPERVISOR_FIXTURE_DIR")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	child := exec.Command("/bin/sleep", "60")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	b, _ := json.Marshal(struct{ PID, Child int }{os.Getpid(), child.Process.Pid})
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0600); err != nil {
		os.Exit(2)
	}
	if name == "hy2" {
		// Verify that the supervisor also bounds an unresponsive wrapper.
		stop()
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	}
	<-ctx.Done()
	_ = child.Process.Signal(syscall.SIGTERM)
	_ = child.Wait()
	os.Exit(0)
}

func TestContainerSupervisorRecoversOnlyCrashedServiceAndStopsAllGroups(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	started := map[string][]int{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := containerSupervisor{
		command: func(name string) (*exec.Cmd, error) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestContainerSupervisorProcessFixture$")
			cmd.Env = append(os.Environ(), "GUANGYUE_SUPERVISOR_FIXTURE="+name, "GUANGYUE_SUPERVISOR_FIXTURE_DIR="+dir)
			return cmd, nil
		},
		retry: 20 * time.Millisecond, stopGrace: 100 * time.Millisecond,
		started: func(name string, pid int) { mu.Lock(); started[name] = append(started[name], pid); mu.Unlock() },
	}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	type processes struct{ PID, Child int }
	read := func(name string) (processes, bool) {
		var v processes
		b, err := os.ReadFile(filepath.Join(dir, name+".json"))
		ok := err == nil && json.Unmarshal(b, &v) == nil && v.PID > 1 && v.Child > 1
		return v, ok
	}
	deadline := time.Now().Add(5 * time.Second)
	original := map[string]processes{}
	for len(original) < 3 && time.Now().Before(deadline) {
		for _, name := range containerManagedServices {
			if p, ok := read(name); ok {
				original[name] = p
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(original) != 3 {
		t.Fatal("fixed services did not start")
	}
	if err := syscall.Kill(original["xray"].PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var replacement processes
	for time.Now().Before(deadline) {
		p, ok := read("xray")
		if ok && p.PID != original["xray"].PID {
			replacement = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if replacement.PID < 2 {
		t.Fatal("crashed core did not receive a new wrapper generation")
	}
	mu.Lock()
	unchanged := len(started["panel"]) == 1 && len(started["hy2"]) == 1 && len(started["xray"]) == 2
	mu.Unlock()
	if !unchanged {
		t.Fatal("recovery restarted another service or reused the old generation")
	}
	stopAt := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("supervisor shutdown was not bounded")
	}
	if time.Since(stopAt) > 3*time.Second {
		t.Fatal("unresponsive wrapper delayed shutdown")
	}
	for _, p := range []processes{original["panel"], original["hy2"], original["xray"], replacement} {
		if err := syscall.Kill(p.PID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatal("supervisor did not reap a managed wrapper")
		}
		// A killed orphan may be a zombie until the container init reaps it.
		// It must never remain as a live sleep process.
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(p.Child) + "/stat"); err == nil {
			pos := strings.LastIndex(string(b), ")")
			if pos >= 0 && !strings.HasPrefix(strings.TrimSpace(string(b)[pos+1:]), "Z ") {
				t.Fatal("shutdown left a live core descendant")
			}
		}
	}
}

func writeContainerTestCertificate(t *testing.T, cert, key string, serial int64) {
	writeContainerTestCertificateValidity(t, cert, key, serial, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

func writeContainerTestCertificateValidity(t *testing.T, cert, key string, serial int64, before, after time.Time) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "test.invalid"}, NotBefore: before, NotAfter: after, KeyUsage: x509.KeyUsageDigitalSignature, DNSNames: []string{"test.invalid"}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestContainerCertificateRenewalReloadsHY2OnlyAndRetriesFailure(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeContainerTestCertificate(t, cert, key, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restarts := make(chan int, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		calls := 0
		watchContainerCertificate(ctx, cert, key, 10*time.Millisecond, func() error {
			calls++
			restarts <- calls
			if calls == 1 {
				return errors.New("replacement not ready")
			}
			return nil
		})
	}()
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(cert, []byte("partial renewal"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case <-restarts:
		t.Fatal("partial certificate triggered a restart")
	default:
	}
	writeContainerTestCertificate(t, cert, key, 2)
	for want := 1; want <= 2; want++ {
		select {
		case got := <-restarts:
			if got != want {
				t.Fatal("certificate replacement restart was not retried")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("valid renewed certificate did not trigger HY2 restart")
		}
	}
	time.Sleep(40 * time.Millisecond)
	select {
	case <-restarts:
		t.Fatal("unchanged valid certificate repeatedly restarted HY2")
	default:
	}
	cancel()
	<-done
}
