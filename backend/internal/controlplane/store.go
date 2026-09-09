package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	edition   string
	db        *persistence.DB
	vault     *Vault
	stateDir  string
	runtimeMu sync.RWMutex
	runtime   RuntimeSettings
}

func openStore(dir string) (*Store, error) {
	return openConfiguredStore(Config{StateDir: dir})
}
func openConfiguredStore(cfg Config) (*Store, error) {
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, err
	}
	v, err := openVault(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := persistence.Open(ctx, cfg.StateDir, cfg.DatabaseOptions())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, vault: v, stateDir: cfg.StateDir, edition: cfg.edition()}
	if err = s.initRuntimeSettings(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) records() ([]Record, error) {
	rows, err := s.db.Query("SELECT id,doc,credentials,password FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var b, c []byte
		if err = rows.Scan(&r.ID, &b, &c, &r.Password); err != nil {
			return nil, err
		}
		id := r.ID
		if err = json.Unmarshal(b, &r.User); err != nil {
			return nil, err
		}
		r.ID = id
		if err = s.vault.open(c, &r.Credentials); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) record(id int64) (Record, error) {
	var r Record
	var b, c []byte
	err := s.db.QueryRow("SELECT id,doc,credentials,password FROM users WHERE id=?", id).Scan(&r.ID, &b, &c, &r.Password)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r.User); err != nil {
		return r, err
	}
	r.ID = id
	err = s.vault.open(c, &r.Credentials)
	return r, err
}
func (s *Store) save(r *Record) error {
	if r.Credentials.PublicToken == "" {
		r.Credentials.PublicToken = randomToken(32)
	}
	b, err := json.Marshal(r.User)
	if err != nil {
		return err
	}
	c, err := s.vault.seal(r.Credentials)
	if err != nil {
		return err
	}
	if r.ID == 0 {
		return s.db.QueryRow("INSERT INTO users(username,doc,credentials,password,token_hash) VALUES(?,?,?,?,?) RETURNING id", r.Username, b, c, r.Password, digest(r.Credentials.Token)).Scan(&r.ID)
	}
	_, err = s.db.Exec("UPDATE users SET username=?,doc=?,credentials=?,password=?,token_hash=? WHERE id=?", r.Username, b, c, r.Password, digest(r.Credentials.Token), r.ID)
	return err
}
func (s *Store) nodes() ([]Node, error) {
	rows, err := s.db.Query("SELECT doc FROM nodes ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var b []byte
		var n Node
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) saveNode(n Node) error {
	b, err := s.vault.seal(n)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO nodes(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", n.ID, b)
	return err
}
func (s *Store) meta(key string) string {
	v, _ := s.readMeta(key)
	return v
}
func (s *Store) setMeta(key, value string) error {
	_, err := s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

func (s *Store) queueKick(id int64) error {
	_, err := s.db.Exec("INSERT INTO revocations(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING", id)
	return err
}
func (s *Store) audit(actor, action, target string) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if !s.logsEnabledLocked() {
		return
	}
	_, _ = s.db.Exec("INSERT INTO audit(at,actor,action,target) VALUES(?,?,?,?)", time.Now().Unix(), actor, action, target)
	_, _ = s.db.Exec("DELETE FROM audit WHERE id < (SELECT COALESCE(MAX(id),0)-2000 FROM audit)")
}
func (s *Store) bootstrap(dir string) error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	n := Node{ID: "vless-main", Name: "待检测 · VLESS", Protocol: "vless", Enabled: true, Exit: "direct"}
	if err := s.saveNode(n); err != nil {
		return err
	}
	if err := s.saveNode(Node{ID: "hy2-main", Name: "待检测 · HY2", Protocol: "hy2", Enabled: true, Exit: "direct"}); err != nil {
		return err
	}
	pass := randomToken(18)
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return err
	}
	r := Record{User: User{Username: "owner", Role: "owner", Enabled: true, VLESS: true, HY2: true, Created: time.Now().Unix()}, Password: hash, Credentials: Credentials{HY2: randomToken(24), Token: randomToken(32), VLESS: map[string]string{n.ID: uuid()}}}
	if err = writeJSON(filepath.Join(dir, "initial-owner.json"), map[string]string{"username": "owner", "password": pass}); err != nil {
		return err
	}
	if err = s.save(&r); err != nil {
		return err
	}
	s.audit("system", "initialize", "personal edition")
	return nil
}

type Counter struct {
	Key, Generation, Protocol, Direction string
	UserID                               int64
	Value                                int64
}

func (s *Store) account(counters []Counter) error {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	recordHistory := s.logsEnabledLocked()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type delta struct{ up, down, vless, hy2 int64 }
	changes := map[int64]*delta{}
	for _, c := range counters {
		if c.Value < 0 {
			continue
		}
		var gen string
		var last int64
		err = tx.QueryRow("SELECT generation,value FROM checkpoints WHERE key=?", c.Key).Scan(&gen, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		d := c.Value
		if gen == c.Generation && c.Value >= last {
			d = c.Value - last
		}
		if _, err = tx.Exec("INSERT INTO checkpoints(key,generation,value) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET generation=excluded.generation,value=excluded.value", c.Key, c.Generation, c.Value); err != nil {
			return err
		}
		if d == 0 {
			continue
		}
		if changes[c.UserID] == nil {
			changes[c.UserID] = &delta{}
		}
		x := changes[c.UserID]
		if c.Direction == "up" {
			x.up += d
		} else {
			x.down += d
		}
		if c.Protocol == "vless" {
			x.vless += d
		} else {
			x.hy2 += d
		}
	}
	for id, d := range changes {
		var b []byte
		if err = tx.QueryRow("SELECT doc FROM users WHERE id=?", id).Scan(&b); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		var u User
		if err = json.Unmarshal(b, &u); err != nil {
			return err
		}
		u.Upload += d.up
		u.Download += d.down
		u.VLESSTraffic += d.vless
		u.HY2Traffic += d.hy2
		b, err = json.Marshal(u)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE users SET doc=? WHERE id=?", b, id); err != nil {
			return err
		}
		if recordHistory {
			if _, err = tx.Exec("INSERT INTO traffic(hour,user_id,upload,download) VALUES(?,?,?,?) ON CONFLICT(hour,user_id) DO UPDATE SET upload=upload+excluded.upload,download=download+excluded.download", time.Now().Unix()/3600*3600, id, d.up, d.down); err != nil {
				return err
			}
		}
	}
	if recordHistory {
		if _, err = tx.Exec("DELETE FROM traffic WHERE hour<?", time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func email(id int64, node string) string { return fmt.Sprintf("u%d.%s@personal", id, node) }
func idFromEmail(s string) int64         { var id int64; _, _ = fmt.Sscanf(s, "u%d.", &id); return id }
func hyID(id int64) string               { return "u" + strconv.FormatInt(id, 10) }
func hyIdentity(r Record) string {
	return hyID(r.ID) + "." + strconv.FormatUint(r.Credentials.HYGeneration, 10) + "." + digest(r.Credentials.HY2)[:12]
}

// readMeta distinguishes an absent key from a broken database. Optional display
// metadata can use meta; settings and runtime decisions must propagate errors.
func (s *Store) readMeta(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
func (s *Store) markPreparedNodes() error {
	nodes, err := s.nodes()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, protocol := range map[string]string{"x_nodes": "vless", "hy_nodes": "hy2"} {
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, nodeHash(nodes, protocol)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
