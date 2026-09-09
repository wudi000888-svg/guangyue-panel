package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type realityUserState struct {
	Inbound          object
	Tag              string
	Desired, Current map[string]string
	Changed          bool
}

func (a *App) xrayUsers(tag string) (map[string]string, error) {
	b, err := a.xapi("inbounduser", "-tag="+tag)
	if err != nil {
		return nil, errors.New("Xray user API unavailable")
	}
	var actual struct {
		Users []struct {
			Email   string `json:"email"`
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		} `json:"users"`
	}
	if err = json.Unmarshal(b, &actual); err != nil {
		return nil, errors.New("invalid Xray user API response")
	}
	result := map[string]string{}
	for _, user := range actual.Users {
		if _, exists := result[user.Email]; exists {
			return nil, errors.New("duplicate Xray user API response")
		}
		result[user.Email] = user.Account.ID
	}
	return result, nil
}

// Credentials belong to exactly one SNI group, while their email-based routing
// and statistics retain the existing user/node identity across SNI changes.
func (a *App) reconcileRealityUsers(xc object, configPath, nodeHash string) error {
	states := []realityUserState{}
	desiredGroups := map[string]map[string]string{}
	changed := false
	for _, inbound := range xc["inbounds"].([]object) {
		if inbound["protocol"] != "vless" {
			continue
		}
		tag := inbound["tag"].(string)
		desired := map[string]string{}
		for _, client := range inbound["settings"].(object)["clients"].([]object) {
			desired[client["email"].(string)] = client["id"].(string)
		}
		current, err := a.xrayUsers(tag)
		if err != nil {
			return err
		}
		state := realityUserState{Inbound: inbound, Tag: tag, Desired: desired, Current: current}
		for email, id := range current {
			if desired[email] != id {
				if _, err = a.xapi("rmu", "-tag="+tag, email); err != nil {
					return errors.New("Xray user revocation failed")
				}
				delete(current, email)
				state.Changed, changed = true, true
			}
		}
		states = append(states, state)
		desiredGroups[tag] = desired
	}
	encoded, _ := json.Marshal(desiredGroups)
	hash := digest(string(encoded)) + nodeHash
	// Install routes after all revocations and before admitting any new UUID.
	if hash != a.lastUsers || changed {
		if _, err := a.xapi("adrules", configPath); err != nil {
			return errors.New("Xray routing synchronization failed")
		}
	}
	for _, state := range states {
		add := []object{}
		for email, id := range state.Desired {
			if state.Current[email] != id {
				add = append(add, object{"id": id, "email": email, "flow": "xtls-rprx-vision", "level": 0})
			}
		}
		if len(add) > 0 {
			inbound := object{}
			for key, value := range state.Inbound {
				inbound[key] = value
			}
			inbound["settings"] = object{"decryption": "none", "clients": add}
			path := filepath.Join(a.cfg.StateDir, "add-users.json")
			if err := writeJSON(path, object{"inbounds": []object{inbound}}); err != nil {
				return err
			}
			_, err := a.xapi("adu", path)
			_ = os.Remove(path)
			if err != nil {
				return errors.New("Xray user synchronization failed")
			}
		}
		if state.Changed || len(add) > 0 {
			// Xray's CLI can exit zero after a per-user RPC failure; read back
			// every changed inbound before claiming the generation is applied.
			actual, err := a.xrayUsers(state.Tag)
			if err != nil {
				return err
			}
			if len(actual) != len(state.Desired) {
				return errors.New("Xray user verification mismatch")
			}
			for email, id := range actual {
				if state.Desired[email] != id {
					return errors.New("Xray credential verification mismatch")
				}
			}
		}
	}
	a.lastUsers = hash
	return nil
}
