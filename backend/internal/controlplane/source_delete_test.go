package controlplane

import (
	"errors"
	"testing"
)

func sourceDeletionFixture(t *testing.T) (*App, Record, ImportSource, string) {
	t.Helper()
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	source := newTestSource(t, a, "https://one.example/sub")
	if err := a.syncSourceLocked(source, sourceItems(t, "one", "two"), nil); err != nil {
		t.Fatal(err)
	}
	source, _ = a.store.importSource(source.ID)
	p, _ := a.store.pool(source.ResourceIDs[0])
	nodes, _ := a.store.nodes()
	for _, n := range nodes {
		if err := a.store.saveNode(bindPool(n, p)); err != nil {
			t.Fatal(err)
		}
	}
	return a, owner, source, p.ID
}

func TestSourceDeletionCascadesOwnedExitsNodesAndCredentials(t *testing.T) {
	a, owner, source, _ := sourceDeletionFixture(t)
	member := testUser(t, a, "member", "user")
	got := decoded[struct {
		OK        bool `json:"ok"`
		Resources int  `json:"resources_deleted"`
		Nodes     int  `json:"nodes_deleted"`
	}](t, req(t, a, owner, "DELETE", "/api/import-sources/"+source.ID, nil), 200)
	pools, _ := a.store.pools()
	nodes, _ := a.store.nodes()
	if !got.OK || got.Resources != 2 || got.Nodes != 2 || len(pools) != 0 || len(nodes) != 0 {
		t.Fatal("source deletion left orphaned resources or nodes")
	}
	for _, record := range []Record{owner, member} {
		current, _ := a.store.record(record.ID)
		if len(current.Credentials.VLESS) != 0 {
			t.Fatal("deleted node credential survived")
		}
		if current.Credentials.Token != record.Credentials.Token || current.Credentials.HY2 != record.Credentials.HY2 {
			t.Fatal("unrelated account credentials changed")
		}
	}
	if err := a.store.bootstrap(a.cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	nodes, _ = a.store.nodes()
	if len(nodes) != 0 {
		t.Fatal("bootstrap recreated deleted nodes")
	}
	if req(t, a, owner, "DELETE", "/api/import-sources/"+source.ID, nil).Code != 404 {
		t.Fatal("deleted source still exists")
	}
}

func TestSourceDeletionReassignsSharedOwnershipWithoutManualConversion(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	if req(t, a, owner, "POST", "/api/ips/import", object{"content": "hy2://fixture-secret@manual.example:443#manual\nhy2://fixture-secret@untouched.example:443#untouched"}).Code != 200 {
		t.Fatal("manual fixture")
	}
	one := newTestSource(t, a, "https://one.example/sub")
	two := newTestSource(t, a, "https://two.example/sub")
	if err := a.syncSourceLocked(one, sourceItems(t, "manual", "shared", "unique"), nil); err != nil {
		t.Fatal(err)
	}
	if err := a.syncSourceLocked(two, sourceItems(t, "shared"), nil); err != nil {
		t.Fatal(err)
	}
	one, _ = a.store.importSource(one.ID)
	two, _ = a.store.importSource(two.ID)
	shared := two.ResourceIDs[0]
	p, _ := a.store.pool(shared)
	nodes, _ := a.store.nodes()
	bound := bindPool(nodes[0], p)
	a.store.saveNode(bound)
	if req(t, a, owner, "DELETE", "/api/import-sources/"+one.ID, nil).Code != 200 {
		t.Fatal("source delete")
	}
	pools, _ := a.store.pools()
	if len(pools) != 2 {
		t.Fatal("wrong shared/manual preservation")
	}
	p, _ = a.store.pool(shared)
	if p.SubscriptionID != two.ID {
		t.Fatal("shared source resource became independent")
	}
	nodes, _ = a.store.nodes()
	if len(nodes) != 2 {
		t.Fatal("shared resource node removed")
	}
	if req(t, a, owner, "DELETE", "/api/import-sources/"+two.ID, nil).Code != 200 {
		t.Fatal("last source delete")
	}
	pools, _ = a.store.pools()
	nodes, _ = a.store.nodes()
	if len(pools) != 1 || pools[0].Host != "untouched.example" || len(nodes) != 1 || nodes[0].ID == bound.ID {
		t.Fatal("last ownership did not cascade or removed an unrelated independent exit")
	}
}

func TestSourceDeletionApplyFailureRestoresAffectedRows(t *testing.T) {
	a, owner, source, boundID := sourceDeletionFixture(t)
	plan, err := a.planSourceDeletion(source)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err = a.applySourceDeletion(plan, func() error {
		attempts++
		if attempts == 1 {
			return errors.New("injected core failure")
		}
		return nil
	})
	if err == nil || attempts != 2 {
		t.Fatal("failed apply did not restore/reapply")
	}
	s, err := a.store.importSource(source.ID)
	if err != nil || s.Revision != source.Revision {
		t.Fatal("source rollback")
	}
	pools, _ := a.store.pools()
	nodes, _ := a.store.nodes()
	current, _ := a.store.record(owner.ID)
	if len(pools) != 2 || len(nodes) != 2 || nodes[0].ExitID != boundID || current.Credentials.VLESS["vless-main"] != owner.Credentials.VLESS["vless-main"] {
		t.Fatal("resource/node/credential rollback")
	}
}

func TestSourceDeletionRejectsRunningResourceWork(t *testing.T) {
	a, owner, source, _ := sourceDeletionFixture(t)
	a.heavyMu.Lock()
	response := req(t, a, owner, "DELETE", "/api/import-sources/"+source.ID, nil)
	a.heavyMu.Unlock()
	if response.Code != 409 {
		t.Fatal("deletion overlapped a resource job")
	}
	pools, _ := a.store.pools()
	nodes, _ := a.store.nodes()
	if len(pools) != 2 || len(nodes) != 2 {
		t.Fatal("busy deletion changed configuration")
	}
}
