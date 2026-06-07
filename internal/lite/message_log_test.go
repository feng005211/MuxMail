package lite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/muxmail/muxmail/internal/domain"
)

func TestMessageLogAppendMessageRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append message: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, messagesFilename))
	wantPrefix := `{"ts":"2026-05-28T03:04:05.123456789Z","request_id":"req_01ABC","business_request_id":"biz_123","message_id":"msg_01ABC","app":"project_a","api_key_name":"default","scene":"register_code","to_domain":"example.com","to_hash":"`
	if !strings.HasPrefix(line, wantPrefix) {
		t.Fatalf("message field order mismatch:\n%s", line)
	}
	if strings.Contains(line, `"to_email"`) ||
		strings.Contains(line, "user@example.com") ||
		strings.Contains(line, "Your code is 123456") ||
		strings.Contains(line, "<p>123456</p>") ||
		strings.Contains(line, "mk_live") {
		t.Fatalf("message record leaked sensitive value: %s", line)
	}
	if strings.Contains(line, "null") {
		t.Fatalf("message record contains null: %s", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("message record must be valid JSON: %v", err)
	}
	if decoded["status"] != string(domain.MessageStatusQueued) {
		t.Fatalf("expected queued status, got %v", decoded["status"])
	}
}

func TestMessageLogAppendAttemptRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	attempt := domain.Attempt{
		MessageID:           "msg_01ABC",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSent,
		FailureClass:        domain.FailureClassNone,
		ErrorCode:           "",
		ProviderMessageID:   "provider_123",
		DurationMS:          42,
	}
	if err := log.AppendAttempt(attempt); err != nil {
		t.Fatalf("append attempt: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, attemptsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","attempt_no":1,"provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","transport":"api","status":"sent","failure_class":"","error_code":"","provider_message_id":"provider_123","duration_ms":42}`
	if line != want {
		t.Fatalf("attempt record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestMessageLogAppendProviderEventRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := log.AppendProviderEvent(event); err != nil {
		t.Fatalf("append provider event: %v", err)
	}

	line := readSingleLine(t, filepath.Join(dir, eventsFilename))
	want := `{"ts":"2026-05-28T03:04:05.123456789Z","message_id":"msg_01ABC","app":"project_a","provider":"resend","provider_account":"resend_main","provider_channel":"resend_auth_api","provider_message_id":"provider_123","event_type":"delivered","event_payload":"{\"redacted\":true}","occurred_at":"2026-05-28T03:00:00Z"}`
	if line != want {
		t.Fatalf("event record mismatch:\nwant %s\ngot  %s", want, line)
	}
}

func TestMessageLogAppendProviderEventFailsWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	err := log.AppendProviderEvent(domain.ProviderEvent{
		MessageID: "msg_01ABC",
		AppCode:   "project_a",
		EventType: domain.ProviderEventDelivered,
	})
	if err == nil {
		t.Fatal("expected disabled event log to fail")
	}
	if _, statErr := os.Stat(filepath.Join(dir, eventsFilename)); !os.IsNotExist(statErr) {
		t.Fatalf("expected event log file to be absent, got %v", statErr)
	}
}

func TestMessageLogHasProviderEvent(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	event := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	if err := log.AppendProviderEvent(event); err != nil {
		t.Fatalf("append provider event: %v", err)
	}

	found, err := log.HasProviderEvent(event)
	if err != nil {
		t.Fatalf("check provider event duplicate: %v", err)
	}
	if !found {
		t.Fatal("expected provider event duplicate check to hit")
	}
}

func TestMessageLogListProviderEvents(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	first := domain.ProviderEvent{
		MessageID:           "msg_01ABC",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_123",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	second := first
	second.Provider = domain.ProviderBrevo
	second.ProviderAccountCode = "brevo_main"
	second.ProviderChannelCode = "brevo_auth_api"
	second.ProviderMessageID = "provider_456"
	second.EventType = domain.ProviderEventBounced
	second.OccurredAt = "2026-05-28T03:10:00Z"
	if err := log.AppendProviderEvent(first); err != nil {
		t.Fatalf("append first provider event: %v", err)
	}
	if err := log.AppendProviderEvent(second); err != nil {
		t.Fatalf("append second provider event: %v", err)
	}

	events, err := log.ListProviderEvents("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("list provider events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two provider events, got %+v", events)
	}
	if events[0].EventType != domain.ProviderEventDelivered || events[1].EventType != domain.ProviderEventBounced {
		t.Fatalf("expected event order to be preserved, got %+v", events)
	}
	if events[0].Provider != domain.ProviderResend || events[1].Provider != domain.ProviderBrevo {
		t.Fatalf("unexpected provider event metadata: %+v", events)
	}
}

func TestMessageLogListRecentProviderEvents(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLogWithEvents(t, dir, 1<<20)
	defer log.Close()

	first := domain.ProviderEvent{
		MessageID:           "msg_01AAA",
		AppCode:             "project_a",
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		ProviderMessageID:   "provider_111",
		EventType:           domain.ProviderEventDelivered,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:00:00Z",
	}
	second := domain.ProviderEvent{
		MessageID:           "msg_01BBB",
		AppCode:             "project_a",
		Provider:            domain.ProviderBrevo,
		ProviderAccountCode: "brevo_main",
		ProviderChannelCode: "brevo_auth_api",
		ProviderMessageID:   "provider_222",
		EventType:           domain.ProviderEventBounced,
		EventPayload:        `{"redacted":true}`,
		OccurredAt:          "2026-05-28T03:10:00Z",
	}
	other := second
	other.AppCode = "project_b"
	other.ProviderMessageID = "provider_333"
	if err := log.AppendProviderEvent(first); err != nil {
		t.Fatalf("append first provider event: %v", err)
	}
	if err := log.AppendProviderEvent(second); err != nil {
		t.Fatalf("append second provider event: %v", err)
	}
	if err := log.AppendProviderEvent(other); err != nil {
		t.Fatalf("append other app provider event: %v", err)
	}

	events, err := log.ListRecentProviderEvents("project_a", ProviderEventListFilter{
		Limit:     1,
		Provider:  domain.ProviderBrevo,
		EventType: domain.ProviderEventBounced,
	})
	if err != nil {
		t.Fatalf("list recent provider events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one filtered event, got %+v", events)
	}
	if events[0].MessageID != "msg_01BBB" || events[0].Provider != domain.ProviderBrevo || events[0].EventType != domain.ProviderEventBounced {
		t.Fatalf("unexpected filtered event: %+v", events[0])
	}
}

func TestMessageLogListAttempts(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := domain.Attempt{
		MessageID:           "msg_01ABC",
		AttemptNo:           1,
		Provider:            domain.ProviderResend,
		ProviderAccountCode: "resend_main",
		ProviderChannelCode: "resend_auth_api",
		Transport:           domain.TransportAPI,
		Status:              domain.AttemptStatusSending,
		FailureClass:        domain.FailureClassNone,
	}
	second := first
	second.Status = domain.AttemptStatusSent
	second.ProviderMessageID = "provider_123"
	second.DurationMS = 42
	if err := log.AppendAttempt(first); err != nil {
		t.Fatalf("append first attempt record: %v", err)
	}
	if err := log.AppendAttempt(second); err != nil {
		t.Fatalf("append second attempt record: %v", err)
	}

	attempts, err := log.ListAttempts("msg_01ABC")
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected two attempt records, got %+v", attempts)
	}
	if attempts[0].Status != domain.AttemptStatusSending || attempts[1].Status != domain.AttemptStatusSent {
		t.Fatalf("expected attempt order to be preserved, got %+v", attempts)
	}
	if attempts[1].ProviderMessageID != "provider_123" || attempts[1].DurationMS != 42 {
		t.Fatalf("unexpected attempt metadata: %+v", attempts[1])
	}
}

func TestMessageLogListLatestMessages(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := testMessage()
	first.MessageID = "msg_01AAA"
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusQueued
	second := testMessage()
	second.MessageID = "msg_01BBB"
	second.SceneCode = "reset_password"
	second.Status = domain.MessageStatusFailed
	if err := log.AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{})
	if err != nil {
		t.Fatalf("list latest messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected two latest messages, got %+v", messages)
	}
	if messages[0].MessageID != "msg_01BBB" || messages[1].MessageID != "msg_01AAA" {
		t.Fatalf("expected latest messages to be sorted descending, got %+v", messages)
	}
}

func TestMessageLogListLatestMessagesFiltersAndLimits(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	first := testMessage()
	first.MessageID = "msg_01AAA"
	first.SceneCode = "register_code"
	first.Status = domain.MessageStatusQueued
	second := testMessage()
	second.MessageID = "msg_01BBB"
	second.SceneCode = "register_code"
	second.Status = domain.MessageStatusFailed
	third := testMessage()
	third.MessageID = "msg_01CCC"
	third.SceneCode = "reset_password"
	third.Status = domain.MessageStatusFailed
	if err := log.AppendMessage(first); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(second); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	if err := log.AppendMessage(third); err != nil {
		t.Fatalf("append third message: %v", err)
	}

	messages, err := log.ListLatestMessages("project_a", MessageListFilter{
		Limit:  1,
		Status: domain.MessageStatusFailed,
		Scene:  "register_code",
	})
	if err != nil {
		t.Fatalf("list filtered latest messages: %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "msg_01BBB" {
		t.Fatalf("expected one filtered message, got %+v", messages)
	}
}

func TestMessageLogRotatesBySize(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 650)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, messagesFilename+".1")); err != nil {
		t.Fatalf("expected rotated backup: %v", err)
	}
	current := readSingleLine(t, filepath.Join(dir, messagesFilename))
	backup := readSingleLine(t, filepath.Join(dir, messagesFilename+".1"))
	if current == "" || backup == "" {
		t.Fatalf("expected current and backup records")
	}
}

func TestMessageLogFindLatestMessage(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append queued message: %v", err)
	}
	message.Status = domain.MessageStatusSent
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append sent message: %v", err)
	}

	snapshot, found, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if !found {
		t.Fatal("expected message to be found")
	}
	if snapshot.Status != domain.MessageStatusSent {
		t.Fatalf("expected latest sent status, got %+v", snapshot)
	}
	if snapshot.ToDomain != "example.com" || snapshot.ToHash == "" || snapshot.BusinessRequestID != "biz_123" {
		t.Fatalf("expected safe message fields, got %+v", snapshot)
	}
}

func TestMessageLogFindLatestMessageIsAppScoped(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append message: %v", err)
	}

	_, found, err := log.FindLatestMessage("project_b", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if found {
		t.Fatal("expected app-scoped lookup to hide other app message")
	}
}

func TestMessageLogFindLatestMessageScansRotatedBackups(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 650)
	defer log.Close()

	message := testMessage()
	if err := log.AppendMessage(message); err != nil {
		t.Fatalf("append first message: %v", err)
	}
	other := testMessage()
	other.MessageID = "msg_other"
	if err := log.AppendMessage(other); err != nil {
		t.Fatalf("append rotating message: %v", err)
	}

	snapshot, found, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err != nil {
		t.Fatalf("find latest message: %v", err)
	}
	if !found {
		t.Fatal("expected message from rotated backup to be found")
	}
	if snapshot.MessageID != "msg_01ABC" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestMessageLogFindLatestMessageRejectsMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	if err := os.WriteFile(filepath.Join(dir, messagesFilename), []byte("{bad json}\n"), filePerm); err != nil {
		t.Fatalf("write malformed record: %v", err)
	}

	_, _, err := log.FindLatestMessage("project_a", "msg_01ABC")
	if err == nil {
		t.Fatal("expected malformed record to fail")
	}
}

func TestMessageLogFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows permission bits do not reliably reflect POSIX modes")
	}

	dir := t.TempDir()
	log := openTestMessageLog(t, dir, 1<<20)
	defer log.Close()

	if err := log.AppendMessage(testMessage()); err != nil {
		t.Fatalf("append message: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != directoryPerm {
		t.Fatalf("expected directory perm %o, got %o", directoryPerm, got)
	}

	fileInfo, err := os.Stat(filepath.Join(dir, messagesFilename))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != filePerm {
		t.Fatalf("expected file perm %o, got %o", filePerm, got)
	}
}

func openTestMessageLog(t *testing.T, dir string, maxBytes int64) *MessageLog {
	t.Helper()

	return openTestMessageLogConfigured(t, dir, maxBytes, false)
}

func openTestMessageLogWithEvents(t *testing.T, dir string, maxBytes int64) *MessageLog {
	t.Helper()

	return openTestMessageLogConfigured(t, dir, maxBytes, true)
}

func openTestMessageLogConfigured(t *testing.T, dir string, maxBytes int64, eventsEnabled bool) *MessageLog {
	t.Helper()

	log, err := NewMessageLog(MessageLogConfig{
		Dir:           dir,
		MaxBytes:      maxBytes,
		MaxBackups:    2,
		EventsEnabled: eventsEnabled,
		Now: func() time.Time {
			return time.Date(2026, 5, 28, 3, 4, 5, 123456789, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("open message log: %v", err)
	}

	return log
}

func testMessage() domain.Message {
	normalizedEmail := domain.NormalizeEmail("user@example.com")
	return domain.Message{
		RequestID:          "req_01ABC",
		BusinessRequestID:  "biz_123",
		MessageID:          "msg_01ABC",
		AppCode:            "project_a",
		APIKeyName:         "default",
		SceneCode:          "register_code",
		ToEmail:            "user@example.com",
		NormalizedToEmail:  normalizedEmail,
		ToDomain:           "example.com",
		ToHash:             domain.ToHash("project_a", normalizedEmail),
		Locale:             "en-US",
		Subject:            "Your code is 123456",
		HTMLBody:           "<p>123456</p>",
		TextBody:           "123456",
		Status:             domain.MessageStatusQueued,
		IdempotencyHash:    domain.IdempotencyHash("project_a", "register_code", "idem-123"),
		RequestFingerprint: "fingerprint_123",
		CallerIP:           "127.0.0.1",
		UserIP:             "1.2.3.4",
		UserIDHash:         domain.UserIDHash("project_a", "10001"),
		ErrorCode:          "",
		ErrorMessage:       "",
	}
}

func readSingleLine(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one line in %s, got %d: %q", path, len(lines), string(data))
	}

	return lines[0]
}
