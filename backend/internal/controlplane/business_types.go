package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/httpapi"
)

const businessLeaseSeconds = 900
const businessMaxSites = 64

type BusinessInfo struct {
	SiteID        string `json:"site_id"`
	Version       string `json:"version"`
	Protocol      int    `json:"protocol"`
	VLESSHost     string `json:"vless_host"`
	HY2Host       string `json:"hy2_host"`
	RealityPublic string `json:"reality_public"`
	RealitySNI    string `json:"reality_sni"`
	ShortID       string `json:"short_id"`
}
type BusinessGrant struct {
	UserID int64 `json:"user_id"`
	Quota  int64 `json:"quota"`
}
type BusinessUsage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	VLESS    int64 `json:"vless"`
	HY2      int64 `json:"hy2"`
}

func (v BusinessUsage) total() int64 { return v.Upload + v.Download }

type BusinessCommand struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	NodeID  string `json:"node_id"`
	Created int64  `json:"created"`
	State   string `json:"state"`
}
type BusinessSite struct {
	Commands          []BusinessCommand       `json:"commands,omitempty"`
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Group             string                  `json:"group"`
	Enabled           bool                    `json:"enabled"`
	OwnerID           int64                   `json:"owner_id"`
	Created           int64                   `json:"created"`
	EnrollmentExpires int64                   `json:"enrollment_expires"`
	PendingToken      string                  `json:"pending_token,omitempty"`
	Info              *BusinessInfo           `json:"info,omitempty"`
	Nodes             []Node                  `json:"nodes"`
	Grants            []BusinessGrant         `json:"grants"`
	Exclusive         bool                    `json:"exclusive"`
	Revision          string                  `json:"revision"`
	LastSeen          int64                   `json:"last_seen"`
	Applied           string                  `json:"applied"`
	Desired           string                  `json:"desired"`
	LeaseUntil        int64                   `json:"lease_until"`
	Error             string                  `json:"error"`
	Reports           []Node                  `json:"reports,omitempty"`
	Usage             map[int64]BusinessUsage `json:"usage,omitempty"`
	Issued            map[int64]int64         `json:"issued,omitempty"`
	IssuedUnlimited   map[int64]bool          `json:"issued_unlimited,omitempty"`
	SentGrants        []BusinessGrant         `json:"sent_grants,omitempty"`
	IssuedNodes       []Node                  `json:"issued_nodes,omitempty"`
	SentNodes         []Node                  `json:"sent_nodes,omitempty"`
}

func (v *BusinessSite) defaults() {
	if v.Nodes == nil {
		v.Nodes = []Node{}
	}
	if v.Grants == nil {
		v.Grants = []BusinessGrant{}
	}
	if v.Usage == nil {
		v.Usage = map[int64]BusinessUsage{}
	}
	if v.Issued == nil {
		v.Issued = map[int64]int64{}
	}
	if v.IssuedUnlimited == nil {
		v.IssuedUnlimited = map[int64]bool{}
	}
}
func (v BusinessSite) public() BusinessSite {
	v.defaults()
	v.PendingToken = ""
	v.SentNodes = nil
	v.IssuedNodes = nil
	v.OwnerID = 0
	for i := range v.Reports {
		v.Reports[i] = v.Reports[i].public()
	}
	return v
}

type BusinessAccount struct {
	User        User        `json:"user"`
	Credentials Credentials `json:"credentials"`
}
type BusinessSnapshot struct {
	Commands     []BusinessCommand `json:"commands,omitempty"`
	SiteID       string            `json:"site_id"`
	Revision     string            `json:"revision"`
	IssuedAt     int64             `json:"issued_at"`
	LeaseSeconds int64             `json:"lease_seconds"`
	Nodes        []Node            `json:"nodes"`
	Users        []BusinessAccount `json:"users"`
	RuntimeMode  string            `json:"runtime_mode"`
}
type BusinessHeartbeat struct {
	Version  string                  `json:"version,omitempty"`
	Commands map[string]string       `json:"commands,omitempty"`
	SiteID   string                  `json:"site_id"`
	Applied  string                  `json:"applied"`
	Usage    map[int64]BusinessUsage `json:"usage"`
	Reports  []Node                  `json:"reports"`
	Error    string                  `json:"error"`
}

func validateControllerURL(address string, dev bool) error {
	return httpapi.ValidateEndpoint(address, dev)
}
func (s *Store) businessSite(id string) (BusinessSite, error) {
	var b []byte
	var v BusinessSite
	if err := s.db.QueryRow("SELECT doc FROM business_sites WHERE id=?", id).Scan(&b); err != nil {
		return v, err
	}
	err := s.vault.open(b, &v)
	v.defaults()
	return v, err
}
func (s *Store) businessSites() ([]BusinessSite, error) {
	rows, err := s.db.Query("SELECT doc FROM business_sites ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BusinessSite{}
	for rows.Next() {
		var b []byte
		var v BusinessSite
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = s.vault.open(b, &v); err != nil {
			return nil, err
		}
		v.defaults()
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) saveBusinessSite(v BusinessSite) error {
	b, err := s.vault.seal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE business_sites SET doc=? WHERE id=?", b, v.ID)
	return err
}
func businessSecret(seed, site, purpose string) string {
	h := hmac.New(sha256.New, []byte(seed))
	h.Write([]byte("guangyue/business/v1/" + site + "/" + purpose))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func businessRecord(r Record, site string, nodes []Node) Record {
	seed := businessSecret(r.Credentials.HY2, site, "account")
	r.Password = nil
	r.LastSub = 0
	r.Role = "user"
	r.Credentials = Credentials{HY2: seed, Token: businessSecret(seed, site, "local-token"), PublicToken: businessSecret(seed, site, "local-public"), VLESS: map[string]string{}}
	for _, n := range nodes {
		if n.Protocol == "vless" {
			h := sha256.Sum256([]byte(businessSecret(seed, site, n.ID)))
			h[6] = (h[6] & 15) | 64
			h[8] = (h[8] & 63) | 128
			x := hex.EncodeToString(h[:16])
			r.Credentials.VLESS[n.ID] = x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
		}
	}
	return r
}
func businessConfig(info BusinessInfo) Config {
	return Config{VLESSHost: info.VLESSHost, HY2Host: info.HY2Host, RealityPublic: info.RealityPublic, RealitySNI: info.RealitySNI, RealityTarget: info.RealitySNI + ":443", ShortID: info.ShortID}
}
func businessSnapshotRevision(v BusinessSnapshot) string {
	v.Revision = ""
	v.IssuedAt = 0
	v.LeaseSeconds = 0
	v.Commands = nil
	v.Users = append([]BusinessAccount{}, v.Users...)
	for i := range v.Users {
		v.Users[i].User.Upload = 0
		v.Users[i].User.Download = 0
		v.Users[i].User.VLESSTraffic = 0
		v.Users[i].User.HY2Traffic = 0
	}
	b, _ := json.Marshal(v)
	return digest(string(b))
}
func validBusinessInfo(v BusinessInfo) bool {
	key, err := base64.RawURLEncoding.DecodeString(v.RealityPublic)
	if err != nil || len(key) != 32 || v.Protocol != 1 || len(v.Version) > 32 || len(v.ShortID) != 16 {
		return false
	}
	if _, err = hex.DecodeString(v.ShortID); err != nil {
		return false
	}
	for _, host := range []string{v.VLESSHost, v.HY2Host, v.RealitySNI} {
		if !validHostname(host) {
			return false
		}
	}
	return true
}
func validHostname(s string) bool {
	if len(s) < 3 || len(s) > 253 || strings.ContainsAny(s, "/:@?#\\\r\n\t ") || !strings.Contains(s, ".") {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (a *App) businessOwnerEnabled(v BusinessSite) bool {
	u, e := a.store.record(v.OwnerID)
	return e == nil && u.Enabled && u.Role == "owner"
}

var errBusinessQuota = errors.New("业务站预分配额度超过用户剩余额度，请先调整站点额度")

func nowBusiness() int64 { return time.Now().Unix() }
