// Package clientcatalog embeds the reviewed client download directory.
// Edit frontend/src/data/clients.json, then run scripts/sync-client-catalog.py.
package clientcatalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed clients.json
var data []byte

type Download struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
}

type Client struct {
	ID        string     `json:"id"`
	Downloads []Download `json:"downloads"`
}

var clients = func() []Client {
	var values []Client
	if err := json.Unmarshal(data, &values); err != nil {
		panic("invalid embedded client directory: " + err.Error())
	}
	return values
}()

// Lookup preserves the source download array's indices across all platforms.
func Lookup(id string, index int) (Download, bool) {
	for _, client := range clients {
		if client.ID == id && index >= 0 && index < len(client.Downloads) {
			return client.Downloads[index], true
		}
	}
	return Download{}, false
}
