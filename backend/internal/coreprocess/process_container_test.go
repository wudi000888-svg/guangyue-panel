package coreprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeContainerProcess(t *testing.T, name string, pid int, start string) containerProcessTable {
	t.Helper()
	root := t.TempDir()
	table := containerProcessTable{filepath.Join(root, "run"), filepath.Join(root, "proc"), containerExecutable}
	for _, dir := range []string{table.dir, filepath.Join(table.proc, "123")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(table.proc, "123")
	if pid != 123 {
		t.Fatal("fixture only supports PID 123")
	}
	if err := os.Symlink(table.executable, filepath.Join(proc, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "cmdline"), []byte(table.executable+"\x00-run-core\x00"+name+"\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	stat := "123 (wrapper (with spaces)) S " + strings.Repeat("0 ", 18) + start + " 0 0 0\n"
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte(stat), 0600); err != nil {
		t.Fatal(err)
	}
	return table
}

func TestContainerGenerationRequiresRealWrapperIdentity(t *testing.T) {
	table := fakeContainerProcess(t, "xray", 123, "456")
	cleanup, err := table.register("xray", 123)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	want := containerGeneration{123, "456", "xray"}
	if got, err := table.generation("guangyue-xray.service"); err != nil || got != want {
		t.Fatalf("real generation not recognized: %v %v", got, err)
	}
	for _, unit := range []string{"nginx.service", "ssh.service", "../xray", "xray", "", "guangyue-xray.service --root"} {
		if _, err := table.generation(unit); err == nil {
			t.Fatalf("arbitrary unit accepted: %q", unit)
		}
	}
	path := filepath.Join(table.proc, "123", "cmdline")
	if err := os.WriteFile(path, []byte("/bin/sh\x00-c\x00xray\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := table.generation("guangyue-xray.service"); err == nil {
		t.Fatal("PID record allowed another program")
	}
}

func TestContainerGenerationRejectsUnsafeAndReusedRecords(t *testing.T) {
	for _, change := range []string{"directory permissions", "file permissions", "symlink", "reused pid", "wrong service", "executable", "zombie"} {
		t.Run(change, func(t *testing.T) {
			table := fakeContainerProcess(t, "xray", 123, "456")
			cleanup, err := table.register("xray", 123)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			path := filepath.Join(table.dir, "xray.json")
			switch change {
			case "directory permissions":
				err = os.Chmod(table.dir, 0755)
			case "file permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".real")
				if err == nil {
					err = os.Symlink(path+".real", path)
				}
			case "reused pid":
				err = os.WriteFile(filepath.Join(table.proc, "123", "stat"), []byte("123 (wrapper) S "+strings.Repeat("0 ", 18)+"789 0 0\n"), 0600)
			case "wrong service":
				b, _ := json.Marshal(containerGeneration{123, "456", "hy2"})
				err = os.WriteFile(path, b, 0600)
			case "executable":
				err = os.Remove(filepath.Join(table.proc, "123", "exe"))
				if err == nil {
					err = os.Symlink("/bin/sh", filepath.Join(table.proc, "123", "exe"))
				}
			case "zombie":
				err = os.WriteFile(filepath.Join(table.proc, "123", "stat"), []byte("123 (wrapper) Z "+strings.Repeat("0 ", 18)+"456 0 0\n"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := table.generation("guangyue-xray.service"); err == nil {
				t.Fatal("unsafe or stale process identity accepted")
			}
		})
	}
}

func TestContainerWrapperCleanupPreservesNewGeneration(t *testing.T) {
	table := fakeContainerProcess(t, "xray", 123, "456")
	cleanup, err := table.register("xray", 123)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(table.dir, "xray.json")
	b, _ := json.Marshal(containerGeneration{124, "789", "xray"})
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if got, err := os.ReadFile(path); err != nil || string(got) != string(b) {
		t.Fatal("old wrapper removed a newer generation")
	}
}

func TestContainerServiceAllowsOneWrapperLifetime(t *testing.T) {
	table := fakeContainerProcess(t, "xray", 123, "456")
	cleanup, err := table.register("xray", 123)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := table.register("xray", 123); err == nil {
		second()
		t.Fatal("duplicate wrapper overwrote a live accounting generation")
	}
	cleanup()
	if second, err := table.register("xray", 123); err != nil {
		t.Fatal("stopped wrapper did not release its generation lock")
	} else {
		second()
	}
}

func TestContainerRestartWaitsForVerifiedNewGeneration(t *testing.T) {
	old := containerGeneration{123, "456", "xray"}
	reads := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitContainerReplacement(ctx, old, func() (containerGeneration, error) {
		reads++
		if reads < 3 {
			return old, nil
		}
		// Even a reused PID must be a different verified kernel generation.
		return containerGeneration{123, "789", "xray"}, nil
	})
	if err != nil || reads != 3 {
		t.Fatalf("replacement incorrectly acknowledged: reads=%d error=%v", reads, err)
	}
	for _, current := range []containerGeneration{old, {0, "789", "xray"}, {124, "789", "hy2"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		err := waitContainerReplacement(ctx, old, func() (containerGeneration, error) { return current, nil })
		cancel()
		if err == nil {
			t.Fatal("old, stopped or unrelated service accepted")
		}
	}
}

func TestContainerModeDoesNotFallbackFromNative(t *testing.T) {
	t.Setenv("GUANGYUE_CONTAINER", "")
	if ContainerMode() {
		t.Fatal("native mode unexpectedly uses private PID records")
	}
	t.Setenv("GUANGYUE_CONTAINER", "1")
	if !ContainerMode() || PID("nginx.service") != 0 {
		t.Fatal("container looked outside managed service whitelist")
	}
	for _, name := range []string{"../xray", "/bin/sh", "nginx", "xray --config /tmp/a"} {
		if _, err := RegisterContainerWrapper(name); err == nil {
			t.Fatalf("arbitrary service registration accepted: %q", name)
		}
	}
}

func TestContainerAccountingGenerationSurvivesReusedPIDAndHostReboot(t *testing.T) {
	first := containerGeneration{PID: 6, StartTime: "100", Service: "xray"}
	second := containerGeneration{PID: 6, StartTime: "200", Service: "xray"}
	boot := "00000000-0000-0000-0000-000000000001"
	firstID, secondID := containerAccountingGeneration(first, boot), containerAccountingGeneration(second, boot)
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatal("container restart with the same PID reused an accounting generation")
	}
	if firstID == containerAccountingGeneration(first, "00000000-0000-0000-0000-000000000002") {
		t.Fatal("host reboot reused the old process accounting generation")
	}
	if containerAccountingGeneration(first, "") != "" || containerAccountingGeneration(first, "not-a-boot-id") != "" {
		t.Fatal("unverified kernel generation accepted")
	}
}
