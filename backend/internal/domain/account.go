// Package domain defines shared account and authorization policy.
package domain

import "time"

type User struct {
	Archived       bool         `json:"archived,omitempty"`
	ID             int64        `json:"id"`
	Username       string       `json:"username"`
	Role           string       `json:"role"`
	Enabled        bool         `json:"enabled"`
	VLESS          bool         `json:"vless"`
	HY2            bool         `json:"hy2"`
	Expires        int64        `json:"expires"`
	Quota          int64        `json:"quota"`
	Upload         int64        `json:"upload"`
	Download       int64        `json:"download"`
	VLESSTraffic   int64        `json:"vless_traffic"`
	HY2Traffic     int64        `json:"hy2_traffic"`
	Created        int64        `json:"created"`
	LastSub        int64        `json:"last_sub"`
	CompiledGroups *[]string    `json:"compiled_groups,omitempty"`
	Meter          *QuotaMeter  `json:"meter,omitempty"`
	Entitlement    *Entitlement `json:"entitlement,omitempty"`
}

func (u User) Active() bool {
	return u.Enabled && (u.Expires == 0 || u.Expires > time.Now().Unix()) && (u.Meter == nil || !u.Meter.PendingReset && (u.Meter.End == 0 || u.Meter.End > time.Now().Unix())) && (u.Quota == 0 || u.QuotaUsed() < u.Quota)
}

type Credentials struct {
	PublicToken  string            `json:"public_token"`
	HYGeneration uint64            `json:"hy_generation"`
	HY2          string            `json:"hy2"`
	Token        string            `json:"token"`
	VLESS        map[string]string `json:"vless"`
}
type Record struct {
	User
	Credentials    Credentials
	Password       []byte
	AccessResolved bool
	AllowedGroups  []string
}
