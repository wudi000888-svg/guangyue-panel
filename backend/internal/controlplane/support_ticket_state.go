package controlplane

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	ticketStateOpen        = "open"
	ticketStateProcessing  = "processing"
	ticketStateWaitingUser = "waiting_user"
	ticketStateResolved    = "resolved"
	ticketStateClosed      = "closed"
)

var ticketStates = map[string]struct{}{
	ticketStateOpen: {}, ticketStateProcessing: {}, ticketStateWaitingUser: {},
	ticketStateResolved: {}, ticketStateClosed: {},
}

// ticketReplyState centralizes the customer-visible workflow. Internal notes
// are audit entries and deliberately leave the visible state untouched.
func ticketReplyState(role, current string, internal bool) string {
	if internal {
		return current
	}
	if role == "owner" {
		return ticketStateWaitingUser
	}
	return ticketStateProcessing
}

func ticketCreateState(_ string) string {
	return ticketStateProcessing
}

func ticketStateIsClosed(state string) bool {
	return state == ticketStateResolved || state == ticketStateClosed
}

func ticketStateFilter(raw string) ([]string, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "all":
		return nil, true
	case "active":
		return []string{ticketStateOpen, ticketStateProcessing}, true
	case "done":
		return []string{ticketStateResolved, ticketStateClosed}, true
	case ticketStateOpen, ticketStateProcessing, ticketStateWaitingUser, ticketStateResolved, ticketStateClosed:
		return []string{strings.TrimSpace(strings.ToLower(raw))}, true
	default:
		return nil, false
	}
}

func ticketSearchText(t Ticket) string {
	return strings.ToLower(strings.Join([]string{
		t.Title, t.ID, t.OrderID, t.Resource, strconv.FormatInt(t.UserID, 10),
	}, " "))
}

func ticketMatchesSearch(t Ticket, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	return query == "" || strings.Contains(ticketSearchText(t), query)
}

// supportTicketPage applies filters and the opaque id cursor on the server.
// Search is evaluated after decoding each bounded DB batch so it remains
// portable across SQLite and PostgreSQL's different BLOB/text operators.
func (a *App) supportTicketPage(actor Record, states []string, search, before string) ([]Ticket, string, bool, error) {
	const pageSize = 50
	const batchSize = 100
	search = strings.TrimSpace(search)
	cursor := strings.TrimSpace(before)
	pageCursor := ""
	out := []Ticket{}
	for {
		clauses := []string{}
		args := []any{}
		if cursor != "" {
			clauses = append(clauses, "id<?")
			args = append(args, cursor)
		}
		if actor.Role != "owner" {
			clauses = append(clauses, "user_id=?")
			args = append(args, actor.ID)
		}
		if len(states) > 0 {
			marks := make([]string, len(states))
			for i, state := range states {
				marks[i] = "?"
				args = append(args, state)
			}
			clauses = append(clauses, "state IN ("+strings.Join(marks, ",")+")")
		}
		query := "SELECT id,doc FROM support_tickets"
		if len(clauses) > 0 {
			query += " WHERE " + strings.Join(clauses, " AND ")
		}
		query += " ORDER BY id DESC LIMIT " + strconv.Itoa(batchSize)
		rows, e := a.store.db.Query(query, args...)
		if e != nil {
			return nil, "", false, e
		}
		consumed := 0
		lastCursor := ""
		hasMore := false
		for rows.Next() {
			var id string
			var b []byte
			if e = rows.Scan(&id, &b); e != nil {
				rows.Close()
				return nil, "", false, e
			}
			consumed++
			lastCursor = id
			var t Ticket
			if e = json.Unmarshal(b, &t); e != nil {
				rows.Close()
				return nil, "", false, e
			}
			if !ticketMatchesSearch(t, search) {
				continue
			}
			if len(out) < pageSize {
				out = append(out, t)
				pageCursor = id
				continue
			}
			// One matching look-ahead proves that the returned cursor can be
			// followed without dropping a matching record.
			hasMore = true
			break
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, "", false, rowErr
		}
		if hasMore {
			return out, pageCursor, true, nil
		}
		if len(out) >= pageSize {
			// The batch ended exactly at the page boundary. Continue from the
			// physical batch cursor to find a matching look-ahead.
			if consumed < batchSize {
				return out, pageCursor, false, nil
			}
			cursor = lastCursor
			continue
		}
		if consumed < batchSize || lastCursor == "" {
			return out, "", false, nil
		}
		cursor = lastCursor
	}
}
