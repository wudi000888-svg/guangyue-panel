// Package geoip resolves countries locally. Visitor addresses never leave the site.
package geoip

import (
	_ "embed"
	"net/netip"
	"sync"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

const Version = "DB-IP Lite 2026-10"

//go:embed country.mmdb
var database []byte

var once sync.Once
var reader *maxminddb.Reader
var openError error

type Country struct {
	Code        string
	Name        string
	EnglishName string
}

func Validate() error {
	once.Do(func() {
		reader, openError = maxminddb.OpenBytes(database)
		if openError == nil {
			openError = reader.Verify()
		}
	})
	return openError
}

func Lookup(ip string) (Country, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Country{}, nil
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return Country{}, nil
	}
	if err = Validate(); err != nil {
		return Country{}, err
	}
	var record struct {
		Country struct {
			Code  string `maxminddb:"iso_code"`
			Names struct {
				Chinese string `maxminddb:"zh-CN"`
				English string `maxminddb:"en"`
			} `maxminddb:"names"`
		} `maxminddb:"country"`
	}
	if err = reader.Lookup(addr).Decode(&record); err != nil {
		return Country{}, err
	}
	name := record.Country.Names.Chinese
	if name == "" {
		name = record.Country.Names.English
	}
	return Country{Code: record.Country.Code, Name: name, EnglishName: record.Country.Names.English}, nil
}
