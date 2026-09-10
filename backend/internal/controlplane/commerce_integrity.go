package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Full ledger reconciliation runs at startup and before restoring a backup.
// Normal writes validate the preceding wallet checkpoint under the wallet lock.
func (s *Store) validateCommerce(deep bool) error {
	checks := []string{
		"SELECT COUNT(*) FROM (SELECT t.id FROM money_transactions t LEFT JOIN money_entries e ON e.transaction_id=t.id GROUP BY t.id HAVING COUNT(e.account)<2 OR COALESCE(SUM(e.amount),1)<>0) bad",
		"SELECT COUNT(*) FROM wallet_accounts w LEFT JOIN users u ON u.id=w.user_id WHERE u.id IS NULL OR w.available<0 OR w.held<0 OR w.available+w.held>100000000000 OR w.revision<>(SELECT COUNT(*) FROM money_transactions t WHERE t.user_id=w.user_id)",
		"SELECT COUNT(*) FROM money_transactions t LEFT JOIN wallet_accounts w ON t.user_id=w.user_id WHERE w.user_id IS NULL",
		"SELECT COUNT(*) FROM money_entries e LEFT JOIN money_transactions t ON t.id=e.transaction_id WHERE t.id IS NULL",
		"SELECT COUNT(*) FROM support_tickets t LEFT JOIN users u ON t.user_id=u.id WHERE u.id IS NULL",
		"SELECT COUNT(*) FROM support_replies r LEFT JOIN support_tickets t ON r.ticket_id=t.id WHERE t.id IS NULL",
		"SELECT COUNT(*) FROM support_attachments a LEFT JOIN support_replies r ON a.reply_id=r.id WHERE a.ticket_id<>'' AND (r.id IS NULL OR a.ticket_id<>r.ticket_id OR a.internal<>r.internal OR a.user_id<>r.user_id)",
	}
	for _, q := range checks {
		var n int
		if e := s.db.QueryRow(q).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return errors.New("commerce records failed integrity check")
		}
	}
	// One row per wallet keeps memory independent of ledger and image history size.
	rows, e := s.db.Query("SELECT user_id,available,held FROM wallet_accounts")
	if e != nil {
		return e
	}
	wallets := []Wallet{}
	for rows.Next() {
		var w Wallet
		if e = rows.Scan(&w.UserID, &w.Available, &w.Held); e != nil {
			rows.Close()
			return e
		}
		wallets = append(wallets, w)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	heldOrders := map[int64]int64{}
	rows, e = s.db.Query("SELECT id,user_id,state,doc FROM commerce_orders")
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, state string
		var user int64
		var b []byte
		var o Order
		if e = rows.Scan(&id, &user, &state, &b); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(b, &o); e != nil {
			rows.Close()
			return e
		}
		if o.ID != id || o.UserID != user || o.State != state || o.Offer.Price <= 0 || o.Offer.Price > moneyLimit {
			rows.Close()
			return errors.New("order snapshot integrity failed")
		}
		if state == "provisioning" {
			heldOrders[user] += o.Offer.Price
		}
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, w := range wallets {
		for account, want := range map[string]int64{"available": w.Available, "held": w.Held} {
			var got int64
			if e = s.db.QueryRow("SELECT COALESCE(SUM(amount),0) FROM money_entries WHERE account=?", fmt.Sprintf("user:%d:%s", w.UserID, account)).Scan(&got); e != nil {
				return e
			}
			if got != want {
				return errors.New("wallet does not match ledger")
			}
		}
		if heldOrders[w.UserID] != w.Held {
			return errors.New("held balance does not match orders")
		}
		delete(heldOrders, w.UserID)
	}
	if len(heldOrders) != 0 {
		return errors.New("order wallet missing")
	}
	if !deep {
		return nil
	}
	for _, table := range []string{"commerce_requests", "support_attachments"} {
		field := "response"
		if table == "support_attachments" {
			field = "body"
		}
		rows, e = s.db.Query("SELECT " + field + " FROM " + table)
		if e != nil {
			return e
		}
		for rows.Next() {
			var b []byte
			var out json.RawMessage
			if e = rows.Scan(&b); e == nil {
				e = s.vault.open(b, &out)
			}
			if e != nil {
				rows.Close()
				return e
			}
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return e
		}
	}
	return nil
}
