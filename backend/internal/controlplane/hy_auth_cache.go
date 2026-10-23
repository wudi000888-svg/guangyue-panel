package controlplane

import (
	"crypto/subtle"
	"sync"
	"sync/atomic"
	"time"
)

// HY2 calls the auth endpoint for every connection. Searching every user ×
// node pair per handshake turns a mass reconnect (after an HY2 restart) into
// thousands of full decrypting scans that time out and retry. The table maps
// a password digest to the (user, node) pair that owned it at build time;
// every hit is re-verified against that user's current record, so revocation,
// expiry and credential rotation still apply immediately.
type hyAuthEntry struct {
	userID int64
	nodeID string
}

type hyAuthTable struct {
	built   time.Time
	entries map[string]hyAuthEntry
}

type hyAuthCache struct {
	table   atomic.Pointer[hyAuthTable]
	buildMu sync.Mutex
}

// A miss rebuilds at most this often, so random passwords cannot force a
// full scan per request.
const hyAuthRebuildInterval = time.Second

func (a *App) hyAuthenticate(password string) (string, bool) {
	if password == "" {
		return "", false
	}
	key := digest(password)
	if t := a.hyAuthCache.table.Load(); t != nil {
		if e, ok := t.entries[key]; ok {
			if identity, ok := a.hyVerify(e, password); ok {
				return identity, true
			}
		}
	}
	t := a.rebuildHYAuth()
	if t == nil {
		return "", false
	}
	if e, ok := t.entries[key]; ok {
		return a.hyVerify(e, password)
	}
	return "", false
}

func (a *App) rebuildHYAuth() *hyAuthTable {
	a.hyAuthCache.buildMu.Lock()
	defer a.hyAuthCache.buildMu.Unlock()
	if t := a.hyAuthCache.table.Load(); t != nil && time.Since(t.built) < hyAuthRebuildInterval {
		return t
	}
	records, err := a.coreRecords()
	if err != nil {
		return a.hyAuthCache.table.Load()
	}
	nodes, err := a.store.nodes()
	if err != nil {
		return a.hyAuthCache.table.Load()
	}
	entries := map[string]hyAuthEntry{}
	for _, record := range records {
		if !record.Active() || !record.HY2 {
			continue
		}
		for _, node := range nodes {
			if node.Protocol != "hy2" || !memberMayUseNode(record, node) {
				continue
			}
			entries[digest(hyNodePassword(record, node))] = hyAuthEntry{userID: record.ID, nodeID: node.ID}
		}
	}
	t := &hyAuthTable{built: time.Now(), entries: entries}
	a.hyAuthCache.table.Store(t)
	return t
}

// hyVerify applies the full authorization policy to the current state of one
// user and node.
func (a *App) hyVerify(e hyAuthEntry, password string) (string, bool) {
	record, err := a.store.record(e.userID)
	if err != nil {
		return "", false
	}
	records, err := a.coreRecordsFrom([]Record{record})
	if err != nil || len(records) != 1 {
		return "", false
	}
	record = records[0]
	node, err := a.store.node(e.nodeID)
	if err != nil || !record.Active() || !record.HY2 || node.Protocol != "hy2" || !memberMayUseNode(record, node) {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(hyNodePassword(record, node))) != 1 {
		return "", false
	}
	return hyNodeIdentity(record, node), true
}
