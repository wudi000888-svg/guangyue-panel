package controlplane

import (
	"encoding/json"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

// syncActivePlanEntitlements updates the policy portion of active package
// assignments when an administrator edits a plan.  Entitlements used to be
// copied only at assignment time, which meant an existing subscriber kept the
// old GroupIDs/NodeIDs until they bought the package again.  Keep quota,
// expiry, and the purchased package name immutable, but make the node policy
// live and advance the entitlement revision so downstream mount sync sees it.
//
// This runs in the same transaction as the plan update.  That prevents a
// subscription request from observing the new plan together with an old
// entitlement (or vice versa).
func syncActivePlanEntitlements(tx *persistence.Tx, p Plan) (int, error) {
	rows, err := tx.Query("SELECT id,doc FROM users")
	if err != nil {
		return 0, err
	}
	type row struct {
		id   int64
		user User
	}
	var users []row
	for rows.Next() {
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		var user User
		if err = json.Unmarshal(raw, &user); err != nil {
			rows.Close()
			return 0, err
		}
		users = append(users, row{id: id, user: user})
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}

	updated := 0
	for _, item := range users {
		u := item.user
		changed := false
		if u.Entitlement != nil && u.Entitlement.PlanID == p.ID {
			// Node policy and protocol flags are the parts of a plan that control
			// what an already-issued subscription can receive. Billing fields
			// (quota/expiry) stay attached to the user's purchase snapshot.
			groupsChanged := !sameStrings(u.Entitlement.GroupIDs, p.GroupIDs)
			nodesChanged := !sameStrings(u.Entitlement.NodeIDs, p.NodeIDs)
			protocolChanged := u.VLESS != p.VLESS || u.HY2 != p.HY2
			// A plan version can also advance for billing, naming, or copy edits.
			// Those changes must keep the purchased snapshot intact. Only a node
			// policy/protocol change is live-propagated to current subscribers.
			if groupsChanged || nodesChanged || protocolChanged {
				u.Entitlement.GroupIDs = append([]string{}, p.GroupIDs...)
				u.Entitlement.NodeIDs = append([]string{}, p.NodeIDs...)
				u.Entitlement.Version = p.Version
				u.VLESS, u.HY2 = p.VLESS, p.HY2
				u.Entitlement.Revision = randomToken(12)
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, e := json.Marshal(u)
		if e != nil {
			return 0, e
		}
		if _, e = tx.Exec("UPDATE users SET doc=? WHERE id=?", encoded, item.id); e != nil {
			return 0, e
		}
		updated++
	}
	return updated, nil
}

