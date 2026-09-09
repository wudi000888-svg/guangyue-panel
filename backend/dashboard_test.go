package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func putTraffic(t *testing.T, a *App, user Record, at, up, down int64) {
	t.Helper()
	if _, err := a.store.db.Exec("INSERT INTO traffic(hour,user_id,upload,download) VALUES(?,?,?,?)", at, user.ID, up, down); err != nil {
		t.Fatal(err)
	}
}
func TestDashboardRangesRespectLocalDayAndActualTraffic(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	now := time.Date(2026, 9, 8, 2, 30, 0, 0, time.UTC).Unix()
	localDay := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC).Unix()
	putTraffic(t, a, member, localDay-3600, 10, 20)
	putTraffic(t, a, member, localDay, 30, 40)
	putTraffic(t, a, owner, localDay+3600, 50, 60)
	putTraffic(t, a, member, localDay-4*86400, 70, 80)
	putTraffic(t, a, member, localDay-10*86400, 1, 2)
	d, err := a.store.dashboardTraffic(owner, "24h", 8*3600, now)
	if err != nil || len(d.Buckets) != 24 || d.Today != 180 || d.Upload != 90 || d.Download != 120 {
		t.Fatalf("incorrect local-day/hour sums: %+v %v", d, err)
	}
	seven, err := a.store.dashboardTraffic(owner, "7d", 8*3600, now)
	if err != nil || len(seven.Buckets) != 7 || seven.Start != localDay-6*86400 || seven.Upload+seven.Download != 360 || seven.Previous != 3 || seven.ComparisonAvailable {
		t.Fatalf("incorrect daily window: %+v %v", seven, err)
	}
	thirty, err := a.store.dashboardTraffic(owner, "30d", 8*3600, now)
	if err != nil || len(thirty.Buckets) != 30 || thirty.Upload+thirty.Download != 363 || thirty.ComparisonAvailable {
		t.Fatal("30 day retention or comparison is misleading")
	}
	var sum int64
	for _, b := range thirty.Buckets {
		sum += b.Upload + b.Download
	}
	if sum != thirty.Upload+thirty.Download {
		t.Fatal("chart total differs from actual total")
	}
	if len(seven.Leaders) != 2 || seven.Leaders[0].UserID != member.ID {
		t.Fatal("leaderboard must use selected period traffic")
	}
}
func TestDashboardMemberScopeAndAPIValidation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	hour := time.Now().Unix() / 3600 * 3600
	putTraffic(t, a, owner, hour, 1000, 2000)
	putTraffic(t, a, member, hour, 10, 20)
	newTestSource(t, a, "https://provider.example/private-token")
	for _, path := range []string{"/api/dashboard?range=all", "/api/dashboard?tz=999999", "/api/dashboard?tz=x"} {
		if req(t, a, member, "GET", path, nil).Code != 400 {
			t.Fatal("invalid query accepted")
		}
	}
	if req(t, a, Record{}, "GET", "/api/dashboard", nil).Code != 401 {
		t.Fatal("unauthenticated dashboard")
	}
	r := req(t, a, member, "GET", "/api/dashboard?range=24h&tz=480", nil)
	if r.Code != 200 || strings.Contains(r.Body.String(), "private-token") || strings.Contains(r.Body.String(), `"public":`) || strings.Contains(r.Body.String(), `"subscriptions":`) || strings.Contains(r.Body.String(), `"username":"owner"`) {
		t.Fatal("global data leaked to member")
	}
	var data struct {
		Traffic DashboardTraffic `json:"traffic"`
	}
	if json.Unmarshal(r.Body.Bytes(), &data) != nil || data.Traffic.Upload != 10 || data.Traffic.Download != 20 || len(data.Traffic.Leaders) != 1 || data.Traffic.Leaders[0].UserID != member.ID {
		t.Fatal("member statistics not scoped")
	}
	r = req(t, a, owner, "GET", "/api/dashboard?range=24h", nil)
	if r.Code != 200 || strings.Contains(r.Body.String(), "private-token") || !strings.Contains(r.Body.String(), `"subscriptions":`) {
		t.Fatal("owner summary missing or leaks source credentials")
	}
}
func TestDashboardEmptyHistoryAndCompleteComparison(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	now := time.Now().Unix()
	end := (now/3600 + 1) * 3600
	empty, err := a.store.dashboardTraffic(owner, "24h", 0, now)
	if err != nil || len(empty.Buckets) != 24 || empty.ComparisonAvailable || empty.FirstRecord != 0 || len(empty.Leaders) != 0 {
		t.Fatal("empty chart fabricates history")
	}
	putTraffic(t, a, owner, end-48*3600, 20, 30)
	putTraffic(t, a, owner, end-3600, 40, 60)
	d, err := a.store.dashboardTraffic(owner, "24h", 0, now)
	if err != nil || !d.ComparisonAvailable || d.Previous != 50 || d.Upload+d.Download != 100 {
		t.Fatal("equal-period comparison is incorrect")
	}
}
