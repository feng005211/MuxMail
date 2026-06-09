package lite

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

const (
	messagesFilename = "mail-messages.jsonl"
	attemptsFilename = "mail-attempts.jsonl"
	eventsFilename   = "mail-events.jsonl"
)

// MessageLogConfig contains Lite JSONL message log settings.
type MessageLogConfig struct {
	Dir           string
	MaxBytes      int64
	MaxBackups    int
	EventsEnabled bool
	Now           func() time.Time
}

// MessageLog appends message and attempt audit records to JSONL files.
type MessageLog struct {
	messages *jsonlWriter
	attempts *jsonlWriter
	events   *jsonlWriter
	now      func() time.Time
}

// MessageSnapshot is the safe, queryable subset of the latest message record.
type MessageSnapshot struct {
	Timestamp         time.Time
	RequestID         string
	BusinessRequestID string
	MessageID         string
	AppCode           string
	APIKeyName        string
	SceneCode         string
	ToDomain          string
	ToHash            string
	Locale            string
	Status            domain.MessageStatus
	ErrorCode         domain.ErrorCode
	ErrorMessage      string
}

// ProviderEventSnapshot is the safe, queryable subset of one recorded provider event.
type ProviderEventSnapshot struct {
	Timestamp           time.Time
	MessageID           string
	AppCode             string
	Provider            domain.Provider
	ProviderAccountCode string
	ProviderChannelCode string
	ProviderMessageID   string
	EventType           domain.ProviderEventType
	OccurredAt          string
}

// AttemptSnapshot is the safe, queryable subset of one recorded provider attempt.
type AttemptSnapshot struct {
	Timestamp           time.Time
	MessageID           string
	AppCode             string
	AttemptNo           int
	Provider            domain.Provider
	ProviderAccountCode string
	ProviderChannelCode string
	Transport           domain.Transport
	Status              domain.AttemptStatus
	FailureClass        domain.FailureClass
	ErrorCode           domain.ErrorCode
	ErrorMessage        string
	ProviderMessageID   string
	DurationMS          int
}

// MessageListFilter controls App-scoped message list queries.
type MessageListFilter struct {
	Limit  int
	Status domain.MessageStatus
	Scene  string
}

// ProviderEventListFilter controls App-scoped provider event list queries.
type ProviderEventListFilter struct {
	Limit     int
	Provider  domain.Provider
	EventType domain.ProviderEventType
}

// NewMessageLog opens the Lite JSONL message log files.
func NewMessageLog(config MessageLogConfig) (*MessageLog, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("log directory is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	messages, err := newJSONLWriter(filepath.Join(config.Dir, messagesFilename), config.MaxBytes, config.MaxBackups)
	if err != nil {
		return nil, err
	}
	attempts, err := newJSONLWriter(filepath.Join(config.Dir, attemptsFilename), config.MaxBytes, config.MaxBackups)
	if err != nil {
		messages.close()
		return nil, err
	}
	var events *jsonlWriter
	if config.EventsEnabled {
		events, err = newJSONLWriter(filepath.Join(config.Dir, eventsFilename), config.MaxBytes, config.MaxBackups)
		if err != nil {
			messages.close()
			attempts.close()
			return nil, err
		}
	}

	return &MessageLog{
		messages: messages,
		attempts: attempts,
		events:   events,
		now:      config.Now,
	}, nil
}

// AppendMessage appends one message status record.
func (l *MessageLog) AppendMessage(message domain.Message) error {
	return l.messages.appendLine(encodeMessageRecord(l.now(), message))
}

// AppendAttempt appends one provider attempt status record.
func (l *MessageLog) AppendAttempt(attempt domain.Attempt) error {
	return l.attempts.appendLine(encodeAttemptRecord(l.now(), attempt))
}

// AppendProviderEvent appends one normalized provider webhook event.
func (l *MessageLog) AppendProviderEvent(event domain.ProviderEvent) error {
	if l.events == nil {
		return fmt.Errorf("provider event log is disabled")
	}

	return l.events.appendLine(encodeProviderEventRecord(l.now(), event))
}

// AppendProviderEventOnce appends event only when the same provider event identity is absent.
func (l *MessageLog) AppendProviderEventOnce(event domain.ProviderEvent) (bool, error) {
	if l.events == nil {
		return false, fmt.Errorf("provider event log is disabled")
	}

	l.events.mu.Lock()
	defer l.events.mu.Unlock()

	for _, path := range l.eventQueryPaths() {
		found, err := providerEventExistsInPath(path, event)
		if err != nil {
			return false, err
		}
		if found {
			return false, nil
		}
	}
	if err := l.events.appendLineLocked(encodeProviderEventRecord(l.now(), event)); err != nil {
		return false, err
	}

	return true, nil
}

// HasProviderEvent reports whether an equivalent provider event has already been recorded.
func (l *MessageLog) HasProviderEvent(event domain.ProviderEvent) (bool, error) {
	if l.events == nil {
		return false, nil
	}

	l.events.mu.Lock()
	defer l.events.mu.Unlock()

	for _, path := range l.eventQueryPaths() {
		found, err := providerEventExistsInPath(path, event)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}

	return false, nil
}

// ListProviderEvents returns all recorded provider events for one App-scoped message.
func (l *MessageLog) ListProviderEvents(appCode string, messageID string) ([]ProviderEventSnapshot, error) {
	if l.events == nil {
		return []ProviderEventSnapshot{}, nil
	}

	l.events.mu.Lock()
	defer l.events.mu.Unlock()

	events := make([]ProviderEventSnapshot, 0)
	for _, path := range l.eventQueryPaths() {
		batch, err := listProviderEventsInPath(path, appCode, messageID)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}

	return events, nil
}

// ListRecentProviderEvents returns recent provider events for one App.
func (l *MessageLog) ListRecentProviderEvents(appCode string, filter ProviderEventListFilter) ([]ProviderEventSnapshot, error) {
	if l.events == nil {
		return []ProviderEventSnapshot{}, nil
	}

	l.events.mu.Lock()
	defer l.events.mu.Unlock()

	events := make([]ProviderEventSnapshot, 0)
	for _, path := range l.eventQueryPaths() {
		batch, err := listRecentProviderEventsInPath(path, appCode, filter)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			if events[i].MessageID == events[j].MessageID {
				return events[i].ProviderMessageID > events[j].ProviderMessageID
			}
			return events[i].MessageID > events[j].MessageID
		}
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	if filter.Limit > 0 && len(events) > filter.Limit {
		events = events[:filter.Limit]
	}

	return events, nil
}

// ListAttempts returns all recorded provider attempts for one App-scoped message.
func (l *MessageLog) ListAttempts(appCode string, messageID string) ([]AttemptSnapshot, error) {
	if l.attempts == nil {
		return nil, fmt.Errorf("attempt log is closed")
	}

	l.attempts.mu.Lock()
	defer l.attempts.mu.Unlock()

	attempts := make([]AttemptSnapshot, 0)
	for _, path := range l.attemptQueryPaths() {
		batch, err := listAttemptsInPath(path, appCode, messageID)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, batch...)
	}

	return attempts, nil
}

// ListLatestMessages returns the latest snapshot for each message in one App.
func (l *MessageLog) ListLatestMessages(appCode string, filter MessageListFilter) ([]MessageSnapshot, error) {
	if l.messages == nil {
		return nil, fmt.Errorf("message log is closed")
	}

	l.messages.mu.Lock()
	defer l.messages.mu.Unlock()

	latestByMessage := make(map[string]MessageSnapshot)
	for _, path := range l.messageQueryPaths() {
		if err := collectLatestMessagesInPath(path, appCode, latestByMessage); err != nil {
			return nil, err
		}
	}

	messages := make([]MessageSnapshot, 0, len(latestByMessage))
	for _, snapshot := range latestByMessage {
		if filter.Status != "" && snapshot.Status != filter.Status {
			continue
		}
		if filter.Scene != "" && snapshot.SceneCode != filter.Scene {
			continue
		}
		messages = append(messages, snapshot)
	}

	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Timestamp.Equal(messages[j].Timestamp) {
			return messages[i].MessageID > messages[j].MessageID
		}
		return messages[i].Timestamp.After(messages[j].Timestamp)
	})
	if filter.Limit > 0 && len(messages) > filter.Limit {
		messages = messages[:filter.Limit]
	}

	return messages, nil
}

// AppendWebhookMessageStatus appends a message status update produced by a webhook.
func (l *MessageLog) AppendWebhookMessageStatus(snapshot MessageSnapshot, event domain.ProviderEvent) error {
	message := domain.Message{
		RequestID:         snapshot.RequestID,
		BusinessRequestID: snapshot.BusinessRequestID,
		MessageID:         snapshot.MessageID,
		AppCode:           snapshot.AppCode,
		APIKeyName:        snapshot.APIKeyName,
		SceneCode:         snapshot.SceneCode,
		ToDomain:          snapshot.ToDomain,
		ToHash:            snapshot.ToHash,
		Locale:            snapshot.Locale,
		Status:            event.EventType.MessageStatus(),
	}
	return l.AppendMessage(message)
}

// FindLatestMessage returns the latest message status record for an App and message ID.
func (l *MessageLog) FindLatestMessage(appCode string, messageID string) (MessageSnapshot, bool, error) {
	if l.messages == nil {
		return MessageSnapshot{}, false, fmt.Errorf("message log is closed")
	}

	l.messages.mu.Lock()
	defer l.messages.mu.Unlock()

	var latest MessageSnapshot
	found := false

	for _, path := range l.messageQueryPaths() {
		snapshot, pathFound, err := findLatestMessageInPath(path, appCode, messageID)
		if err != nil {
			return MessageSnapshot{}, false, err
		}
		if pathFound {
			latest = snapshot
			found = true
		}
	}

	return latest, found, nil
}

func (l *MessageLog) messageQueryPaths() []string {
	paths := make([]string, 0, l.messages.maxBackups+1)
	for index := l.messages.maxBackups; index >= 1; index-- {
		paths = append(paths, fmt.Sprintf("%s.%d", l.messages.path, index))
	}
	paths = append(paths, l.messages.path)

	return paths
}

func collectLatestMessagesInPath(path string, appCode string, latestByMessage map[string]MessageSnapshot) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open message log for query: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		snapshot, err := decodeMessageSnapshot(scanner.Bytes())
		if err != nil {
			return fmt.Errorf("decode message log record in %s: %w", path, err)
		}
		if snapshot.AppCode != appCode {
			continue
		}
		latestByMessage[snapshot.MessageID] = snapshot
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan message log: %w", err)
	}

	return nil
}

func findLatestMessageInPath(path string, appCode string, messageID string) (MessageSnapshot, bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return MessageSnapshot{}, false, nil
	}
	if err != nil {
		return MessageSnapshot{}, false, fmt.Errorf("open message log for query: %w", err)
	}
	defer file.Close()

	var latest MessageSnapshot
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		snapshot, err := decodeMessageSnapshot(scanner.Bytes())
		if err != nil {
			return MessageSnapshot{}, false, fmt.Errorf("decode message log record in %s: %w", path, err)
		}
		if snapshot.AppCode != appCode || snapshot.MessageID != messageID {
			continue
		}
		latest = snapshot
		found = true
	}
	if err := scanner.Err(); err != nil {
		return MessageSnapshot{}, false, fmt.Errorf("scan message log: %w", err)
	}

	return latest, found, nil
}

func (l *MessageLog) eventQueryPaths() []string {
	paths := make([]string, 0, l.events.maxBackups+1)
	for index := l.events.maxBackups; index >= 1; index-- {
		paths = append(paths, fmt.Sprintf("%s.%d", l.events.path, index))
	}
	paths = append(paths, l.events.path)

	return paths
}

func (l *MessageLog) attemptQueryPaths() []string {
	paths := make([]string, 0, l.attempts.maxBackups+1)
	for index := l.attempts.maxBackups; index >= 1; index-- {
		paths = append(paths, fmt.Sprintf("%s.%d", l.attempts.path, index))
	}
	paths = append(paths, l.attempts.path)

	return paths
}

// Close flushes and closes all open JSONL files.
func (l *MessageLog) Close() error {
	var firstErr error
	if l.messages != nil {
		firstErr = l.messages.close()
	}
	if l.attempts != nil {
		if err := l.attempts.close(); firstErr == nil {
			firstErr = err
		}
	}
	if l.events != nil {
		if err := l.events.close(); firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func decodeMessageSnapshot(line []byte) (MessageSnapshot, error) {
	var raw struct {
		Timestamp         string               `json:"ts"`
		RequestID         string               `json:"request_id"`
		BusinessRequestID string               `json:"business_request_id"`
		MessageID         string               `json:"message_id"`
		AppCode           string               `json:"app"`
		APIKeyName        string               `json:"api_key_name"`
		SceneCode         string               `json:"scene"`
		ToDomain          string               `json:"to_domain"`
		ToHash            string               `json:"to_hash"`
		Locale            string               `json:"locale"`
		Status            domain.MessageStatus `json:"status"`
		ErrorCode         domain.ErrorCode     `json:"error_code"`
		ErrorMessage      string               `json:"error_message"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return MessageSnapshot{}, fmt.Errorf("decode message log record: %w", err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if err != nil {
		return MessageSnapshot{}, fmt.Errorf("decode message log timestamp: %w", err)
	}
	if raw.MessageID == "" || !raw.Status.IsValid() {
		return MessageSnapshot{}, fmt.Errorf("decode message log record: required fields are invalid")
	}

	return MessageSnapshot{
		Timestamp:         timestamp,
		RequestID:         raw.RequestID,
		BusinessRequestID: raw.BusinessRequestID,
		MessageID:         raw.MessageID,
		AppCode:           raw.AppCode,
		APIKeyName:        raw.APIKeyName,
		SceneCode:         raw.SceneCode,
		ToDomain:          raw.ToDomain,
		ToHash:            raw.ToHash,
		Locale:            raw.Locale,
		Status:            raw.Status,
		ErrorCode:         raw.ErrorCode,
		ErrorMessage:      raw.ErrorMessage,
	}, nil
}

func providerEventExistsInPath(path string, target domain.ProviderEvent) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open provider event log for query: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		recorded, err := decodeProviderEventSnapshot(scanner.Bytes())
		if err != nil {
			return false, fmt.Errorf("decode provider event log record in %s: %w", path, err)
		}
		if sameProviderEventIdentity(recorded, target) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan provider event log: %w", err)
	}

	return false, nil
}

func listAttemptsInPath(path string, appCode string, messageID string) ([]AttemptSnapshot, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []AttemptSnapshot{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open attempt log for query: %w", err)
	}
	defer file.Close()

	attempts := make([]AttemptSnapshot, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		recorded, err := decodeAttemptSnapshot(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode attempt log record in %s: %w", path, err)
		}
		if recorded.AppCode != appCode || recorded.MessageID != messageID {
			continue
		}
		attempts = append(attempts, recorded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan attempt log: %w", err)
	}

	return attempts, nil
}

func decodeAttemptSnapshot(line []byte) (AttemptSnapshot, error) {
	var raw struct {
		Timestamp           string               `json:"ts"`
		MessageID           string               `json:"message_id"`
		AppCode             string               `json:"app"`
		AttemptNo           int                  `json:"attempt_no"`
		Provider            domain.Provider      `json:"provider"`
		ProviderAccountCode string               `json:"provider_account"`
		ProviderChannelCode string               `json:"provider_channel"`
		Transport           domain.Transport     `json:"transport"`
		Status              domain.AttemptStatus `json:"status"`
		FailureClass        domain.FailureClass  `json:"failure_class"`
		ErrorCode           domain.ErrorCode     `json:"error_code"`
		ErrorMessage        string               `json:"error_message"`
		ProviderMessageID   string               `json:"provider_message_id"`
		DurationMS          int                  `json:"duration_ms"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return AttemptSnapshot{}, fmt.Errorf("decode attempt log record: %w", err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if err != nil {
		return AttemptSnapshot{}, fmt.Errorf("decode attempt log timestamp: %w", err)
	}
	if raw.MessageID == "" ||
		raw.AttemptNo <= 0 ||
		!raw.Status.IsValid() ||
		!raw.FailureClass.IsValid() ||
		!isValidAttemptProviderMetadata(raw.Provider, raw.ProviderAccountCode, raw.ProviderChannelCode, raw.Transport, raw.Status, raw.FailureClass) {
		return AttemptSnapshot{}, fmt.Errorf("decode attempt log record: required fields are invalid")
	}

	return AttemptSnapshot{
		Timestamp:           timestamp,
		MessageID:           raw.MessageID,
		AppCode:             raw.AppCode,
		AttemptNo:           raw.AttemptNo,
		Provider:            raw.Provider,
		ProviderAccountCode: raw.ProviderAccountCode,
		ProviderChannelCode: raw.ProviderChannelCode,
		Transport:           raw.Transport,
		Status:              raw.Status,
		FailureClass:        raw.FailureClass,
		ErrorCode:           raw.ErrorCode,
		ErrorMessage:        raw.ErrorMessage,
		ProviderMessageID:   raw.ProviderMessageID,
		DurationMS:          raw.DurationMS,
	}, nil
}

func isValidAttemptProviderMetadata(provider domain.Provider, providerAccount string, providerChannel string, transport domain.Transport, status domain.AttemptStatus, failureClass domain.FailureClass) bool {
	if provider.IsValid() && providerAccount != "" && providerChannel != "" && transport.IsValid() {
		return true
	}
	if provider == "" &&
		providerAccount == "" &&
		providerChannel != "" &&
		transport == "" &&
		status == domain.AttemptStatusFailed &&
		failureClass == domain.FailureClassChannel {
		return true
	}

	return false
}

func listProviderEventsInPath(path string, appCode string, messageID string) ([]ProviderEventSnapshot, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []ProviderEventSnapshot{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open provider event log for query: %w", err)
	}
	defer file.Close()

	events := make([]ProviderEventSnapshot, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		recorded, err := decodeProviderEventQuerySnapshot(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode provider event log record in %s: %w", path, err)
		}
		if recorded.AppCode != appCode || recorded.MessageID != messageID {
			continue
		}
		events = append(events, recorded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan provider event log: %w", err)
	}

	return events, nil
}

func listRecentProviderEventsInPath(path string, appCode string, filter ProviderEventListFilter) ([]ProviderEventSnapshot, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []ProviderEventSnapshot{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open provider event log for query: %w", err)
	}
	defer file.Close()

	events := make([]ProviderEventSnapshot, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		recorded, err := decodeProviderEventQuerySnapshot(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode provider event log record in %s: %w", path, err)
		}
		if recorded.AppCode != appCode {
			continue
		}
		if filter.Provider != "" && recorded.Provider != filter.Provider {
			continue
		}
		if filter.EventType != "" && recorded.EventType != filter.EventType {
			continue
		}
		events = append(events, recorded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan provider event log: %w", err)
	}

	return events, nil
}

func decodeProviderEventSnapshot(line []byte) (domain.ProviderEvent, error) {
	var raw struct {
		Timestamp           string                   `json:"ts"`
		MessageID           string                   `json:"message_id"`
		AppCode             string                   `json:"app"`
		Provider            domain.Provider          `json:"provider"`
		ProviderAccountCode string                   `json:"provider_account"`
		ProviderChannelCode string                   `json:"provider_channel"`
		ProviderMessageID   string                   `json:"provider_message_id"`
		EventType           domain.ProviderEventType `json:"event_type"`
		EventPayload        string                   `json:"event_payload"`
		OccurredAt          string                   `json:"occurred_at"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return domain.ProviderEvent{}, fmt.Errorf("decode provider event log record: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err != nil {
		return domain.ProviderEvent{}, fmt.Errorf("decode provider event log timestamp: %w", err)
	}
	if err := validateProviderEventLogFields(
		raw.MessageID,
		raw.AppCode,
		raw.Provider,
		raw.ProviderAccountCode,
		raw.ProviderChannelCode,
		raw.ProviderMessageID,
		raw.EventType,
		raw.OccurredAt,
	); err != nil {
		return domain.ProviderEvent{}, fmt.Errorf("decode provider event log record: required fields are invalid")
	}

	return domain.ProviderEvent{
		MessageID:           raw.MessageID,
		AppCode:             raw.AppCode,
		Provider:            raw.Provider,
		ProviderAccountCode: raw.ProviderAccountCode,
		ProviderChannelCode: raw.ProviderChannelCode,
		ProviderMessageID:   raw.ProviderMessageID,
		EventType:           raw.EventType,
		EventPayload:        raw.EventPayload,
		OccurredAt:          raw.OccurredAt,
	}, nil
}

func decodeProviderEventQuerySnapshot(line []byte) (ProviderEventSnapshot, error) {
	var raw struct {
		Timestamp           string                   `json:"ts"`
		MessageID           string                   `json:"message_id"`
		AppCode             string                   `json:"app"`
		Provider            domain.Provider          `json:"provider"`
		ProviderAccountCode string                   `json:"provider_account"`
		ProviderChannelCode string                   `json:"provider_channel"`
		ProviderMessageID   string                   `json:"provider_message_id"`
		EventType           domain.ProviderEventType `json:"event_type"`
		OccurredAt          string                   `json:"occurred_at"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return ProviderEventSnapshot{}, fmt.Errorf("decode provider event log record: %w", err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if err != nil {
		return ProviderEventSnapshot{}, fmt.Errorf("decode provider event log timestamp: %w", err)
	}
	if err := validateProviderEventLogFields(
		raw.MessageID,
		raw.AppCode,
		raw.Provider,
		raw.ProviderAccountCode,
		raw.ProviderChannelCode,
		raw.ProviderMessageID,
		raw.EventType,
		raw.OccurredAt,
	); err != nil {
		return ProviderEventSnapshot{}, fmt.Errorf("decode provider event log record: required fields are invalid")
	}

	return ProviderEventSnapshot{
		Timestamp:           timestamp,
		MessageID:           raw.MessageID,
		AppCode:             raw.AppCode,
		Provider:            raw.Provider,
		ProviderAccountCode: raw.ProviderAccountCode,
		ProviderChannelCode: raw.ProviderChannelCode,
		ProviderMessageID:   raw.ProviderMessageID,
		EventType:           raw.EventType,
		OccurredAt:          raw.OccurredAt,
	}, nil
}

func validateProviderEventLogFields(messageID string, appCode string, provider domain.Provider, providerAccount string, providerChannel string, providerMessageID string, eventType domain.ProviderEventType, occurredAt string) error {
	if messageID == "" ||
		appCode == "" ||
		providerAccount == "" ||
		providerChannel == "" ||
		providerMessageID == "" ||
		occurredAt == "" ||
		!provider.IsValid() ||
		!eventType.IsValid() {
		return fmt.Errorf("required fields are invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, occurredAt); err != nil {
		return err
	}

	return nil
}

func sameProviderEventIdentity(left domain.ProviderEvent, right domain.ProviderEvent) bool {
	return left.MessageID == right.MessageID &&
		left.AppCode == right.AppCode &&
		left.Provider == right.Provider &&
		left.ProviderAccountCode == right.ProviderAccountCode &&
		left.ProviderChannelCode == right.ProviderChannelCode &&
		left.ProviderMessageID == right.ProviderMessageID &&
		left.EventType == right.EventType &&
		left.OccurredAt == right.OccurredAt
}

func encodeMessageRecord(ts time.Time, message domain.Message) []byte {
	line := make([]byte, 0, 512)
	line = append(line, '{')
	appendJSONField(&line, "ts", ts.UTC().Format(time.RFC3339Nano), true)
	appendJSONField(&line, "request_id", message.RequestID, false)
	appendJSONField(&line, "business_request_id", message.BusinessRequestID, false)
	appendJSONField(&line, "message_id", message.MessageID, false)
	appendJSONField(&line, "app", message.AppCode, false)
	appendJSONField(&line, "api_key_name", message.APIKeyName, false)
	appendJSONField(&line, "scene", message.SceneCode, false)
	appendJSONField(&line, "to_domain", message.ToDomain, false)
	appendJSONField(&line, "to_hash", message.ToHash, false)
	appendJSONField(&line, "locale", message.Locale, false)
	appendJSONField(&line, "status", string(message.Status), false)
	appendJSONField(&line, "idempotency_hash", message.IdempotencyHash, false)
	appendJSONField(&line, "request_fingerprint", message.RequestFingerprint, false)
	appendJSONField(&line, "caller_ip", message.CallerIP, false)
	appendJSONField(&line, "user_ip", message.UserIP, false)
	appendJSONField(&line, "user_id_hash", message.UserIDHash, false)
	appendJSONField(&line, "error_code", string(message.ErrorCode), false)
	appendJSONField(&line, "error_message", message.ErrorMessage, false)
	line = append(line, '}')

	return line
}

func encodeAttemptRecord(ts time.Time, attempt domain.Attempt) []byte {
	line := make([]byte, 0, 384)
	line = append(line, '{')
	appendJSONField(&line, "ts", ts.UTC().Format(time.RFC3339Nano), true)
	appendJSONField(&line, "message_id", attempt.MessageID, false)
	appendJSONField(&line, "app", attempt.AppCode, false)
	appendJSONIntField(&line, "attempt_no", attempt.AttemptNo, false)
	appendJSONField(&line, "provider", string(attempt.Provider), false)
	appendJSONField(&line, "provider_account", attempt.ProviderAccountCode, false)
	appendJSONField(&line, "provider_channel", attempt.ProviderChannelCode, false)
	appendJSONField(&line, "transport", string(attempt.Transport), false)
	appendJSONField(&line, "status", string(attempt.Status), false)
	appendJSONField(&line, "failure_class", string(attempt.FailureClass), false)
	appendJSONField(&line, "error_code", string(attempt.ErrorCode), false)
	appendJSONField(&line, "error_message", attempt.ErrorMessage, false)
	appendJSONField(&line, "provider_message_id", attempt.ProviderMessageID, false)
	appendJSONIntField(&line, "duration_ms", attempt.DurationMS, false)
	line = append(line, '}')

	return line
}

func encodeProviderEventRecord(ts time.Time, event domain.ProviderEvent) []byte {
	line := make([]byte, 0, 512)
	line = append(line, '{')
	appendJSONField(&line, "ts", ts.UTC().Format(time.RFC3339Nano), true)
	appendJSONField(&line, "message_id", event.MessageID, false)
	appendJSONField(&line, "app", event.AppCode, false)
	appendJSONField(&line, "provider", string(event.Provider), false)
	appendJSONField(&line, "provider_account", event.ProviderAccountCode, false)
	appendJSONField(&line, "provider_channel", event.ProviderChannelCode, false)
	appendJSONField(&line, "provider_message_id", event.ProviderMessageID, false)
	appendJSONField(&line, "event_type", string(event.EventType), false)
	appendJSONField(&line, "event_payload", event.EventPayload, false)
	appendJSONField(&line, "occurred_at", event.OccurredAt, false)
	line = append(line, '}')

	return line
}
