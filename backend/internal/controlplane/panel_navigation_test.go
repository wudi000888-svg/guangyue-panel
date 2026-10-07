package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

func getNavigation(t *testing.T, a *App, actor Record, role string) navigationSettings {
	t.Helper()
	return decoded[navigationSettings](t, req(t, a, actor, "GET", "/api/settings/navigation?role="+role, nil), 200)
}

func putNavigation(role string, items []string, revision string) object {
	return object{"role": role, "items": items, "revision": revision}
}

func TestPanelNavigationPartialSaveAndRevisions(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	admin := getNavigation(t, a, owner, "admin")
	member := getNavigation(t, a, owner, "member")
	if admin.Revision == "" || member.Revision == "" || admin.Revision == member.Revision {
		t.Fatal("missing role-specific revision")
	}
	settings := a.store.siteSettings()
	settings.PanelName = "Retain latest site settings"
	settings.LoginNotice = "This must survive navigation autosave"
	settings.ClientDownloadRelay = true
	settings.CountryAccessBlocked = []string{"CN"}
	settings.CountryAccessReason = "Retain access policy"
	settings = decoded[SiteSettings](t, req(t, a, owner, "PUT", "/api/settings", settings), 200)
	if got := getNavigation(t, a, owner, "admin"); got.Revision != admin.Revision || got.SiteRevision != settings.Revision {
		t.Fatal("unrelated settings invalidated navigation revision")
	}
	generation := a.store.meta("desired_generation")
	saved := decoded[navigationSettings](t, req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("admin", []string{"settings", "tasks", "nodes", "users"}, admin.Revision)), 200)
	if !slices.Equal(saved.Items, []string{"tasks", "nodes", "users", "settings"}) || saved.Revision == admin.Revision || saved.SiteRevision == settings.Revision {
		t.Fatal("order, fixed settings or revisions not saved")
	}
	current := a.store.siteSettings()
	want := settings
	want.SidebarAdmin, want.Revision = saved.Items, saved.SiteRevision
	if !reflect.DeepEqual(current, want) || a.store.meta("desired_generation") != generation || a.status != "" {
		t.Fatal("partial save changed unrelated settings or proxy state")
	}
	if req(t, a, owner, "PUT", "/api/settings", settings).Code != 409 {
		t.Fatal("old whole-settings form overwrote autosave")
	}
	memberSaved := decoded[navigationSettings](t, req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("member", []string{"clients", "wallet"}, member.Revision)), 200)
	if !slices.Equal(memberSaved.Items, []string{"clients", "wallet", "settings"}) || !slices.Equal(a.store.siteSettings().SidebarAdmin, saved.Items) {
		t.Fatal("other role conflicted or overwrote admin navigation")
	}
	if req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("admin", []string{"users"}, admin.Revision)).Code != 409 {
		t.Fatal("same-role stale revision accepted")
	}
	if req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("admin", []string{"users"}, memberSaved.Revision)).Code != 409 {
		t.Fatal("another role's revision accepted")
	}
	noop := decoded[navigationSettings](t, req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("admin", saved.Items, saved.Revision)), 200)
	if noop.Revision != saved.Revision || noop.SiteRevision != memberSaved.SiteRevision {
		t.Fatal("no-op save changed revisions")
	}
	cleared := decoded[navigationSettings](t, req(t, a, owner, "PUT", "/api/settings/navigation", putNavigation("member", []string{}, memberSaved.Revision)), 200)
	if !slices.Equal(cleared.Items, []string{"settings"}) {
		t.Fatal("removing all movable entries removed fixed settings")
	}
	public := decoded[SiteSettings](t, req(t, a, Record{}, "GET", "/api/site", nil), 200)
	if !slices.Equal(public.SidebarAdmin, saved.Items) || !slices.Equal(public.SidebarMember, cleared.Items) {
		t.Fatal("site navigation did not refresh after autosave")
	}
}

func TestPanelNavigationPermissionsAndValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	base := getNavigation(t, a, owner, "admin")
	for _, method := range []string{"GET", "PUT"} {
		path := "/api/settings/navigation?role=admin"
		body := putNavigation("admin", []string{"users"}, base.Revision)
		if req(t, a, member, method, path, body).Code != 403 || req(t, a, Record{}, method, path, body).Code != 401 {
			t.Fatal("navigation requires owner authentication", method)
		}
	}
	for _, role := range []string{"", "owner", "user", "ADMIN"} {
		if req(t, a, owner, "GET", "/api/settings/navigation?role="+role, nil).Code != 400 {
			t.Fatal("invalid GET role accepted", role)
		}
	}
	before := a.store.meta("site_settings")
	for _, input := range []object{
		putNavigation("owner", []string{"users"}, base.Revision),
		putNavigation("admin", nil, base.Revision),
		putNavigation("admin", []string{"users"}, ""),
		putNavigation("admin", []string{"users", "users"}, base.Revision),
		putNavigation("admin", []string{"unknown"}, base.Revision),
		putNavigation("admin", []string{"/api/backup"}, base.Revision),
		putNavigation("member", []string{"users"}, base.Revision),
		putNavigation("member", []string{"nodes"}, base.Revision),
		putNavigation("member", []string{"fleet"}, base.Revision),
		{"role": "admin", "items": []string{"nodes"}, "revision": base.Revision, "panel_name": "Not a partial update"},
	} {
		if w := req(t, a, owner, "PUT", "/api/settings/navigation", input); w.Code != 400 {
			t.Fatalf("invalid request accepted: %v (%d %s)", input, w.Code, w.Body.String())
		}
	}
	if a.store.meta("site_settings") != before {
		t.Fatal("rejected navigation mutation persisted")
	}
}

func TestPanelNavigationConcurrentWriters(t *testing.T) {
	for _, distinctRoles := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_role", true: "different_roles"}[distinctRoles], func(t *testing.T) {
			a := testApp(t)
			owner := testUser(t, a, "owner", "owner")
			// Independent controller locks exercise the SQL compare-and-swap,
			// rather than relying on one App mutex to serialize both writers.
			other := &App{store: a.store}
			roles := []string{"admin", "admin"}
			items := [][]string{{"nodes", "users"}, {"clients", "wallet"}}
			if distinctRoles {
				roles[1] = "member"
			}
			base := []navigationSettings{getNavigation(t, a, owner, roles[0]), getNavigation(t, a, owner, roles[1])}
			start := make(chan struct{})
			responses := make([]*httptest.ResponseRecorder, 2)
			var wg sync.WaitGroup
			for i, app := range []*App{a, other} {
				wg.Add(1)
				go func(i int, app *App) {
					defer wg.Done()
					b, _ := json.Marshal(putNavigation(roles[i], items[i], base[i].Revision))
					r := httptest.NewRequest("PUT", "/api/settings/navigation", bytes.NewReader(b))
					responses[i] = httptest.NewRecorder()
					<-start
					app.panelNavigation(responses[i], r, owner)
				}(i, app)
			}
			close(start)
			wg.Wait()
			if distinctRoles {
				for i, w := range responses {
					saved := decoded[navigationSettings](t, w, 200)
					if !slices.Equal(saved.Items, append(items[i], "settings")) {
						t.Fatal("concurrent partial write lost requested order")
					}
				}
				v := a.store.siteSettings()
				if !slices.Equal(v.SidebarAdmin, []string{"nodes", "users", "settings"}) || !slices.Equal(v.SidebarMember, []string{"clients", "wallet", "settings"}) {
					t.Fatal("concurrent role update lost another role")
				}
			} else {
				codes := []int{responses[0].Code, responses[1].Code}
				slices.Sort(codes)
				if !slices.Equal(codes, []int{200, 409}) {
					t.Fatal("same-role concurrent edits must have one winner", codes)
				}
			}
		})
	}
}

func TestPanelNavigationRemoteManagement(t *testing.T) {
	master, child := testApp(t), testApp(t)
	master.cfg.Edition = "pro"
	master.cfg.SiteID, child.cfg.SiteID = "navigation_master", "navigation_child"
	owner := testUser(t, master, "owner", "owner")
	childOwner := testUser(t, child, "child-owner", "owner")
	server := httptest.NewServer(child.routes())
	defer server.Close()
	token := fleetToken(t, child, childOwner, "manage")
	peer := decoded[FleetPeer](t, req(t, master, owner, "POST", "/api/fleet/peers", object{"name": "Child", "url": server.URL, "token": token}), 201)
	base := decoded[navigationSettings](t, siteRequest(t, master, owner, peer.ID, "GET", "/api/settings/navigation?role=member", nil), 200)
	saved := decoded[navigationSettings](t, siteRequest(t, master, owner, peer.ID, "PUT", "/api/settings/navigation", putNavigation("member", []string{"wallet", "clients"}, base.Revision)), 200)
	if !slices.Equal(child.store.siteSettings().SidebarMember, saved.Items) || slices.Equal(master.store.siteSettings().SidebarMember, saved.Items) {
		t.Fatal("remote navigation saved to the wrong site")
	}
	child.cfg.Edition = "pro" // Pro supports the optional read-only token scope.
	readToken := fleetToken(t, child, childOwner, "read")
	b, _ := json.Marshal(object{"method": "PUT", "path": "/api/settings/navigation", "body": putNavigation("member", []string{}, saved.Revision)})
	r := httptest.NewRequest("POST", "/api/fleet-gateway", bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+readToken)
	r.Header.Set("X-Requested-With", "guangyue")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	child.routes().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || httpapi.AllowedGateway("PUT", "/api/settings/navigation", "read") {
		t.Fatal("read-only token modified navigation")
	}
}
