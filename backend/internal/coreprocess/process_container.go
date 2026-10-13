package coreprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const ContainerRuntimeDir = "/run/guangyue-runtime"
const containerExecutable = "/opt/guangyue-personal/bin/guangyue"

// ContainerMode is an explicit deployment choice, never a systemctl-error
// fallback. A native installation therefore keeps its existing service owner.
func ContainerMode() bool { return os.Getenv("GUANGYUE_CONTAINER") == "1" }

type containerGeneration struct {
	PID       int    `json:"pid"`
	StartTime string `json:"start_time"`
	Service   string `json:"service"`
}

var containerBootID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func containerAccountingGeneration(v containerGeneration, boot string) string {
	if v.PID < 2 || v.StartTime == "" || !containerBootID.MatchString(boot) {
		return ""
	}
	return boot + ":" + strconv.Itoa(v.PID) + ":" + v.StartTime
}

type containerProcessTable struct {
	dir, proc, executable string
}

func containerTable() containerProcessTable {
	return containerProcessTable{ContainerRuntimeDir, "/proc", containerExecutable}
}

func containerService(unit string) (string, bool) {
	switch unit {
	case "guangyue.service":
		return "panel", true
	case "guangyue-xray.service":
		return "xray", true
	case "guangyue-hy2.service":
		return "hy2", true
	default:
		return "", false
	}
}

func validContainerService(name string) bool {
	return name == "panel" || name == "xray" || name == "hy2"
}

func privateRuntimePath(path string, directory bool) bool {
	v, err := os.Lstat(path)
	if err != nil || v.Mode()&os.ModeSymlink != 0 || v.IsDir() != directory || v.Mode().Perm()&0077 != 0 {
		return false
	}
	if !directory && (!v.Mode().IsRegular() || v.Size() > 1024) {
		return false
	}
	st, ok := v.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}

// startTime identifies the kernel process generation, including PID reuse. The
// command may contain spaces or parentheses; parse fields after its final ')'.
func processStartTime(stat []byte) (string, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", errors.New("process is unavailable")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return "", errors.New("invalid process generation")
	}
	return fields[19], nil
}

func (t containerProcessTable) inspect(name string, pid int) (containerGeneration, error) {
	if !validContainerService(name) || pid < 2 {
		return containerGeneration{}, errors.New("unknown container service")
	}
	dir := filepath.Join(t.proc, strconv.Itoa(pid))
	info, err := os.Stat(dir)
	if err != nil {
		return containerGeneration{}, errors.New("container service is not running")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return containerGeneration{}, errors.New("container service owner mismatch")
	}
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil || exe != t.executable {
		return containerGeneration{}, errors.New("container service executable mismatch")
	}
	cmd, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || string(cmd) != t.executable+"\x00-run-core\x00"+name+"\x00" {
		return containerGeneration{}, errors.New("container service command mismatch")
	}
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return containerGeneration{}, errors.New("container service generation missing")
	}
	start, err := processStartTime(stat)
	if err != nil {
		return containerGeneration{}, err
	}
	return containerGeneration{PID: pid, StartTime: start, Service: name}, nil
}

func (t containerProcessTable) generation(unit string) (containerGeneration, error) {
	name, ok := containerService(unit)
	if !ok || !privateRuntimePath(t.dir, true) {
		return containerGeneration{}, errors.New("container service record unavailable")
	}
	path := filepath.Join(t.dir, name+".json")
	if !privateRuntimePath(path, false) {
		return containerGeneration{}, errors.New("container service record is not private")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return containerGeneration{}, errors.New("container service record unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
		return containerGeneration{}, errors.New("container service record is not private")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return containerGeneration{}, errors.New("container service record owner mismatch")
	}
	var record containerGeneration
	decoder := json.NewDecoder(io.LimitReader(f, 1025))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || record.Service != name {
		return containerGeneration{}, errors.New("invalid container service record")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return containerGeneration{}, errors.New("invalid container service record")
	}
	current, err := t.inspect(name, record.PID)
	if err != nil || current != record {
		return containerGeneration{}, errors.New("stale container service record")
	}
	return current, nil
}

func containerPID(unit string) int {
	generation, err := containerTable().generation(unit)
	if err != nil {
		return 0
	}
	return generation.PID
}

func restartContainer(unit string) error {
	table := containerTable()
	previous, err := table.generation(unit)
	if err != nil {
		return errors.New("container core is not running; supervisor recovery required")
	}
	if err = signalContainerGeneration(previous, func() (containerGeneration, error) { return table.generation(unit) }); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return waitContainerReplacement(ctx, previous, func() (containerGeneration, error) { return table.generation(unit) })
}

func waitContainerReplacement(ctx context.Context, previous containerGeneration, current func() (containerGeneration, error)) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if v, err := current(); err == nil && v.Service == previous.Service && v.PID > 1 && v != previous {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("container core replacement did not start; supervisor recovery required")
		case <-ticker.C:
		}
	}
}

// RegisterContainerWrapper is called only by the fixed -run-core executable.
// Records live on private ephemeral /run, never in mutable persistent state.
// The cleanup removes only this exact generation, not a newer replacement.
func RegisterContainerWrapper(name string) (func(), error) {
	if !ContainerMode() || !validContainerService(name) {
		return nil, errors.New("invalid container runtime wrapper")
	}
	return containerTable().register(name, os.Getpid())
}

func (table containerProcessTable) register(name string, pid int) (func(), error) {
	if !privateRuntimePath(table.dir, true) {
		return nil, errors.New("container runtime directory must be private and owned by the service user")
	}
	if !validContainerService(name) {
		return nil, errors.New("unknown container service")
	}
	lock, err := os.OpenFile(filepath.Join(table.dir, name+".lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = lock.Close()
		}
	}()
	if !privateRuntimePath(filepath.Join(table.dir, name+".lock"), false) {
		return nil, errors.New("container wrapper lock is not private")
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("container service already has a wrapper")
	}
	v, err := table.inspect(name, pid)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(table.dir, name+".json")
	b, _ := json.Marshal(v)
	f, err := os.CreateTemp(table.dir, ".generation-")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = os.Rename(tmp, path); err != nil {
		return nil, err
	}
	keepLock = true
	return func() {
		defer lock.Close()
		// The process may already have exited when cleanup is checked, so compare
		// the file itself rather than requiring a live /proc identity here.
		raw, e := os.ReadFile(path)
		var saved containerGeneration
		if e == nil && json.Unmarshal(raw, &saved) == nil && saved == v {
			_ = os.Remove(path)
		}
	}, nil
}

func ContainerServiceReady(unit string) bool {
	_, err := containerTable().generation(unit)
	return err == nil
}
