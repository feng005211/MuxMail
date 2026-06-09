package muxmail

import (
	"os"
	"strings"
	"testing"
)

func TestMakeBuildUsesAdminBuildWrapperAndRestoreTarget(t *testing.T) {
	data, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)

	required := []string{
		"build:",
		"node web/admin/scripts/build-binary.mjs",
		"admin-sync: admin-build",
		"node web/admin/scripts/sync-admin-dist.mjs",
		"admin-restore:",
		"node web/admin/scripts/restore-admin-placeholder.mjs",
	}
	for _, fragment := range required {
		if !strings.Contains(makefile, fragment) {
			t.Fatalf("expected Makefile to contain %q", fragment)
		}
	}
}

func TestAdminBuildWrapperRestoresPlaceholderInFinally(t *testing.T) {
	data, err := os.ReadFile("web/admin/scripts/build-binary.mjs")
	if err != nil {
		t.Fatalf("read admin build wrapper: %v", err)
	}
	script := string(data)

	required := []string{
		"syncAdminDist();",
		"run('go', ['build', '-o', './bin/muxmail', './cmd/muxmail'], repoRoot);",
		"finally {",
		"restoreAdminPlaceholder();",
	}
	for _, fragment := range required {
		if !strings.Contains(script, fragment) {
			t.Fatalf("expected admin build wrapper to contain %q", fragment)
		}
	}
}
