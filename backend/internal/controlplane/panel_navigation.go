package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
)

type navigationSettings struct {
	Role         string   `json:"role"`
	Items        []string `json:"items"`
	Revision     string   `json:"revision"`
	SiteRevision string   `json:"site_revision"`
}

func sidebarDefinition(role string) (defaults, permitted []string, ok bool) {
	switch role {
	case "admin":
		return []string{"overview", "users", "plans", "nodes", "fleet", "settings"},
			[]string{"overview", "monitor", "users", "plans", "shop", "subscription", "wallet", "orders", "redeem-codes", "nodes", "subsite-nodes", "node-groups", "ips", "public", "public-nodes", "public-subscription", "fleet", "pairing", "tickets", "messages", "clients", "tasks", "system", "settings"}, true
	case "member":
		return []string{"subscription", "shop", "orders", "settings"},
			[]string{"overview", "subscription", "shop", "wallet", "orders", "tickets", "messages", "clients", "settings"}, true
	default:
		return nil, nil, false
	}
}

func navigationFromSettings(v SiteSettings, role string) (navigationSettings, error) {
	defaults, permitted, ok := sidebarDefinition(role)
	if !ok {
		return navigationSettings{}, errors.New("请选择管理员或成员侧边栏")
	}
	items := v.SidebarAdmin
	if role == "member" {
		items = v.SidebarMember
	}
	items, err := normalizeSidebar(items, defaults, permitted)
	if err != nil {
		return navigationSettings{}, err
	}
	encoded, _ := json.Marshal(items)
	return navigationSettings{Role: role, Items: items, Revision: digest(role + ":" + string(encoded)), SiteRevision: v.Revision}, nil
}

// Compare the complete persisted document so merging one navigation role never
// overwrites a simultaneous change to another role or unrelated site settings.
func (s *Store) compareAndSwapSiteSettings(previous string, next SiteSettings) (bool, error) {
	b, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	query := "UPDATE meta SET value=? WHERE key='site_settings' AND value=?"
	if previous == "" {
		query = "INSERT INTO meta(key,value) VALUES('site_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE meta.value=?"
	}
	result, err := s.db.Exec(query, string(b), previous)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (a *App) panelNavigation(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.Role != "owner" {
		failure(w, 403, "需要管理员权限")
		return
	}
	var in struct {
		Role     string   `json:"role"`
		Items    []string `json:"items"`
		Revision string   `json:"revision"`
	}
	if r.Method == "GET" {
		in.Role = r.URL.Query().Get("role")
	} else if !decode(w, r, &in) {
		return
	}
	defaults, permitted, ok := sidebarDefinition(in.Role)
	if !ok {
		failure(w, 400, "请选择管理员或成员侧边栏")
		return
	}
	if r.Method == "PUT" {
		if in.Items == nil || in.Revision == "" {
			failure(w, 400, "请提供侧边栏入口及当前版本")
			return
		}
		seen := map[string]bool{}
		for _, item := range in.Items {
			if seen[item] {
				failure(w, 400, "侧边栏入口不能重复")
				return
			}
			seen[item] = true
		}
		var err error
		in.Items, err = normalizeSidebar(in.Items, defaults, permitted)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for attempt := 0; attempt < 4; attempt++ {
		raw, err := a.store.readMeta("site_settings")
		if err != nil {
			failure(w, 500, "读取系统设置失败")
			return
		}
		settings, err := a.store.parseSiteSettings(raw)
		if err != nil {
			failure(w, 500, "读取系统设置失败")
			return
		}
		current, err := navigationFromSettings(settings, in.Role)
		if err != nil {
			failure(w, 500, "读取侧边栏设置失败")
			return
		}
		if r.Method == "GET" {
			jsonResponse(w, 200, current)
			return
		}
		if in.Revision != current.Revision {
			failure(w, 409, "此侧边栏已被其他管理员修改，请重新加载")
			return
		}
		if slices.Equal(in.Items, current.Items) {
			jsonResponse(w, 200, current)
			return
		}
		if in.Role == "admin" {
			settings.SidebarAdmin = in.Items
		} else {
			settings.SidebarMember = in.Items
		}
		settings.Revision = randomToken(12)
		saved, err := a.store.compareAndSwapSiteSettings(raw, settings)
		if err != nil {
			failure(w, 500, "保存侧边栏设置失败")
			return
		}
		if !saved {
			continue
		}
		result, _ := navigationFromSettings(settings, in.Role)
		a.store.audit(actor.Username, "update-navigation", in.Role)
		jsonResponse(w, 200, result)
		return
	}
	failure(w, 409, "设置正在被其他管理员修改，请重新加载后重试")
}
