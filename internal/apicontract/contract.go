// Package apicontract registers the contracts supported by this binary.
package apicontract

import (
	_ "embed"
	"encoding/json"
)

// V1 is the canonical current v1 contract. Only registered contracts are served.
//
//go:embed v1/openapi.json
var v1 []byte

// VersionHeader selects an exact date within a major version.
const VersionHeader = "X-Ledger-API-Version"

var currentVersion = func() string {
	var metadata struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(v1, &metadata); err != nil {
		panic("invalid embedded API contract")
	}
	return metadata.Info.Version
}()

// Version returns the current v1 revision from the canonical contract.
func Version() string { return currentVersion }

// Supported returns the explicitly registered revisions for a major version.
func Supported(major string) ([]string, bool) {
	if major != "v1" {
		return nil, false
	}
	return []string{Version()}, true
}

// Document returns a copy of the current document.
func Document() []byte { return append([]byte(nil), v1...) }

// URL identifies the served immutable revision.
func URL() string { return "/openapi/v1/" + Version() + ".json" }
