package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type mountLeases struct {
	Version   int              `json:"version"`
	Users     map[string]int64 `json:"users"`
	Revisions map[string]int64 `json:"revisions,omitempty"`
}

// The core wrapper can expire delegated users even if the panel is stopped.
// It reads only public lease metadata; no database or federation secrets.
func (a *App) writeMountLeases() error {
	records, err := a.coreRecords()
	if err != nil {
		return err
	}
	out := mountLeases{Version: 1, Users: map[string]int64{}, Revisions: map[string]int64{}}
	for _, r := range records {
		if r.Mount == nil {
			continue
		}
		deadline := r.Mount.LeaseUntil
		if r.Expires > 0 {
			deadline = min(deadline, r.Expires)
		}
		if (!r.Active() || len(r.Mount.NodeIDs) == 0) && deadline > time.Now().Unix() {
			deadline = 0
		}
		out.Users[businessUsageKey(r.ID)] = deadline
		out.Revisions[businessUsageKey(r.ID)] = r.Mount.Revision
	}
	path := filepath.Join(a.cfg.StateDir, "mount-leases.json")
	next, _ := json.MarshalIndent(out, "", "  ")
	previous, _ := os.ReadFile(path)
	if bytes.Equal(bytes.TrimSpace(next), bytes.TrimSpace(previous)) {
		return nil
	}
	return writeJSON(path, out)
}
func readMountLeases(dir string) (mountLeases, error) {
	v := mountLeases{}
	b, err := os.ReadFile(filepath.Join(dir, "mount-leases.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mountLeases{Version: 1, Users: map[string]int64{}}, nil
	}
	if err != nil {
		return v, err
	}
	if len(b) > 1<<20 || json.Unmarshal(b, &v) != nil || v.Version != 1 {
		return v, errors.New("invalid mount lease metadata")
	}
	return v, nil
}
func filterExpiredMountClients(config []byte, leases mountLeases, now int64) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(config, &root); err != nil {
		return nil, err
	}
	inbound, _ := root["inbounds"].([]any)
	for _, entry := range inbound {
		n, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		settings, _ := n["settings"].(map[string]any)
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		keep := []any{}
		for _, entry := range clients {
			client, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("invalid core client")
			}
			email, _ := client["email"].(string)
			user, _, _ := strings.Cut(strings.TrimPrefix(email, "u"), ".")
			if deadline, mounted := leases.Users[user]; mounted && deadline <= now {
				continue
			}
			keep = append(keep, client)
		}
		settings["clients"] = keep
	}
	return json.Marshal(root)
}
func prepareMountedCore(name, dir string, args []string) ([]string, func(), error) {
	leases, err := readMountLeases(dir)
	if err != nil {
		return nil, nil, err
	}
	if name != "xray" || len(leases.Users) == 0 {
		return args, func() {}, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "xray.json"))
	if err != nil {
		return nil, nil, err
	}
	b, err = filterExpiredMountClients(b, leases, time.Now().Unix())
	if err != nil {
		return nil, nil, err
	}
	file, err := os.CreateTemp("", "guangyue-mounted-xray-*.json")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if _, err = file.Write(b); err != nil {
		file.Close()
		cleanup()
		return nil, nil, err
	}
	if err = file.Close(); err != nil {
		cleanup()
		return nil, nil, err
	}
	out := append([]string{}, args...)
	for i := 1; i < len(out); i++ {
		if out[i-1] == "-config" {
			out[i] = file.Name()
		}
	}
	return out, cleanup, nil
}
func guardMountLeases(ctx context.Context, dir string, active map[string]bool, previous mountLeases, cancel context.CancelFunc) {
	revokedAt := map[string]int64{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	check := func(initial bool) bool {
		v, err := readMountLeases(dir)
		if err != nil {
			return len(active) == 0
		}
		now := time.Now().Unix()
		for id, deadline := range v.Users {
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				return false
			}
			old, known := previous.Users[id]
			// A grant and its withdrawal can both happen between two polls.
			// The retained grant revision proves this process may have admitted
			// that identity even when its current deadline is already zero.
			if deadline > now || !known || old != deadline || previous.Revisions[id] != v.Revisions[id] {
				active[id] = true
			}
		}
		previous = v
		if !initial {
			for id := range active {
				deadline := v.Users[id]
				if deadline > now {
					delete(revokedAt, id)
					continue
				}
				if deadline > 0 && deadline <= now-6 {
					return false
				}
				if _, ok := revokedAt[id]; !ok {
					revokedAt[id] = now
				}
				if now-revokedAt[id] >= 6 {
					return false
				}
			}
		}
		return true
	}
	if !check(true) {
		cancel()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !check(false) {
				cancel()
				return
			}
		}
	}
}
