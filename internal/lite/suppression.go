package lite

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

var suppressionAppCodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$`)

// SuppressionStore provides App-scoped recipient suppression lookups.
type SuppressionStore struct {
	path    string
	mu      sync.RWMutex
	entries map[string]domain.SuppressionEntry
}

// SuppressionListFilter controls App-scoped suppression list queries.
type SuppressionListFilter struct {
	Limit  int
	Reason domain.SuppressionReason
	Email  string
}

// NewEmptySuppressionStore creates a suppression store with no entries.
func NewEmptySuppressionStore() *SuppressionStore {
	return &SuppressionStore{entries: map[string]domain.SuppressionEntry{}}
}

// LoadSuppressionStore reads the static YAML suppression list from path.
func LoadSuppressionStore(path string) (*SuppressionStore, error) {
	if strings.TrimSpace(path) == "" {
		return NewEmptySuppressionStore(), nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		store := NewEmptySuppressionStore()
		store.path = path
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read suppression file: %w", err)
	}

	file, err := decodeSuppressionYAML(data)
	if err != nil {
		return nil, fmt.Errorf("parse suppression file: %w", err)
	}

	store := NewEmptySuppressionStore()
	store.path = path
	for index, entry := range file.Entries {
		appCode := strings.TrimSpace(entry.App)
		email := strings.TrimSpace(entry.Email)
		normalizedEmail, emailOK := domain.NormalizeAddrSpecEmail(email)
		reason := domain.SuppressionReason(entry.Reason)
		if appCode == "" {
			return nil, fmt.Errorf("suppression entry %d app is required", index)
		}
		if !isValidSuppressionAppCode(appCode) {
			return nil, fmt.Errorf("suppression entry %d app is invalid", index)
		}
		if !emailOK {
			return nil, fmt.Errorf("suppression entry %d email must be a valid single addr-spec", index)
		}
		if !reason.IsValid() {
			return nil, fmt.Errorf("suppression entry %d reason must be hard_bounce, complaint, or manual", index)
		}

		key := suppressionKey(appCode, normalizedEmail)
		if _, exists := store.entries[key]; exists {
			return nil, fmt.Errorf("suppression entry %d duplicates an earlier app and email", index)
		}
		store.entries[key] = domain.SuppressionEntry{
			AppCode:         appCode,
			Email:           email,
			NormalizedEmail: normalizedEmail,
			Reason:          reason,
		}
	}

	return store, nil
}

// Contains reports whether appCode and normalizedEmail are present in the suppression list.
func (s *SuppressionStore) Contains(appCode string, normalizedEmail string) (domain.SuppressionEntry, bool) {
	if s == nil {
		return domain.SuppressionEntry{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[suppressionKey(appCode, normalizedEmail)]
	return entry, ok
}

// Add stores a suppression entry and persists it to the YAML file when the store has a backing path.
func (s *SuppressionStore) Add(entry domain.SuppressionEntry) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("suppression store is required")
	}

	appCode := strings.TrimSpace(entry.AppCode)
	email := strings.TrimSpace(entry.Email)
	sourceEmail := email
	if sourceEmail == "" {
		sourceEmail = strings.TrimSpace(entry.NormalizedEmail)
	}
	normalizedEmail, emailOK := domain.NormalizeAddrSpecEmail(sourceEmail)
	if appCode == "" {
		return false, fmt.Errorf("suppression entry app is required")
	}
	if !isValidSuppressionAppCode(appCode) {
		return false, fmt.Errorf("suppression entry app is invalid")
	}
	if !emailOK {
		return false, fmt.Errorf("suppression entry email must be a valid single addr-spec")
	}
	if !entry.Reason.IsValid() {
		return false, fmt.Errorf("suppression entry reason must be hard_bounce, complaint, or manual")
	}
	if email == "" {
		email = normalizedEmail
	}

	normalizedEntry := domain.SuppressionEntry{
		AppCode:         appCode,
		Email:           email,
		NormalizedEmail: normalizedEmail,
		Reason:          entry.Reason,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := suppressionKey(appCode, normalizedEmail)
	if existing, ok := s.entries[key]; ok {
		if existing.Reason == normalizedEntry.Reason && existing.Email == normalizedEntry.Email {
			return false, nil
		}
		if existing.Reason == domain.SuppressionReasonHardBounce && normalizedEntry.Reason == domain.SuppressionReasonComplaint {
			s.entries[key] = normalizedEntry
			if err := s.persistLocked(); err != nil {
				s.entries[key] = existing
				return false, err
			}
			return true, nil
		}
		return false, nil
	}

	s.entries[key] = normalizedEntry
	if err := s.persistLocked(); err != nil {
		delete(s.entries, key)
		return false, err
	}

	return true, nil
}

// List returns App-scoped suppression entries sorted by normalized email.
func (s *SuppressionStore) List(appCode string, filter SuppressionListFilter) []domain.SuppressionEntry {
	if s == nil {
		return []domain.SuppressionEntry{}
	}

	var normalizedEmail string
	if strings.TrimSpace(filter.Email) != "" {
		var ok bool
		normalizedEmail, ok = domain.NormalizeAddrSpecEmail(filter.Email)
		if !ok {
			return []domain.SuppressionEntry{}
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.entries))
	for key, entry := range s.entries {
		if entry.AppCode != appCode {
			continue
		}
		if filter.Reason != "" && entry.Reason != filter.Reason {
			continue
		}
		if normalizedEmail != "" && entry.NormalizedEmail != normalizedEmail {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]domain.SuppressionEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, s.entries[key])
	}
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}

	return entries
}

type suppressionYAML struct {
	Entries []suppressionYAMLEntry `yaml:"entries"`
}

type suppressionYAMLEntry struct {
	App    string `yaml:"app"`
	Email  string `yaml:"email"`
	Reason string `yaml:"reason"`
}

func decodeSuppressionYAML(data []byte) (suppressionYAML, error) {
	var file suppressionYAML
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return file, nil
		}
		return suppressionYAML{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return suppressionYAML{}, fmt.Errorf("suppression file must contain a single YAML document")
		}
		return suppressionYAML{}, err
	}

	return file, nil
}

func suppressionKey(appCode string, normalizedEmail string) string {
	return appCode + "\x00" + normalizedEmail
}

func isValidSuppressionAppCode(value string) bool {
	return suppressionAppCodePattern.MatchString(value)
}

func (s *SuppressionStore) persistLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}

	if err := ensureDirectory(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("prepare suppression directory: %w", err)
	}

	keys := make([]string, 0, len(s.entries))
	for key := range s.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	file := suppressionYAML{Entries: make([]suppressionYAMLEntry, 0, len(keys))}
	for _, key := range keys {
		entry := s.entries[key]
		file.Entries = append(file.Entries, suppressionYAMLEntry{
			App:    entry.AppCode,
			Email:  entry.Email,
			Reason: string(entry.Reason),
		})
	}

	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal suppression file: %w", err)
	}
	if err := writeFileAtomically(s.path, data, filePerm); err != nil {
		return fmt.Errorf("write suppression file: %w", err)
	}

	return nil
}

// writeFileAtomically replaces path only after the full temporary file is durable.
func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tempFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(perm); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	cleanup = false

	return nil
}
