package controlplane

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"html/template"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

type emailMessage struct {
	ID      string
	To      string
	Subject string
	Text    string
	HTML    string
}
type EmailDelivery struct {
	ID          string `json:"id"`
	To          string `json:"to"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt"`
	LastError   string `json:"last_error"`
	Created     int64  `json:"created"`
	SentAt      int64  `json:"sent_at"`
}

var emailTemplate = template.Must(template.New("mail").Parse(`<!doctype html><html lang="zh-CN"><body><h1>{{.Subject}}</h1><p style="white-space:pre-line">{{.Text}}</p>{{if .URL}}<p><a href="{{.URL}}">打开面板完成操作</a></p><p>如非本人操作，请忽略此邮件。</p>{{end}}</body></html>`))

func buildEmail(id, to, subject, text, link string) (emailMessage, error) {
	var b bytes.Buffer
	err := emailTemplate.Execute(&b, struct{ Subject, Text, URL string }{subject, text, link})
	plain := text
	if link != "" {
		plain += "\n\n" + link + "\n\n如非本人操作，请忽略此邮件。"
	}
	return emailMessage{id, to, subject, plain, b.String()}, err
}
func (s *Store) insertEmail(tx *persistence.Tx, id string, user int64, to, kind, subject, text, link string, expires, now int64) error {
	msg, err := buildEmail(id, to, subject, text, link)
	if err != nil {
		return err
	}
	body, err := s.vault.seal(msg)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO email_outbox(id,user_id,recipient,kind,body,next_attempt,expires,created) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", id, user, to, kind, body, now, expires, now)
	return err
}
func (s *Store) queueEmail(user int64, to, kind, subject, text, link string, expires int64) (string, error) {
	id := "mail-" + randomToken(18)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = s.insertEmail(tx, id, user, to, kind, subject, text, link, expires, time.Now().Unix()); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (s *Store) emailDeliveries() ([]EmailDelivery, error) {
	rows, err := s.db.Query("SELECT id,recipient,kind,state,attempts,next_attempt,last_error,created,sent_at FROM email_outbox ORDER BY created DESC,id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EmailDelivery{}
	for rows.Next() {
		var v EmailDelivery
		if err = rows.Scan(&v.ID, &v.To, &v.Kind, &v.State, &v.Attempts, &v.NextAttempt, &v.LastError, &v.Created, &v.SentAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) retryEmail(id string, now int64) error {
	res, err := s.db.Exec("UPDATE email_outbox SET state='pending',attempts=0,next_attempt=?,last_error='',lease_token='',lease_until=0 WHERE id=? AND state IN ('failed','retry') AND (expires=0 OR expires>?)", now, id, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return commerceFail(409, "邮件不可重试，可能已发送、已过期或正在投递")
	}
	return nil
}
func (s *Store) captureEmailEvents(settings EmailSettings, now int64) error {
	if !settings.OrderNotifications {
		return nil
	}
	rows, err := s.db.Query("SELECT id,user_id,body,created FROM commerce_events WHERE created>=? AND (id LIKE 'order:%:completed' OR id LIKE 'order:%:refunded') AND NOT EXISTS(SELECT 1 FROM email_event_receipts WHERE event_id=commerce_events.id) ORDER BY created,id LIMIT 50", settings.EventsAfter)
	if err != nil {
		return err
	}
	type event struct {
		id      string
		user    int64
		body    string
		created int64
	}
	events := []event{}
	for rows.Next() {
		var v event
		if err = rows.Scan(&v.id, &v.user, &v.body, &v.created); err != nil {
			rows.Close()
			return err
		}
		events = append(events, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, v := range events {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		err = func() error {
			defer tx.Rollback()
			var email string
			err := tx.QueryRow("SELECT email FROM email_accounts WHERE user_id=? AND verified_at<=?", v.user, v.created).Scan(&email)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			state := "skipped"
			if email != "" {
				state = "queued"
				subject := "套餐订单已开通"
				if strings.HasSuffix(v.id, ":refunded") {
					subject = "订单退款已完成"
				}
				if err = s.insertEmail(tx, "event-"+digest(v.id), v.user, email, "order", subject, v.body, "", 0, now); err != nil {
					return err
				}
			}
			if _, err = tx.Exec("INSERT INTO email_event_receipts(event_id,state,created) VALUES(?,?,?) ON CONFLICT(event_id) DO NOTHING", v.id, state, now); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// Claims are atomic and durable. SMTP cannot promise exactly-once delivery after
// an accepted DATA response is lost, so retries reuse a stable Message-ID.
func (s *Store) processEmail(ctx context.Context, now int64, send func(context.Context, EmailSettings, emailMessage) error) (bool, error) {
	settings, err := s.emailSettings()
	if err != nil || !emailReady(settings) {
		return false, err
	}
	if _, err = s.db.Exec("UPDATE email_outbox SET state='expired',lease_token='',lease_until=0 WHERE state IN ('pending','retry','sending') AND expires>0 AND expires<=? AND lease_until<=?", now, now); err != nil {
		return false, err
	}
	if _, err = s.db.Exec("UPDATE email_outbox SET state='failed',last_error='投递进程中断，重试次数已用尽',lease_token='',lease_until=0 WHERE state='sending' AND lease_until<=? AND attempts>=5", now); err != nil {
		return false, err
	}
	var id string
	err = s.db.QueryRow("SELECT id FROM email_outbox WHERE attempts<5 AND ((state IN ('pending','retry') AND next_attempt<=?) OR (state='sending' AND lease_until<=?)) ORDER BY created,id LIMIT 1", now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lease := randomToken(24)
	res, err := s.db.Exec("UPDATE email_outbox SET state='sending',attempts=attempts+1,lease_token=?,lease_until=? WHERE id=? AND attempts<5 AND ((state IN ('pending','retry') AND next_attempt<=?) OR (state='sending' AND lease_until<=?))", lease, now+120, id, now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	var body []byte
	var attempts int
	var user int64
	var recipient, kind string
	if err = s.db.QueryRow("SELECT body,attempts,user_id,recipient,kind FROM email_outbox WHERE id=? AND lease_token=?", id, lease).Scan(&body, &attempts, &user, &recipient, &kind); err != nil {
		return true, err
	}
	if kind == "order" {
		var count int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM email_accounts WHERE user_id=? AND email=?", user, recipient).Scan(&count); err != nil {
			return true, err
		}
		_, eligibleErr := s.eligibleEmailUser(user)
		if count != 1 || eligibleErr != nil {
			_, err = s.db.Exec("UPDATE email_outbox SET state='cancelled',last_error='收件邮箱已变更或账户不可用',lease_token='',lease_until=0 WHERE id=? AND lease_token=?", id, lease)
			return true, err
		}
	}
	var msg emailMessage
	err = s.vault.open(body, &msg)
	if err == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err = send(attemptCtx, settings, msg)
		cancel()
	}
	if err == nil {
		_, err = s.db.Exec("UPDATE email_outbox SET state='sent',sent_at=?,last_error='',lease_token='',lease_until=0 WHERE id=? AND lease_token=?", time.Now().Unix(), id, lease)
		return true, err
	}
	state := "retry"
	if attempts >= 5 {
		state = "failed"
	}
	delay := int64(30) << (attempts - 1)
	_, dbErr := s.db.Exec("UPDATE email_outbox SET state=?,next_attempt=?,last_error=?,lease_token='',lease_until=0 WHERE id=? AND lease_token=?", state, now+delay, emailFailure(err), id, lease)
	return true, dbErr
}
func (a *App) emailTick(ctx context.Context) error {
	settings, err := a.store.emailSettings()
	if err != nil || !emailReady(settings) {
		return err
	}
	now := time.Now().Unix()
	if err = a.store.captureEmailEvents(settings, now); err != nil {
		return err
	}
	for i := 0; i < 5 && ctx.Err() == nil; i++ {
		found, err := a.store.processEmail(ctx, time.Now().Unix(), func(ctx context.Context, v EmailSettings, msg emailMessage) error { return sendSMTP(ctx, v, msg, nil) })
		if err != nil {
			return err
		}
		if !found {
			break
		}
	}
	_, err = a.store.db.Exec("DELETE FROM email_tokens WHERE expires<?", now-86400)
	if err != nil {
		return err
	}
	_, err = a.store.db.Exec("DELETE FROM email_rate_limits WHERE bucket<?", now/600-144)
	return err
}
func (a *App) runEmailWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.emailTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
