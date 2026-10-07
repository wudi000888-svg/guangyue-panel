package controlplane

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
)

func normalizeSidebar(items, defaults, permitted []string) ([]string, error) {
	if items == nil {
		items = defaults
	}
	if len(items) > 40 {
		return nil, errors.New("侧边栏选项过多")
	}
	out := []string{}
	for _, id := range items {
		if !slices.Contains(permitted, id) {
			return nil, errors.New("侧边栏包含无效的功能入口")
		}
		if id != "settings" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	out = append(out, "settings")
	return out, nil
}

func validateSiteAccessSettings(v *SiteSettings) error {
	adminDefaults, admin, _ := sidebarDefinition("admin")
	memberDefaults, member, _ := sidebarDefinition("member")
	var err error
	v.SidebarAdmin, err = normalizeSidebar(v.SidebarAdmin, adminDefaults, admin)
	if err != nil {
		return err
	}
	v.SidebarMember, err = normalizeSidebar(v.SidebarMember, memberDefaults, member)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(v.CountryAccessReason) > 500 || len(v.CountryAccessBlocked) > 250 {
		return errors.New("访问限制原因或国家列表过长")
	}
	countries := []string{}
	for _, code := range v.CountryAccessBlocked {
		code = strings.ToUpper(strings.TrimSpace(code))
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			return errors.New("请选择有效的国家或地区")
		}
		region, err := language.ParseRegion(code)
		if err != nil || !region.IsCountry() {
			return errors.New("请选择有效的国家或地区")
		}
		code = region.String()
		if !slices.Contains(countries, code) {
			countries = append(countries, code)
		}
	}
	v.CountryAccessBlocked = countries
	return nil
}
