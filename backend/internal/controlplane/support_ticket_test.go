package controlplane

import (
	"fmt"
	"net/http"
	"testing"
)

func createSupportTicket(t *testing.T, a *App, actor Record, title string) Ticket {
	t.Helper()
	return decoded[Ticket](t, req(t, a, actor, "POST", "/api/support/tickets", object{
		"title": title, "category": "other", "body": "Please help", "operation_id": randomToken(24),
	}), 201)
}

func TestSupportTicketReplyStatePolicy(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")

	ticket := createSupportTicket(t, a, member, "State policy")
	if ticket.State != "processing" {
		t.Fatalf("new member ticket state = %q, want processing", ticket.State)
	}
	ownerReply := decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{
		"id": ticket.ID, "revision": ticket.Revision, "body": "We are checking this", "operation_id": randomToken(24),
	}), 200)
	if ownerReply.State != "waiting_user" {
		t.Fatalf("owner reply state = %q, want waiting_user", ownerReply.State)
	}
	memberReply := decoded[Ticket](t, req(t, a, member, "POST", "/api/support/reply", object{
		"id": ticket.ID, "revision": ownerReply.Revision, "body": "More details", "operation_id": randomToken(24),
	}), 200)
	if memberReply.State != "processing" {
		t.Fatalf("member reply state = %q, want processing", memberReply.State)
	}
	internal := decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{
		"id": ticket.ID, "revision": memberReply.Revision, "body": "Internal handoff", "internal": true, "operation_id": randomToken(24),
	}), 200)
	if internal.State != memberReply.State {
		t.Fatalf("internal reply changed state from %q to %q", memberReply.State, internal.State)
	}
	if internal.Revision == memberReply.Revision {
		t.Fatal("internal reply must still advance revision for stale write protection")
	}
}

func TestSupportTicketStateRevisionRejectsStaleMutation(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")
	ticket := createSupportTicket(t, a, member, "Revision")
	stale := ticket.Revision
	updated := decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{
		"id": ticket.ID, "revision": stale, "body": "A reply changes the revision", "operation_id": randomToken(24),
	}), 200)
	if updated.Revision == stale {
		t.Fatal("reply did not advance ticket revision")
	}
	if got := req(t, a, owner, "POST", "/api/support/state", object{
		"id": ticket.ID, "revision": stale, "state": "processing",
	}); got.Code != http.StatusConflict {
		t.Fatalf("stale state mutation status = %d, want 409", got.Code)
	}
	if got := req(t, a, member, "POST", "/api/support/reply", object{
		"id": ticket.ID, "revision": stale, "body": "stale reply", "operation_id": randomToken(24),
	}); got.Code != http.StatusConflict {
		t.Fatalf("stale reply status = %d, want 409", got.Code)
	}
	if got := req(t, a, member, "POST", "/api/support/reply", object{
		"id": ticket.ID, "body": "missing revision", "operation_id": randomToken(24),
	}); got.Code != http.StatusConflict {
		t.Fatalf("missing reply revision status = %d, want 409", got.Code)
	}
}

func TestSupportTicketListFiltersSearchesAndPaginatesOnServer(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	member := testUser(t, a, "member", "user")

	needle := createSupportTicket(t, a, member, "Needle beyond first page")
	waiting := createSupportTicket(t, a, member, "Waiting ticket")
	waiting = decoded[Ticket](t, req(t, a, owner, "POST", "/api/support/reply", object{
		"id": waiting.ID, "revision": waiting.Revision, "body": "Waiting for your reply", "operation_id": randomToken(24),
	}), 200)
	if waiting.State != "waiting_user" {
		t.Fatalf("fixture waiting state = %q", waiting.State)
	}
	for i := 0; i < 55; i++ {
		createSupportTicket(t, a, owner, fmt.Sprintf("Generic ticket %02d", i))
	}

	var result struct {
		Items   []Ticket `json:"items"`
		Next    string   `json:"next_before"`
		HasMore bool     `json:"has_more"`
	}
	result = decoded[struct {
		Items   []Ticket `json:"items"`
		Next    string   `json:"next_before"`
		HasMore bool     `json:"has_more"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets?search=Needle", nil), 200)
	if len(result.Items) != 1 || result.Items[0].ID != needle.ID {
		t.Fatalf("server search result = %+v, want needle ticket", result.Items)
	}
	filtered := decoded[struct {
		Items []Ticket `json:"items"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets?state=waiting_user", nil), 200)
	if len(filtered.Items) != 1 || filtered.Items[0].ID != waiting.ID {
		t.Fatalf("state filter = %+v, want waiting ticket", filtered.Items)
	}
	page := decoded[struct {
		Items   []Ticket `json:"items"`
		Next    string   `json:"next_before"`
		HasMore bool     `json:"has_more"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets", nil), 200)
	if len(page.Items) != 50 || page.Next == "" || !page.HasMore {
		t.Fatalf("first page metadata = len %d next %q more %v", len(page.Items), page.Next, page.HasMore)
	}
	page2 := decoded[struct {
		Items   []Ticket `json:"items"`
		Next    string   `json:"next_before"`
		HasMore bool     `json:"has_more"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets?before="+page.Next, nil), 200)
	if len(page2.Items) == 0 || page2.Items[0].ID == page.Items[len(page.Items)-1].ID {
		t.Fatalf("cursor page did not advance: first=%q second=%q", page.Items[len(page.Items)-1].ID, page2.Items[0].ID)
	}
}

func TestSupportTicketPaginationKeepsExactBatchBoundary(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	// The first physical batch contains exactly 50 search hits. The 101st
	// physical row is also a hit, so the page implementation must retain the
	// first batch while looking ahead rather than replacing it.
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("GYBOUND%03d", i)
		title := "Other ticket"
		if i%2 == 0 {
			title = "Needle boundary"
		}
		ticket := Ticket{ID: id, UserID: owner.ID, Title: title, Category: "other", State: ticketStateProcessing, Priority: "normal", Created: int64(i + 1), Updated: int64(i + 1), Revision: fmt.Sprintf("rev-%03d", i)}
		if _, err := a.store.db.Exec("INSERT INTO support_tickets(id,user_id,state,created,updated,doc) VALUES(?,?,?,?,?,?)", id, owner.ID, ticket.State, ticket.Created, ticket.Updated, jsonBytes(ticket)); err != nil {
			t.Fatal(err)
		}
	}
	first := decoded[struct {
		Items []Ticket `json:"items"`
		Next  string   `json:"next_before"`
		More  bool     `json:"has_more"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets?search=Needle", nil), 200)
	if len(first.Items) != 50 || first.Next == "" || !first.More {
		t.Fatalf("boundary first page = len %d next %q more %v", len(first.Items), first.Next, first.More)
	}
	second := decoded[struct {
		Items []Ticket `json:"items"`
		More  bool     `json:"has_more"`
	}](t, req(t, a, owner, "GET", "/api/support/tickets?search=Needle&before="+first.Next, nil), 200)
	if len(second.Items) != 1 || second.Items[0].ID != "GYBOUND000" || second.More {
		t.Fatalf("boundary second page = %+v more %v", second.Items, second.More)
	}
}
