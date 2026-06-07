package lite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/muxmail/muxmail/internal/domain"
	"gopkg.in/yaml.v3"
)

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

	var file suppressionYAML
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse suppression file: %w", err)
	}

	store := NewEmptySuppressionStore()
	store.path = path
	for index, entry := range file.Entries {
		appCode := strings.TrimSpace(entry.App)
		normalizedEmail := domain.NormalizeEmail(strings.TrimSpace(entry.Email))
		reason := domain.SuppressionReason(entry.Reason)
		if appCode == "" {
			return nil, fmt.Errorf("suppression entry %d app is required", index)
		}
		if normalizedEmail == "" {
			return nil, fmt.Errorf("suppression entry %d email is required", index)
		}
		if !reason.IsValid() {
			return nil, fmt.Errorf("suppression entry %d reason must be hard_bounce, complaint, or manual", index)
		}

		store.entries[suppressionKey(appCode, normalizedEmail)] = domain.SuppressionEntry{
			AppCode:         appCode,
			Email:           strings.TrimSpace(entry.Email),
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
	normalizedEmail := domain.NormalizeEmail(strings.TrimSpace(entry.NormalizedEmail))
	if normalizedEmail == "" {
		normalizedEmail = domain.NormalizeEmail(strings.TrimSpace(entry.Email))
	}
	email := strings.TrimSpace(entry.Email)
	if appCode == "" || normalizedEmail == "" {
		return false, fmt.Errorf("suppression entry app and email are required")
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

	normalizedEmail := domain.NormalizeEmail(strings.TrimSpace(filter.Email))

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

func suppressionKey(appCode string, normalizedEmail string) string {
	return appCode + "\x00" + normalizedEmail
}

func (s *SuppressionStore) persistLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), directoryPerm); err != nil {
		return fmt.Errorf("create suppression directory: %w", err)
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
	if err := os.WriteFile(s.path, data, filePerm); err != nil {
		return fmt.Errorf("write suppression file: %w", err)
	}

	return nil
}
