package muxmail

import (
	"regexp"
	"testing"
)

func TestVersionUsesReleaseSemVer(t *testing.T) {
	matched, err := regexp.MatchString(`^[0-9]+\.[0-9]+\.[0-9]+$`, Version())
	if err != nil {
		t.Fatalf("compile version pattern: %v", err)
	}
	if !matched {
		t.Fatalf("version must use MAJOR.MINOR.PATCH, got %q", Version())
	}
}
