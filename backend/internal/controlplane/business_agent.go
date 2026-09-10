package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type businessAgentState struct {
	Tasks      map[string]string `json:"tasks,omitempty"`
	Token      string            `json:"token"`
	Applied    string            `json:"applied"`
	LeaseUntil int64             `json:"lease_until"`
	Contact    int64             `json:"contact"`
	Error      string            `json:"error"`
}

func (s *Store) businessAgentState() (businessAgentState, error) {
	var v businessAgentState
	raw, err := s.readMeta("business_agent")
	if err != nil {
		return v, err
	}
	if raw == "" {
		return v, nil
	}
	b, err := base64.RawStdEncoding.DecodeString(raw)
	if err == nil {
		err = s.vault.open(b, &v)
	}
	return v, err
}
func (s *Store) saveBusinessAgent(v businessAgentState) error {
	b, e := s.vault.seal(v)
	if e != nil {
		return e
	}
	if e = s.setMeta("business_agent", base64.RawStdEncoding.EncodeToString(b)); e != nil {
		return e
	}
	// Core wrappers enforce expiry independently, including when the panel dies.
	return writeJSON(filepath.Join(s.stateDir, "business-lease.json"), object{"lease_until": v.LeaseUntil})
}
func (a *App) businessLeaseDeadline() int64 {
	v, e := a.store.businessAgentState()
	if e != nil {
		return 0
	}
	return v.LeaseUntil
}
func (a *App) businessRequest(ctx context.Context, client *http.Client, path, token string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return errors.New("business report exceeds limit")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(a.cfg.ControllerURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "guangyue")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("controller connection unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("controller did not accept business request")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return errors.New("invalid controller response")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return errors.New("invalid controller response")
	}
	return nil
}
func (a *App) syncBusinessAgent(ctx context.Context, client *http.Client) error {
	a.mu.Lock()
	state, err := a.store.businessAgentState()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if state.Token == "" {
		token := a.cfg.BusinessToken
		if token == "" {
			var out struct {
				SiteID string `json:"site_id"`
				Token  string `json:"token"`
			}
			info := BusinessInfo{SiteID: a.cfg.siteID(), Version: version, Protocol: 1, VLESSHost: a.cfg.VLESSHost, HY2Host: a.cfg.HY2Host, RealityPublic: a.cfg.RealityPublic, RealitySNI: a.cfg.RealitySNI, ShortID: a.cfg.ShortID}
			if err = a.businessRequest(ctx, client, "/api/business/enroll", a.cfg.EnrollmentToken, info, &out); err != nil {
				return err
			}
			if out.SiteID != a.cfg.siteID() || !strings.HasPrefix(out.Token, "gyb_") || len(out.Token) != 47 {
				return errors.New("controller returned invalid enrollment")
			}
			token = out.Token
		}
		state.Token = token
		if err = a.store.saveBusinessAgent(state); err != nil {
			return err
		}
	}
	a.mu.Lock()
	// A final sample before publishing the watermark prevents ordinary controller
	// restarts from losing unreported traffic. No counter reset is requested.
	collectionError := a.collect()
	users, err := a.store.records()
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if collectionError != nil && !a.cfg.Dev && len(users) > 0 && a.businessLeaseDeadline() > time.Now().Unix() {
		a.mu.Unlock()
		return errors.New("business traffic collection failed")
	}
	nodes, err := a.store.nodes()
	if err != nil {
		a.mu.Unlock()
		return err
	}
	report := BusinessHeartbeat{Version: version, SiteID: a.cfg.siteID(), Applied: state.Applied, Usage: map[int64]BusinessUsage{}, Error: state.Error}
	for _, u := range users {
		report.Usage[u.ID] = BusinessUsage{Upload: u.Upload, Download: u.Download, VLESS: u.VLESSTraffic, HY2: u.HY2Traffic}
	}
	for _, n := range nodes {
		report.Reports = append(report.Reports, n.public())
	}
	report.Commands = map[string]string{}
	if a.jobs != nil {
		for id, taskID := range state.Tasks {
			if task, e := a.jobs.Get(ctx, taskID); e == nil {
				report.Commands[id] = task.State
			} else {
				report.Commands[id] = "failed"
			}
		}
	}
	a.mu.Unlock()
	var snapshot BusinessSnapshot
	if err = a.businessRequest(ctx, client, "/api/business/sync", state.Token, report, &snapshot); err != nil {
		return err
	}
	if err = a.applyBusinessSnapshot(ctx, snapshot); err != nil {
		return err
	}
	return a.queueBusinessCommands(ctx, snapshot.Commands)
}
func (a *App) businessAgentLoop(ctx context.Context) {
	transport := httpapi.NewGatewayTransport(a.cfg.Dev)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := a.syncBusinessAgent(request, client)
		cancel()
		if err != nil {
			a.mu.Lock()
			state, e := a.store.businessAgentState()
			if e == nil {
				state.Error = "业务站同步失败，保留有效期内的已生效配置"
				_ = a.store.saveBusinessAgent(state)
			}
			a.mu.Unlock()
			if delay < 10*time.Second {
				delay = 10 * time.Second
			} else {
				delay = min(time.Minute, delay*2)
			}
		} else {
			delay = 25*time.Second + time.Duration(time.Now().UnixNano()%10)*time.Second
		}
	}
}
func (a *App) applyBusinessSnapshot(ctx context.Context, snapshot BusinessSnapshot) error {
	now := time.Now().Unix()
	if snapshot.SiteID != a.cfg.siteID() || snapshot.LeaseSeconds < 30 || snapshot.LeaseSeconds > businessLeaseSeconds || snapshot.IssuedAt > now+60 || snapshot.IssuedAt+snapshot.LeaseSeconds <= now || snapshot.Revision != businessSnapshotRevision(snapshot) || len(snapshot.Users) > 256 || len(snapshot.Nodes) > 16 {
		return errors.New("business policy identity, lease or revision invalid")
	}
	if snapshot.RuntimeMode != runtimeNormal && snapshot.RuntimeMode != runtimeNoLogs {
		return errors.New("invalid runtime policy")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state, err := a.store.businessAgentState()
	if err != nil {
		return err
	}
	if snapshot.IssuedAt < state.Contact {
		return errors.New("stale business policy")
	}
	if state.Applied == snapshot.Revision {
		state.LeaseUntil = snapshot.IssuedAt + snapshot.LeaseSeconds
		state.Contact = snapshot.IssuedAt
		state.Error = ""
		if err = a.store.saveBusinessAgent(state); err != nil {
			return err
		}
		return a.reconcileIfNeeded()
	}
	oldRuntime := a.store.runtimeSnapshot()
	oldNodes, err := a.store.nodes()
	if err != nil {
		return err
	}
	oldPools, err := a.store.pools()
	if err != nil {
		return err
	}
	oldUsers, err := a.store.records()
	if err != nil {
		return err
	}
	nodes := append([]Node{}, snapshot.Nodes...)
	seen := map[string]bool{}
	exits := map[string]IPResource{}
	for i := range nodes {
		n := &nodes[i]
		if !simpleID(n.ID) || seen[n.ID] || n.Protocol != "vless" && n.Protocol != "hy2" {
			return errors.New("invalid business node")
		}
		seen[n.ID] = true
		n.DefaultDirect = n.ID == "vless-main" && n.Protocol == "vless" || n.ID == "hy2-main" && n.Protocol == "hy2"
		if n.DefaultDirect {
			if n.Exit != "direct" || !n.Enabled || n.ExitID != "" {
				return errors.New("business default direct node is required")
			}
		}
		if n.Exit == "direct" && !n.DefaultDirect {
			return errors.New("additional business node requires an assigned exit")
		}
		if n.DNS != nil {
			if err = validateNodeDNS(n.DNS); err != nil {
				return err
			}
		}
		if n.RealitySNI != "" {
			n.RealitySNI, err = normalizeRealitySNI(n.RealitySNI, a.cfg)
			if err != nil {
				return err
			}
			for _, old := range oldNodes {
				if old.ID == n.ID && old.RealitySNI == n.RealitySNI {
					n.RealityIP = old.RealityIP
				}
			}
			if n.RealitySNI != "" && n.RealityIP == "" {
				n.RealityIP, err = resolveRealityTarget(ctx, a.cfg, nodes, n.RealitySNI, net.DefaultResolver.LookupIPAddr, checkRealityTLS)
				if err != nil {
					return err
				}
			}
		}
		if n.ExitID != "" {
			p := IPResource{Node: *n, Label: n.Name, PoolGroup: "private", Revision: randomToken(12)}
			p.ID = n.ExitID
			p.ExitID = ""
			if n.ManagedBy == publicManager {
				p.PoolGroup = "public"
			}
			if old, ok := exits[p.ID]; ok {
				if !sameExit(old.Node, p.Node) {
					return errors.New("conflicting business exit definitions")
				}
			} else {
				exits[p.ID] = p
			}
		}
	}
	if !seen["vless-main"] || !seen["hy2-main"] {
		return errors.New("missing default business nodes")
	}
	ids := []string{}
	for id := range exits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	pools := []IPResource{}
	for i, id := range ids {
		p := exits[id]
		if p.Exit == "subscription" {
			p.BridgePort = 21000 + i
			p.BridgePassword = digest("business-bridge/" + a.cfg.StatsSecret + "/" + id)
		}
		for _, old := range oldPools {
			if old.ID == id && sameExit(old.Node, p.Node) {
				p.Quality = old.Quality
				p.Speed = old.Speed
				p.ProbeIP = old.ProbeIP
				p.Country = old.Country
				p.CountryCode = old.CountryCode
				p.CheckedAt = old.CheckedAt
				p.ProbedAt = old.ProbedAt
				p.Reachable = old.Reachable
			}
		}
		exits[id] = p
		pools = append(pools, p)
	}
	for i := range nodes {
		if nodes[i].ExitID != "" {
			nodes[i] = bindPool(nodes[i], exits[nodes[i].ExitID])
		} else {
			for _, old := range oldNodes {
				if old.ID == nodes[i].ID && sameExit(old, nodes[i]) {
					nodes[i].Quality = old.Quality
					nodes[i].ProbeIP = old.ProbeIP
					nodes[i].Country = old.Country
					nodes[i].CountryCode = old.CountryCode
					nodes[i].Name = old.Name
					nodes[i].CheckedAt = old.CheckedAt
					nodes[i].ProbedAt = old.ProbedAt
				}
			}
		}
		if err = validateNode(nodes[i]); err != nil {
			return err
		}
	}
	records := []Record{}
	usersSeen := map[int64]bool{}
	names := map[string]bool{}
	for _, account := range snapshot.Users {
		u := account.User
		if u.ID <= 0 || usersSeen[u.ID] || names[u.Username] || !usernamePattern.MatchString(u.Username) || u.Role != "user" || u.Quota < 0 || u.Quota > 1<<60 || len(account.Credentials.HY2) != 43 {
			return errors.New("invalid business account")
		}
		usersSeen[u.ID] = true
		names[u.Username] = true
		r := Record{User: u, Credentials: account.Credentials, Password: []byte("!")}
		r.Upload = 0
		r.Download = 0
		r.VLESSTraffic = 0
		r.HY2Traffic = 0
		for _, old := range oldUsers {
			if old.ID == u.ID {
				r.Upload = old.Upload
				r.Download = old.Download
				r.VLESSTraffic = old.VLESSTraffic
				r.HY2Traffic = old.HY2Traffic
			}
		}
		for _, n := range nodes {
			if n.Protocol == "vless" && !uuidPattern.MatchString(r.Credentials.VLESS[n.ID]) {
				return errors.New("invalid business VLESS identity")
			}
		}
		records = append(records, r)
	}
	for _, old := range oldUsers {
		if !usersSeen[old.ID] {
			old.Enabled = false
			old.Username = "~retired:" + businessUsageKey(old.ID)
			records = append(records, old)
		}
	}
	if err = validateRealityNodes(a.cfg, nodes); err != nil {
		return err
	}
	if !a.cfg.Dev {
		candidate := filepath.Join(a.cfg.StateDir, "business-candidate-xray.json")
		if err = writeJSON(candidate, xrayConfig(a.cfg, records, nodes)); err != nil {
			return err
		}
		defer os.Remove(candidate)
		if _, err = command(8*time.Second, a.cfg.Xray, "run", "-test", "-c", candidate); err != nil {
			return errors.New("business Xray validation failed")
		}
		if err = a.validateHY(hy2Config(a.cfg, nodes)); err != nil {
			return err
		}
		if err = a.validateBridge(pools); err != nil {
			return err
		}
	}
	if _, _, err = a.store.changeRuntimeMode(snapshot.RuntimeMode, a.store.runtimeSnapshot().Revision); err != nil {
		return err
	}
	if err = a.replaceBusinessState(records, nodes, pools); err != nil {
		_, _, _ = a.store.changeRuntimeMode(oldRuntime.Mode, a.store.runtimeSnapshot().Revision)
		return err
	}
	// Expiry remains at the last accepted lease until the candidate is committed.
	// Granting this lease only follows successful syntax and transport validation.
	previous := state
	state.LeaseUntil = snapshot.IssuedAt + snapshot.LeaseSeconds
	state.Contact = snapshot.IssuedAt
	if err = a.store.saveBusinessAgent(state); err == nil {
		err = a.reconcile()
	}
	if err != nil {
		restoreDB := a.replaceBusinessState(oldUsers, oldNodes, oldPools)
		_, _, restoreMode := a.store.changeRuntimeMode(oldRuntime.Mode, a.store.runtimeSnapshot().Revision)
		restoreLease := a.store.saveBusinessAgent(previous)
		restoreCore := a.reconcile()
		if errors.Join(restoreDB, restoreMode, restoreLease, restoreCore) != nil {
			previous.LeaseUntil = 0
			previous.Error = "业务站配置回滚失败，授权已关闭"
			_ = a.store.saveBusinessAgent(previous)
			_ = a.reconcile()
			return errors.New("business policy rollback failed; authorization closed")
		}
		return errors.New("business policy apply failed; previous policy restored")
	}
	state.Applied = snapshot.Revision
	state.Error = ""
	return a.store.saveBusinessAgent(state)
}
func (a *App) replaceBusinessState(records []Record, nodes []Node, pools []IPResource) error {
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := []any{}
	marks := []string{}
	for _, r := range records {
		ids = append(ids, r.ID)
		marks = append(marks, "?")
	}
	if len(ids) == 0 {
		_, err = tx.Exec("DELETE FROM users")
	} else {
		_, err = tx.Exec("DELETE FROM users WHERE id NOT IN ("+strings.Join(marks, ",")+")", ids...)
	}
	if err != nil {
		return err
	}
	// Retire removed usernames before upserting replacements with the same name.
	for _, r := range records {
		if !r.Enabled && strings.HasPrefix(r.Username, "~retired:") {
			if _, err = tx.Exec("UPDATE users SET username=? WHERE id=?", r.Username, r.ID); err != nil {
				return err
			}
		}
	}
	for _, r := range records {
		b, e := json.Marshal(r.User)
		if e != nil {
			return e
		}
		c, e := a.store.vault.seal(r.Credentials)
		if e != nil {
			return e
		}
		_, err = tx.Exec("INSERT INTO users(id,username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,doc=excluded.doc,credentials=excluded.credentials,password=excluded.password,token_hash=excluded.token_hash", r.ID, r.Username, b, c, []byte("!"), digest(r.Credentials.Token))
		if err != nil {
			return err
		}
	}
	for _, table := range []string{"nodes", "ip_pool"} {
		if _, err = tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		b, e := a.store.vault.seal(n)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO nodes(id,doc) VALUES(?,?)", n.ID, b); err != nil {
			return err
		}
	}
	for _, p := range pools {
		b, e := a.store.vault.seal(p)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO ip_pool(id,doc) VALUES(?,?)", p.ID, b); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) queueBusinessCommands(ctx context.Context, commands []BusinessCommand) error {
	if len(commands) > 16 {
		return errors.New("too many business commands")
	}
	if a.jobs == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state, e := a.store.businessAgentState()
	if e != nil {
		return e
	}
	current := map[string]string{}
	for _, c := range commands {
		if len(c.ID) != 22 || (c.Kind != "speed" && c.Kind != "quality") || !simpleID(c.NodeID) || c.Created < time.Now().Add(-24*time.Hour).Unix() {
			continue
		}
		if id, ok := state.Tasks[c.ID]; ok {
			current[c.ID] = id
			continue
		}
		target := "nodes/" + c.NodeID
		guard, err := a.taskGuard(c.Kind, target)
		if err != nil {
			continue
		}
		task, err := a.jobs.Submit(ctx, c.Kind, target, guard, 0)
		if err != nil {
			return err
		}
		current[c.ID] = task.ID
	}
	state.Tasks = current
	return a.store.saveBusinessAgent(state)
}
