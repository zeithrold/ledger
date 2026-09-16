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

// previousVersion is the revision tolerated alongside the current one so an
// already deployed client keeps working across a backend roll-forward.
//
// A date revision must be additive for this to be meaningful: the served
// contract is always the current document, and anything that renames or removes
// a field is a breaking change that belongs in a new major version instead.
// Update this constant to the outgoing date as the first step of every bump.
const previousVersion = "2026-09-16"

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

// PreviousVersion returns the tolerated preceding revision, or the current one
// when no earlier revision is accepted.
func PreviousVersion() string {
	if previousVersion == "" {
		return currentVersion
	}
	return previousVersion
}

// Supported returns the explicitly registered revisions for a major version.
//
// The current revision and, when it differs, the immediately preceding one are
// accepted. The preceding revision is only ever served the current document.
func Supported(major string) ([]string, bool) {
	if major != "v1" {
		return nil, false
	}
	if previousVersion == "" || previousVersion == currentVersion {
		return []string{currentVersion}, true
	}
	return []string{currentVersion, previousVersion}, true
}

// Document returns a copy of the current document.
func Document() []byte { return append([]byte(nil), v1...) }

// URL identifies the served immutable revision.
func URL() string { return "/openapi/v1/" + Version() + ".json" }
