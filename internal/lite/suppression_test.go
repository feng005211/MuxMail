package lite

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestLoadSuppressionStoreRejectsInvalidEmail(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: User <user@example.com>
    reason: manual
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatalf("expected invalid email error")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Fatalf("expected email error, got %v", err)
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

func TestLoadSuppressionStoreRejectsDuplicateEntry(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: user@example.com
    reason: manual
  - app: project_a
    email: USER@example.com
    reason: hard_bounce
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatal("expected duplicate suppression entry error")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("expected duplicate entry error, got %v", err)
	}
}

func TestLoadSuppressionStoreRejectsUnknownField(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: bounced@example.com
    reason: manual
    note: typo
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "note") {
		t.Fatalf("expected unknown field in error, got %v", err)
	}
}

func TestLoadSuppressionStoreRejectsMultipleYAMLDocuments(t *testing.T) {
	path := writeSuppressionFile(t, `
entries: []
---
entries:
  - app: project_a
    email: bounced@example.com
    reason: manual
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatal("expected multiple YAML documents to fail")
	}
	if !strings.Contains(err.Error(), "single YAML document") {
		t.Fatalf("expected single document error, got %v", err)
	}
}

func TestLoadSuppressionStoreAllowsEmptyFile(t *testing.T) {
	path := writeSuppressionFile(t, "")

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("expected empty suppression file to load: %v", err)
	}
	if entries := store.List("project_a", SuppressionListFilter{}); len(entries) != 0 {
		t.Fatalf("expected empty suppression store, got %+v", entries)
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

func TestLoadSuppressionStoreRejectsInvalidAppCode(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: Project A
    email: bounced@example.com
    reason: manual
`)

	_, err := LoadSuppressionStore(path)
	if err == nil {
		t.Fatalf("expected invalid app error")
	}
	if !strings.Contains(err.Error(), "app is invalid") {
		t.Fatalf("expected app invalid error, got %v", err)
	}
}

func TestSuppressionStoreAddRejectsInvalidEmail(t *testing.T) {
	store := NewEmptySuppressionStore()

	changed, err := store.Add(domain.SuppressionEntry{
		AppCode:         "project_a",
		Email:           "good@example.com",
		NormalizedEmail: "not-an-email",
		Reason:          domain.SuppressionReasonManual,
	})
	if err != nil {
		t.Fatalf("expected valid email to win over stale normalized email: %v", err)
	}
	if !changed {
		t.Fatal("expected first add to report change")
	}
	if _, ok := store.Contains("project_a", domain.NormalizeEmail("good@example.com")); !ok {
		t.Fatal("expected suppression key to use the valid email")
	}

	changed, err = store.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "bad email@example.com",
		Reason:  domain.SuppressionReasonManual,
	})
	if err == nil {
		t.Fatal("expected invalid email error")
	}
	if changed {
		t.Fatal("expected invalid email add to report no change")
	}
}

func TestSuppressionStoreAddRejectsInvalidAppCode(t *testing.T) {
	store := NewEmptySuppressionStore()

	changed, err := store.Add(domain.SuppressionEntry{
		AppCode: "Project A",
		Email:   "good@example.com",
		Reason:  domain.SuppressionReasonManual,
	})
	if err == nil {
		t.Fatal("expected invalid app code error")
	}
	if changed {
		t.Fatal("expected invalid app add to report no change")
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

func TestSuppressionStoreAddTightensPersistedFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX permission bits for os.Chmod")
	}

	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: old@example.com
    reason: hard_bounce
`)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("widen suppression file permissions: %v", err)
	}

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}
	if _, err := store.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "new@example.com",
		Reason:  domain.SuppressionReasonComplaint,
	}); err != nil {
		t.Fatalf("add suppression: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat suppression file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != filePerm {
		t.Fatalf("expected suppression file perm %o, got %o", filePerm, got)
	}
}

func TestWriteFileAtomicallyReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suppression.yaml")
	if err := os.WriteFile(path, []byte("old file\n"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	if err := writeFileAtomically(path, []byte("new file\n"), filePerm); err != nil {
		t.Fatalf("write file atomically: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(data) != "new file\n" {
		t.Fatalf("expected replaced file content, got %q", string(data))
	}

	if runtime.GOOS == "windows" {
		return
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat replaced file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != filePerm {
		t.Fatalf("expected replaced file perm %o, got %o", filePerm, got)
	}
}

func TestWriteFileAtomicallyCleansTempFileOnReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suppression.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}

	err := writeFileAtomically(path, []byte("new file\n"), filePerm)
	if err == nil {
		t.Fatal("expected replace failure")
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".suppression.yaml.*.tmp"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected temporary file cleanup, got %+v", matches)
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

func TestSuppressionStoreAddUpgradesHardBounceToComplaint(t *testing.T) {
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
		Reason:  domain.SuppressionReasonComplaint,
	})
	if err != nil {
		t.Fatalf("upgrade suppression: %v", err)
	}
	if !changed {
		t.Fatal("expected complaint to upgrade hard bounce suppression")
	}

	reloaded, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("reload suppression store: %v", err)
	}
	entry, ok := reloaded.Contains("project_a", domain.NormalizeEmail("bounced@example.com"))
	if !ok {
		t.Fatal("expected upgraded suppression entry")
	}
	if entry.Reason != domain.SuppressionReasonComplaint {
		t.Fatalf("expected complaint reason, got %+v", entry)
	}
}

func TestSuppressionStoreAddDoesNotOverrideManual(t *testing.T) {
	path := writeSuppressionFile(t, `
entries:
  - app: project_a
    email: manual@example.com
    reason: manual
`)

	store, err := LoadSuppressionStore(path)
	if err != nil {
		t.Fatalf("load suppression store: %v", err)
	}

	changed, err := store.Add(domain.SuppressionEntry{
		AppCode: "project_a",
		Email:   "manual@example.com",
		Reason:  domain.SuppressionReasonComplaint,
	})
	if err != nil {
		t.Fatalf("add suppression: %v", err)
	}
	if changed {
		t.Fatal("expected manual suppression to be preserved")
	}

	entry, ok := store.Contains("project_a", domain.NormalizeEmail("manual@example.com"))
	if !ok {
		t.Fatal("expected manual suppression entry")
	}
	if entry.Reason != domain.SuppressionReasonManual {
		t.Fatalf("expected manual reason, got %+v", entry)
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

	entries = store.List("project_a", SuppressionListFilter{
		Email: "User <z-user@example.com>",
	})
	if len(entries) != 0 {
		t.Fatalf("expected invalid email filter to miss, got %+v", entries)
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
