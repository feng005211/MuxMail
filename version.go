package muxmail

import (
	_ "embed"
	"strings"
)

// rawVersion is the repository VERSION file embedded into MuxMail binaries.
//
//go:embed VERSION
var rawVersion string

// Version returns the release version embedded in this MuxMail build.
func Version() string {
	return strings.TrimSpace(rawVersion)
}
