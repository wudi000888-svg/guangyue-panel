package controlplane

import (
	"net/url"
	"strconv"
	"testing"
)

func TestCommerceOrderExactLookupPreservesScope(t *testing.T) {
	a := testApp(t)
	owner := testUser(t, a, "owner", "owner")
	alice := testUser(t, a, "alice", "user")
	bob := testUser(t, a, "bob", "user")
	order := newOrder(t, a, alice, commerceOffer(t, a, owner))

	// Fill the newest page without invoking checkout 50 times. The target stays
	// pending so subsequent exact reads can also represent a status refresh.
	for i := 0; i < 50; i++ {
		newer := order
		newer.ID += "-" + strconv.Itoa(i)
		newer.State = "completed"
		if _, err := a.store.db.Exec("INSERT INTO commerce_orders(id,user_id,state,created,updated,expires,doc) VALUES(?,?,?,?,?,?,?)", newer.ID, newer.UserID, newer.State, newer.Created, newer.Updated, newer.Expires, jsonBytes(newer)); err != nil {
			t.Fatal(err)
		}
	}
	type listing struct {
		Items []Order `json:"items"`
	}
	first := decoded[listing](t, req(t, a, alice, "GET", "/api/commerce/orders", nil), 200)
	if len(first.Items) != 50 {
		t.Fatalf("expected a full first page, got %d", len(first.Items))
	}
	for _, item := range first.Items {
		if item.ID == order.ID {
			t.Fatal("fixture target should be outside the first page")
		}
	}
	for _, test := range []struct {
		name   string
		actor  Record
		id     string
		query  string
		status int
		found  bool
	}{
		{"own older order", alice, order.ID, "", 200, true},
		{"another member", bob, order.ID, "", 200, false},
		{"member cannot use all scope", bob, order.ID, "&all=1", 200, false},
		{"member cannot choose another user", bob, order.ID, "&user_id=" + strconv.FormatInt(alice.ID, 10), 403, false},
		{"owner defaults to own scope", owner, order.ID, "", 200, false},
		{"owner all scope", owner, order.ID, "&all=1", 200, true},
		{"owner selected member", owner, order.ID, "&user_id=" + strconv.FormatInt(alice.ID, 10), 200, true},
		{"missing order", alice, "missing", "", 200, false},
		{"id remains a literal", alice, order.ID + "' OR 1=1 --", "", 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := req(t, a, test.actor, "GET", "/api/commerce/orders?id="+url.QueryEscape(test.id)+test.query, nil)
			if response.Code != test.status {
				t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
			}
			if test.status != 200 {
				return
			}
			result := decoded[listing](t, response, 200)
			if test.found {
				if len(result.Items) != 1 || result.Items[0].ID != order.ID || result.Items[0].State != "pending" {
					t.Fatalf("exact order missing: %+v", result.Items)
				}
			} else if len(result.Items) != 0 {
				t.Fatalf("unexpected order exposure: %+v", result.Items)
			}
		})
	}
}
