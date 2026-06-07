package lite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestLoadSuppressionStoreMissingFileIsEmpty(t *testing.T) {
	store, err := LoadSuppressionStore(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("load missing suppression file: %v", err)
	}

	if _, ok := store.Contains("project_a", domain.NormalizeEmail("bounced@example.com")); ok {
		t.Fatalf("expected missing suppression file to produce an empty store")
	}
}

func TestLoadSuppressionStoreValidHit(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: Bounced@Example.COM
    reason: hard_bounce
`)

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression file: %v", err)
	}

	entry, ok := store.Contains("project_a", domain.NormalizeEmail("bounced@example.com"))
	if !ok {
		t.Fatalf("expected suppressed recipient hit")
	}
	if entry.AppCode != "project_a" ||
		entry.Email != "Bounced@Example.COM" ||
		entry.NormalizedEmail != "bounced@example.com" ||
		entry.Reason != domain.SuppressionReasonHardBounce {
		t.Fatalf("unexpected suppression entry: %+v", entry)
	}
}

func TestLoadSuppressionStoreMissByApp(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: bounced@example.com
    reason: complaint
`)

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression file: %v", err)
	}

	if _, ok := store.Contains("project_b", domain.NormalizeEmail("bounced@example.com")); ok {
		t.Fatalf("expected suppression lookup to be isolated by app")
	}
}

func TestLoadSuppressionStoreRejectsInvalidReason(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: bounced@example.com
    reason: unsubscribe
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatalf("expected invalid reason error")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Fatalf("expected reason error, got %v", err)
	}
}

func TestLoadSuppressionStoreRejectsInvalidYAML(t *testing.T) {
	path := writeSuppressionFile(t, "entries: [")

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatalf("expected invalid YAML error")
	}
	if !strings.Contains(err.Error(), "parse suppression file") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestLoadSuppressionStoreRejectsMissingApp(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - email: bounced@example.com
    reason: manual
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatalf("expected missing app error")
	}
	if !strings.Contains(err.Error(), "app is required") {
		t.Fatalf("expected app error, got %v", err)
	}
}

func TestSuppressionStoreAddPersistsEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suppression.yaml")
	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}

	changed, err := store.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "Bounced@Example.com",
		Reason:  domain.SuppressionReasonHardBounce,
	})
	if err != nil {
		t.Fatalf("add suppression: %v", err)
	}
	if !changed {
		t.Fatal("expected add to report change")
	}

	reloaded, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	entry, ok := reloaded.Contains("project_a", domain.NormalizeEmail("bounced@example.com"))
	if !ok {
		t.Fatal("expected persisted suppression entry")
	}
	if entry.Reason != domain.SuppressionReasonHardBounce {
		t.Fatalf("unexpected persisted entry: %+v", entry)
	}
}

func TestSuppressionStoreAddDuplicateIsNoop(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: bounced@example.com
    reason: hard_bounce
`)

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}

	changed, err := store.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "bounced@example.com",
		Reason:  domain.SuppressionReasonHardBounce,
	})
	if err != nil {
		t.Fatalf("add duplicate suppression: %v", err)
	}
	if changed {
		t.Fatal("expected duplicate add to be a no-op")
	}
}

func TestSuppressionStoreListFiltersAndLimits(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: z-user@example.com
    reason: hard_bounce
  - app: project_a
    email: a-user@example.com
    reason: complaint
  - app: project_b
    email: foreign@example.com
    reason: complaint
`)

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}

	entries := store.List("project_a", SuppressionListFilter{
		Limit:  1,
		Reason: domain.SuppressionReasonComplaint,
	})
	if len(entries) != 1 {
		t.Fatalf("expected one filtered entry, got %+v", entries)
	}
	if entries[0].Email != "a-user@example.com" || entries[0].Reason != domain.SuppressionReasonComplaint {
		t.Fatalf("unexpected filtered entry: %+v", entries[0])
	}

	entries = store.List("project_a", SuppressionListFilter{
		Email: "z-user@example.com",
	})
	if len(entries) != 1 || entries[0].NormalizedEmail != "z-user@example.com" {
		t.Fatalf("expected exact email filter hit, got %+v", entries)
	}
}

func writeSuppressionFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "suppression.yaml")
	err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o600)
	if err != nil {
		t.Fatalf("write suppression file: %v", err)
	}

	return path
}
