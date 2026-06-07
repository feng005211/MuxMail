# MuxMail MVP Task Breakdown

This document breaks the MuxMail MVP into stable, implementation-ready tasks.

Source of truth:

- Product design: `docs/mail-center-design.md`
- Agent rules: `AGENTS.md`

Use this document to continue work across multiple sessions. Do not rely on chat memory when implementing a task. Before starting any task, read the task entry, `docs/mail-center-design.md`, and `AGENTS.md`.

## 1. Task Rules

### 1.1 MVP Boundary

MVP includes:

- Lite mode only.
- File-based configuration.
- In-memory queue.
- In-memory fixed-window rate limiter.
- In-memory idempotency cache.
- JSONL message, attempt, and optional stats logs.
- Static YAML suppression list.
- HTTP API `POST /v1/mail/send`.
- Read-only Lite HTTP APIs for message status, attempts, provider events, suppressions, and stats summary.
- Provider event receivers for normalized MuxMail events, Resend native webhooks, and Brevo native webhooks.
- Health endpoints `/healthz` and `/readyz`.
- CLI commands `muxmail serve`, `muxmail config validate`, and `muxmail send dry-run`.
- Provider transports:
  - `mock: api`
  - `resend: api, smtp`
  - `brevo: api, smtp`

MVP excludes:

- Tenant.
- Admin UI.
- PostgreSQL runtime.
- Redis runtime.
- SMTP server.
- Marketing bulk sending.
- Attachments.
- Open/click tracking.
- Config hot reload.

### 1.2 Task ID Convention

Task IDs are stable:

- `MUX-FOUND-*`: repository and shared foundations.
- `MUX-CONFIG-*`: configuration and validation.
- `MUX-CORE-*`: domain logic and pure services.
- `MUX-LITE-*`: Lite mode infrastructure.
- `MUX-API-*`: HTTP API and runtime.
- `MUX-WORKER-*`: routing and worker execution.
- `MUX-PROVIDER-*`: provider adapters.
- `MUX-PACKAGE-*`: examples, docs, packaging.

Do not rename task IDs after implementation starts.

### 1.3 Definition of Done

Every implementation task is done only when:

- Code compiles.
- Focused tests for the task pass.
- No real provider is called in automated tests.
- No Docker command is used unless the user explicitly asks.
- Public/exported Go symbols include English comments.
- Design changes, if any, are reflected in `docs/mail-center-design.md`.
- Sensitive values are not logged.

## 2. Recommended Execution Order

1. `MUX-FOUND-001`
2. `MUX-FOUND-002`
3. `MUX-CONFIG-001`
4. `MUX-CONFIG-002`
5. `MUX-CONFIG-003`
6. `MUX-CONFIG-004`
7. `MUX-CORE-001`
8. `MUX-CORE-002`
9. `MUX-CORE-003`
10. `MUX-CORE-004`
11. `MUX-LITE-001`
12. `MUX-LITE-002`
13. `MUX-LITE-003`
14. `MUX-LITE-004`
15. `MUX-LITE-005`
16. `MUX-LITE-006`
17. `MUX-API-001`
18. `MUX-API-002`
19. `MUX-API-003`
20. `MUX-WORKER-002`
21. `MUX-WORKER-001`
22. `MUX-PROVIDER-001`
23. `MUX-TEST-001`
24. `MUX-TEST-002`
25. `MUX-TEST-003`
26. `MUX-TEST-004`
27. `MUX-TEST-005`
28. `MUX-PROVIDER-002`
29. `MUX-PROVIDER-005`
30. `MUX-PROVIDER-003`
31. `MUX-PROVIDER-004`
32. `MUX-PACKAGE-001`
33. `MUX-API-004`
34. `MUX-API-005`
35. `MUX-TEST-006`
36. `MUX-PACKAGE-002`
37. `MUX-PACKAGE-003`

## 3. Foundation Tasks

### MUX-FOUND-001: Create Go Project Skeleton

Goal:

- Initialize the Go backend project structure for the MVP.

Dependencies:

- None.

Deliverables:

- `go.mod`
- `cmd/muxmail/main.go`
- Initial internal package directories:
  - `internal/config`
  - `internal/domain`
  - `internal/api`
  - `internal/lite`
  - `internal/worker`
  - `internal/provider`
  - `internal/template`
  - `internal/observability`

Acceptance criteria:

- `go test ./...` runs successfully.
- The binary entrypoint exists but may only print help or return a placeholder error.
- No Docker files are required in this task.

Tests:

- Add at least one trivial package test to ensure the test command is wired.

Do not:

- Implement API behavior.
- Implement providers.
- Add PostgreSQL, Redis, or Admin UI.

### MUX-FOUND-002: Define Shared Domain Types and Constants

Goal:

- Create shared types and constants used across config, API, logs, routing, and worker.

Dependencies:

- `MUX-FOUND-001`

Deliverables:

- Domain types for:
  - App
  - API Key metadata
  - Scene
  - Template
  - Provider Account
  - Provider Channel
  - Route Policy
  - Rate Limit Policy
  - Message
  - Attempt
  - Suppression Entry
- Constants for:
  - message statuses
  - attempt statuses
  - failure classes
  - providers
  - transports
  - API error codes

Acceptance criteria:

- Constants match `docs/mail-center-design.md`.
- No string literals for status/failure/provider/transport are needed outside domain constants.

Tests:

- Unit tests for enum validation helpers.

Do not:

- Add database models.
- Add JSONL writers.

## 4. Configuration Tasks

### MUX-CONFIG-001: Implement Config Structures and YAML Loading

Goal:

- Load the MVP `config.yaml` shape from disk.

Dependencies:

- `MUX-FOUND-002`

Deliverables:

- Config structs matching the example in `docs/mail-center-design.md`.
- YAML loader.
- `-c / --config` required path behavior.
- Relative path resolution based on the config file directory.

Acceptance criteria:

- Missing config path fails.
- Config file read errors fail.
- Relative paths resolve from the config file directory, not the current working directory.
- Defaults are applied:
  - `server.listen = ":8080"` when empty.
  - HTTP timeouts from the design.
  - Lite defaults from the design.

Tests:

- Load a valid minimal config.
- Fail on missing file.
- Verify relative `logging.dir`, `suppression.yaml`, and `file:` references resolve from config directory.

Do not:

- Validate all business rules yet.
- Start HTTP server.

### MUX-CONFIG-002: Implement Secret Reference Resolver

Goal:

- Resolve `plain:`, `env:`, and `file:` secret references.

Dependencies:

- `MUX-CONFIG-001`

Deliverables:

- Secret resolver interface.
- Implementations for:
  - `plain:`
  - `env:`
  - `file:`
- Warning model for `plain:`.

Acceptance criteria:

- `plain:` resolves and emits a validation warning.
- `env:` fails if the environment variable is missing.
- `env:` does not trim.
- `file:` fails if unreadable.
- `file:` trims one trailing newline only.
- Resolved secret values are never returned in error messages.

Tests:

- Unit tests for all three reference types.
- Test that secret values are masked in resolver errors.

Do not:

- Store provider credentials globally in plaintext after loading.

### MUX-CONFIG-003: Implement Full Config Validation

Goal:

- Implement `muxmail config validate -c config.yaml`.

Dependencies:

- `MUX-CONFIG-001`
- `MUX-CONFIG-002`
- `MUX-FOUND-002`

Deliverables:

- CLI command `muxmail config validate`.
- Validation warning output.
- Validation error output.

Acceptance criteria:

- Checks every validation rule listed in `docs/mail-center-design.md`, including:
  - unique App code
  - App locale rules
  - API Key name uniqueness per App
  - Scene uniqueness per App
  - Template uniqueness per App
  - Template locale allowed by App
  - Provider Account uniqueness
  - Provider Channel uniqueness
  - provider and transport allowlists
  - route channel existence and enabled status
  - route policy must include `*`
  - Scene can only reference same-App Template
  - `from` domain equals `sender_domain`
  - SMTP host, port, username
  - SMTP port `587`
  - App default locale template exists
  - env/file refs are resolvable
  - `plain:` warning
  - suppression file parsing and reason enum
  - positive numeric defaults
  - retry backoff count equals max attempts
  - logging settings
  - server timeouts

Tests:

- One valid config passes.
- Each major validation family has at least one failing test.
- Warning test for `plain:`.

Do not:

- Implement runtime send.

### MUX-CONFIG-004: Add `config.example.yaml`

Goal:

- Provide a complete, safe example config.

Dependencies:

- `MUX-CONFIG-001`

Deliverables:

- `config.example.yaml`
- Matching content with `docs/mail-center-design.md`.

Acceptance criteria:

- Uses obvious placeholders only.
- Uses `plain:` only where explicitly marked as local example or uses `env:` placeholders.
- Passes `muxmail config validate -c config.example.yaml` with only expected `plain:` warnings.

Tests:

- Include example config in config validation tests.

Do not:

- Include real provider credentials.

## 5. Core Logic Tasks

### MUX-CORE-001: Implement Normalization, Hashing, and ID Helpers

Goal:

- Provide deterministic helpers for IDs, hashes, and normalization.

Dependencies:

- `MUX-FOUND-002`

Deliverables:

- `request_id` and `message_id` generation.
- `normalized_to_email`.
- `to_hash`.
- `user_id_hash`.
- `idempotency_hash`.
- `request_fingerprint`.
- Constant-time key hash comparison.

Acceptance criteria:

- IDs use `req_` / `msg_` prefixes and 26-character ULID-style body.
- Hashes match the design formulas.
- Request fingerprint uses canonical JSON with `to`, `locale`, `vars`.
- No helper logs sensitive input.

Tests:

- Hash determinism tests.
- Request fingerprint key-order tests.
- ID prefix and length tests.
- Constant-time compare behavior smoke test.

Do not:

- Add external state.

### MUX-CORE-002: Implement Request Validation

Goal:

- Validate `/v1/mail/send` input without side effects.

Dependencies:

- `MUX-CORE-001`
- `MUX-FOUND-002`

Deliverables:

- Content-Type validation.
- JSON object validation.
- Idempotency-Key validation.
- recipient validation.
- locale validation.
- context validation.
- vars validation.

Acceptance criteria:

- Enforces body and vars size limits.
- Rejects non-ASCII recipient addresses.
- Rejects display-name recipients.
- Rejects invalid locale casing such as `zh-cn`.
- Rejects nested vars/context.
- Maps failures to stable error codes.

Tests:

- Table-driven tests for each validation rule.

Do not:

- Authenticate API Key.
- Render templates.

### MUX-CORE-003: Implement Template Rendering

Goal:

- Render subject, HTML, and text bodies using Go standard templates.

Dependencies:

- `MUX-CORE-002`

Deliverables:

- Template lookup within current App.
- Locale resolution and fallback.
- Required variable checking.
- Subject render.
- HTML render.
- Text render.
- Multipart metadata decision.

Acceptance criteria:

- Uses `html/template` for HTML.
- Uses `text/template` for subject and text.
- Requires at least one of `html_body` or `text_body`.
- Missing required var returns `missing_template_var`.
- Missing locale fallback follows design.
- Render failure returns `template_render_failed`.

Tests:

- Render success in `en-US`.
- Render success in `zh-CN`.
- Fallback to App default locale.
- Missing default locale template fails.
- Missing required var fails before rate limit.

Do not:

- Send email.

### MUX-CORE-004: Implement Route Selection

Goal:

- Build candidate Provider Channel list for a send request.

Dependencies:

- `MUX-FOUND-002`
- `MUX-CONFIG-003`

Deliverables:

- Recipient domain extraction.
- Exact route match.
- `*` fallback.
- Candidate truncation by `max_attempts_per_message`.

Acceptance criteria:

- Exact domain match takes priority over `*`.
- Candidate order is preserved from config.
- No duplicate retry of same channel.
- No route returns `route_not_found`.

Tests:

- Exact match.
- Fallback match.
- Missing route.
- Candidate truncation.

Do not:

- Implement provider health, quotas, or cost.

## 6. Lite Infrastructure Tasks

### MUX-LITE-001: Implement JSONL MessageLog and Rotation

Goal:

- Append message and attempt records to JSONL files.

Dependencies:

- `MUX-CORE-001`
- `MUX-FOUND-002`

Deliverables:

- JSONL writer.
- `mail-messages.jsonl` writer.
- `mail-attempts.jsonl` writer.
- Size-based rotation.
- File and directory permissions.

Acceptance criteria:

- Creates `logging.dir` with `0750`.
- Creates JSONL files with `0640`.
- Flushes after each record.
- Does not fsync per record.
- Rotates at configured size.
- Keeps configured backup count.
- Does not write full email, full vars, API Key, provider secret, verification code, or reset token.
- Field order matches design.
- No `null` fields.

Tests:

- Append message record.
- Append attempt record.
- Rotation behavior.
- Permission behavior where test environment supports it.
- Sensitive field absence.

Do not:

- Restore queued tasks from JSONL.

### MUX-LITE-002: Implement StatsSink

Goal:

- Support `stats: off` and `stats: file`.

Dependencies:

- `MUX-LITE-001`

Deliverables:

- No-op stats sink.
- File stats sink.
- Fixed metric names.

Acceptance criteria:

- `stats: off` creates no `mail-stats.jsonl`.
- `stats: file` writes JSONL stats.
- Count metrics write value `1`.
- Duration metric writes milliseconds.
- Request-level metrics use empty provider channel and transport.

Tests:

- Off mode does nothing.
- File mode writes expected metrics.

Do not:

- Add aggregation windows.

### MUX-LITE-003: Implement SuppressionStore

Goal:

- Load and query static YAML suppression list.

Dependencies:

- `MUX-CONFIG-001`
- `MUX-CORE-001`

Deliverables:

- YAML suppression parser.
- App + normalized email lookup.

Acceptance criteria:

- Missing file means empty list.
- Invalid YAML fails validation/startup.
- Invalid reason fails validation/startup.
- Matching is by App and normalized email.

Tests:

- Missing file.
- Valid hit.
- Valid miss by App.
- Invalid reason.

Do not:

- Implement webhook writes.
- Implement admin updates.

### MUX-LITE-004: Implement Fixed-Window RateLimiter

Goal:

- Enforce first-phase fixed-window rate limits in memory.

Dependencies:

- `MUX-CORE-001`

Deliverables:

- In-memory fixed-window counter.
- Keys for email minute, email day, user IP hour, caller IP hour.
- UTC-aligned windows.

Acceptance criteria:

- Counts after template, suppression, route, and idempotency checks pass.
- Duplicate idempotent replay does not increment.
- Missing `context.user_ip` skips user IP limit.
- Any exceeded rule returns `rate_limited`.
- Old windows are deleted.

Tests:

- Email per minute.
- Email per day.
- User IP per hour.
- Caller IP per hour.
- Missing user IP.
- UTC window reset.

Do not:

- Implement sliding window.
- Implement Redis.

### MUX-LITE-005: Implement IdempotencyCache

Goal:

- Prevent duplicate send requests in Lite mode.

Dependencies:

- `MUX-CORE-001`

Deliverables:

- In-memory cache with capacity and TTL.
- Lookup by App + Scene + idempotency hash.
- Store message ID and request fingerprint.

Acceptance criteria:

- Same fingerprint returns existing message ID.
- Different fingerprint returns `idempotency_conflict`.
- TTL expiry treats request as new.
- Capacity eviction removes earliest `created_at`.
- Cache is marked queued only after queue enqueue succeeds.

Tests:

- Replay success.
- Conflict.
- TTL expiry.
- Capacity eviction.
- Not marked if enqueue fails.

Do not:

- Persist idempotency across process restart.

### MUX-LITE-006: Implement MemoryQueue

Goal:

- Queue messages and delayed retry attempts in Lite mode.

Dependencies:

- `MUX-FOUND-002`

Deliverables:

- In-memory queue.
- Delayed scheduling for retrying messages.
- Queue capacity accounting.

Acceptance criteria:

- Queued and backoff-delayed messages count toward capacity.
- In-flight messages do not count toward capacity.
- Full queue returns `queue_full`.
- Graceful shutdown discards not-yet-started tasks.

Tests:

- Enqueue.
- Full queue.
- Delayed retry counts toward capacity.
- In-flight capacity behavior.

Do not:

- Implement Redis queue.

## 7. API Tasks

### MUX-API-001: Implement HTTP Server Runtime

Goal:

- Start HTTP server with the configured runtime behavior.

Dependencies:

- `MUX-CONFIG-003`

Deliverables:

- `muxmail serve -c config.yaml`.
- HTTP timeouts.
- `/healthz`.
- `/readyz`.
- graceful shutdown.

Acceptance criteria:

- `-c / --config` is required.
- `/healthz` returns `{"status":"ok"}`.
- `/readyz` returns `{"status":"ok"}` only after config, logging, and queue are ready.
- CORS is disabled.
- OPTIONS returns 404.
- TLS is not implemented in process.
- SIGINT/SIGTERM behavior matches design.

Tests:

- Server starts with valid config.
- Health endpoint.
- Ready endpoint.
- CORS headers absent.
- OPTIONS 404.

Do not:

- Implement send endpoint in this task.

### MUX-API-002: Implement API Authentication

Goal:

- Authenticate API Key and resolve App.

Dependencies:

- `MUX-CONFIG-002`
- `MUX-CORE-001`
- `MUX-API-001`

Deliverables:

- Bearer token parser.
- SHA-256 key hash matching.
- Constant-time compare.
- App and API key metadata in request context.

Acceptance criteria:

- Missing auth returns `unauthorized`.
- Invalid auth returns `unauthorized`.
- Disabled App returns `app_disabled`.
- Disabled key returns `unauthorized`.
- Errors do not reveal whether key exists.

Tests:

- Valid key.
- Missing header.
- Wrong scheme.
- Invalid key.
- Disabled App.
- Disabled key.

Do not:

- Accept App identity from body.

### MUX-API-003: Implement Send Request Pipeline

Goal:

- Implement `POST /v1/mail/send` through enqueue.

Dependencies:

- `MUX-API-002`
- `MUX-CORE-002`
- `MUX-CORE-003`
- `MUX-CORE-004`
- `MUX-LITE-001`
- `MUX-LITE-003`
- `MUX-LITE-004`
- `MUX-LITE-005`
- `MUX-LITE-006`

Deliverables:

- Full request pipeline in the exact design order.
- Error response writer.
- Success response writer.
- Stats hooks.

Acceptance criteria:

- Returns `202 Accepted` only after JSONL queued record and queue enqueue succeed.
- Replayed idempotency returns new `request_id` and original `message_id`.
- Replayed idempotency does not enqueue, rate limit, or write new message record.
- Template, suppression, and route failures do not consume rate limit.
- Queue full returns `503 queue_full`.
- MessageLog or Queue write failure returns `500 internal_error`.
- Response never includes provider delivery status.

Tests:

- Successful queued response.
- Idempotent replay.
- Idempotency conflict.
- Suppressed recipient.
- Route not found.
- Rate limited.
- Queue full.
- MessageLog failure.
- Invalid JSON.
- Unsupported media type.

Do not:

- Call provider directly from API handler.

### MUX-API-004: Implement Lite Read APIs

Goal:

- Expose App-scoped read APIs over Lite JSONL and suppression data.

Dependencies:

- `MUX-API-002`
- `MUX-LITE-001`
- `MUX-LITE-002`
- `MUX-LITE-003`

Deliverables:

- `GET /v1/mail/messages`
- `GET /v1/mail/messages/failed`
- `GET /v1/mail/messages/{message_id}`
- `GET /v1/mail/messages/{message_id}/attempts`
- `GET /v1/mail/messages/{message_id}/events`
- `GET /v1/suppressions`
- `GET /v1/provider-events`
- `GET /v1/stats/summary`
- OpenAPI entries for every read API.

Acceptance criteria:

- Every read API is scoped to the authenticated App.
- Message and provider event list APIs support `limit` with default `50` and maximum `200`.
- Message list supports optional `status` and `scene` filters.
- Failed message list is equivalent to message list with `status=failed`.
- Message status and timeline APIs return `message_not_found` for other Apps or missing messages.
- Suppression list supports optional `reason` and `email` filters.
- Provider event list supports optional `provider` and `event_type` filters.
- Stats summary supports only `1h`, `24h`, and `7d` windows.
- Read APIs do not expose full recipient email, caller IP, user IP, template variables, API keys, provider secrets, or raw webhook payloads, except `GET /v1/suppressions`, which may return full email addresses for the authenticated App.

Tests:

- Message list and failed list filters.
- Message status app isolation.
- Attempt timeline.
- Provider event timeline and list filters.
- Suppression list filters.
- Stats summary windows and stats-off behavior.
- OpenAPI path coverage.

Do not:

- Add PostgreSQL query paths.
- Mutate message state from read APIs.

### MUX-API-005: Implement Provider Event Receivers

Goal:

- Accept provider delivery events and advance Lite message status while preserving sensitive-data boundaries.

Dependencies:

- `MUX-API-004`
- `MUX-LITE-001`
- `MUX-LITE-003`

Deliverables:

- `POST /v1/provider-events` for normalized MuxMail provider events.
- `POST /v1/provider-events/resend` for Resend native webhooks.
- `POST /v1/provider-events/brevo` for Brevo native webhooks.
- Webhook secret validation.
- Event identity de-duplication.
- Bounce and complaint suppression upsert.
- OpenAPI entries for provider event receivers.

Acceptance criteria:

- Webhook receivers are disabled unless `webhooks.enabled` is true.
- Normalized provider events require `webhooks.shared_secret_ref` and Bearer authentication.
- Resend native events require `webhooks.resend_secret_ref` and valid Svix headers.
- Brevo native events require `webhooks.brevo_token_ref` and Bearer authentication.
- Supported events are `delivered`, `bounced`, and `complained`.
- Duplicate event identity does not append duplicate event records or message status records.
- Bounce and complaint events update `suppression.yaml` only when a recipient email is present.
- Event JSONL records do not include full recipient email or raw provider payload.
- Errors return stable API error codes.

Tests:

- Disabled webhook rejection.
- Normalized event accepted.
- Duplicate event ignored.
- Delivered advances message status.
- Bounce and complaint upsert suppression.
- Resend signature verification and event mapping.
- Brevo token verification and event mapping.
- Sensitive field absence in JSONL.

Do not:

- Call provider APIs from webhook handlers.
- Require Redis or PostgreSQL.

## 8. Worker and Routing Tasks

### MUX-WORKER-001: Implement Worker Attempt Loop

Goal:

- Consume queued messages and perform attempts with backoff.

Dependencies:

- `MUX-LITE-001`
- `MUX-LITE-006`
- `MUX-CORE-004`
- `MUX-WORKER-002`

Deliverables:

- Worker goroutine pool.
- Attempt numbering.
- Retry/backoff scheduling.
- Message status logging.
- Attempt status logging.

Acceptance criteria:

- `attempt_no` starts at 1.
- Each attempt calls at most one Provider Channel.
- Backoff follows `retry_backoff_seconds`.
- `retry_after_seconds` respected with 300-second cap.
- Temporary and channel failures move to next channel.
- Permanent message failures stop immediately.
- Final failure writes `provider_unavailable` when attempts are exhausted.

Tests:

- Success first attempt.
- Temporary fail then success.
- Channel fail then success.
- Permanent failure stops.
- Max attempts exhausted.
- Retry-after cap.

Do not:

- Implement provider-specific HTTP calls here.

### MUX-WORKER-002: Implement Provider Interface and Fake Provider

Goal:

- Define provider adapter contract and fake implementation for tests.

Dependencies:

- `MUX-FOUND-002`

Deliverables:

- Provider interface.
- Provider request struct.
- Provider accepted result.
- Provider failed result.
- Fake provider adapter.

Acceptance criteria:

- Result shape matches design.
- Fake provider can script accepted, temporary failure, channel failure, and permanent failure.
- Worker tests use fake provider only.

Tests:

- Fake provider result mapping.
- Worker integration with fake provider.

Do not:

- Call real providers.

## 9. Provider Tasks

### MUX-PROVIDER-001: Implement Mock API Provider

Goal:

- Provide network-free mock provider for local use and tests.

Dependencies:

- `MUX-WORKER-002`

Deliverables:

- `mock: api` adapter.

Acceptance criteria:

- Does not access network.
- Always accepts unless configured test hook says otherwise.
- Returns `provider_message_id = mock_{message_id}`.

Tests:

- Mock send success.

Do not:

- Add SMTP mock server.

### MUX-PROVIDER-002: Implement SMTP Transport Shared Client

Goal:

- Implement shared SMTP client behavior for Brevo SMTP and Resend SMTP.

Dependencies:

- `MUX-WORKER-002`

Deliverables:

- SMTP transport client.
- STARTTLS on port 587.
- MIME message builder.
- Multipart alternative support.

Acceptance criteria:

- Requires host, port, username.
- Uses `password_ref` or account API key fallback.
- Supports subject, HTML, and text.
- Does not support attachments, CC, BCC, Reply-To, List-Unsubscribe.
- Classifies SMTP 4xx as temporary failure.
- Classifies SMTP 5xx as message permanent failure except auth/domain failures mapped to channel failure.

Tests:

- Use fake/local test SMTP server only.
- MIME text only.
- MIME HTML only.
- MIME multipart alternative.
- SMTP 4xx mapping.
- SMTP 5xx mapping.

Do not:

- Use Docker for SMTP tests.
- Call Resend or Brevo.

### MUX-PROVIDER-003: Implement Resend API Adapter

Goal:

- Send mail through Resend Email API.

Dependencies:

- `MUX-WORKER-002`

Deliverables:

- Resend API adapter.
- Request mapping.
- Response mapping.
- Error classification.

Acceptance criteria:

- Uses provider timeout.
- Disables open/click tracking when supported by API.
- Does not log raw provider response.
- Maps HTTP 429/5xx/network timeout to temporary failure.
- Maps auth/domain verification to channel failure.
- Maps invalid recipient to message permanent failure.

Tests:

- Use `httptest.Server`.
- Accepted response.
- HTTP 429.
- HTTP 5xx.
- Auth failure.
- Invalid recipient.

Do not:

- Call real Resend.

### MUX-PROVIDER-004: Implement Brevo API Adapter

Goal:

- Send mail through Brevo API.

Dependencies:

- `MUX-WORKER-002`

Deliverables:

- Brevo API adapter.
- Request mapping.
- Response mapping.
- Error classification.

Acceptance criteria:

- Uses provider timeout.
- Disables tracking when supported by API.
- Does not log raw provider response.
- Maps temporary and permanent failures according to design.

Tests:

- Use `httptest.Server`.
- Accepted response.
- HTTP 429.
- HTTP 5xx.
- Auth failure.
- Invalid recipient.

Do not:

- Call real Brevo.

### MUX-PROVIDER-005: Wire Resend SMTP and Brevo SMTP Channels

Goal:

- Bind shared SMTP transport into provider channel selection.

Dependencies:

- `MUX-PROVIDER-002`
- `MUX-WORKER-001`

Deliverables:

- Resend SMTP adapter registration.
- Brevo SMTP adapter registration.

Acceptance criteria:

- `resend + smtp` uses shared SMTP client.
- `brevo + smtp` uses shared SMTP client.
- Provider/account/channel metadata appears correctly in attempt logs.

Tests:

- Worker sends through fake SMTP channel for Resend metadata.
- Worker sends through fake SMTP channel for Brevo metadata.

Do not:

- Add provider-specific SMTP behavior unless required by config.

## 10. CLI and Packaging Tasks

### MUX-PACKAGE-001: Implement `muxmail send dry-run`

Goal:

- Provide deterministic dry-run output.

Dependencies:

- `MUX-CONFIG-003`
- `MUX-CORE-003`
- `MUX-CORE-004`

Deliverables:

- CLI command `muxmail send dry-run`.
- JSON output matching design.

Acceptance criteria:

- Does not call provider.
- Does not enqueue.
- Does not increment rate limits.
- Does not write attempts.
- Does not output full recipient, vars, provider secret, or API Key.

Tests:

- Dry-run valid output.
- Dry-run invalid scene.
- Dry-run missing template var.
- Dry-run route selection.

Do not:

- Add interactive prompts.

### MUX-PACKAGE-002: Add Build, Test, and Release Metadata

Goal:

- Make the project buildable and testable in a standard Go workflow.

Dependencies:

- `MUX-API-003`
- `MUX-WORKER-001`

Deliverables:

- `README.md` minimal MVP usage.
- `.gitignore`.
- Optional `Makefile` or documented commands.

Acceptance criteria:

- Default repository verification command is `make verify`.
- `make verify` runs `go test ./...`, `go vet ./...`, example config validation, container example strict validation, dry-run, and build.
- Generated logs and local config overrides are ignored.
- README documents Lite mode and non-goals.

Tests:

- `make verify`.

Do not:

- Require Docker to run tests.

### MUX-PACKAGE-003: Add Docker Packaging Files

Goal:

- Provide single-container deployment artifacts.

Dependencies:

- `MUX-PACKAGE-002`

Deliverables:

- `Dockerfile`
- `.dockerignore`
- Optional `compose.example.yaml`
- Documentation for 1Panel/reverse-proxy deployment.

Acceptance criteria:

- Container runs `muxmail serve -c /etc/muxmail/config.yaml`.
- Exposes port 8080.
- Uses volume paths for config and data.
- Docker build context excludes local logs, local config overrides, Go cache, VCS metadata, and build outputs.
- Documents that TLS should terminate at reverse proxy.

Tests:

- Do not run Docker unless user explicitly asks.
- Validate Dockerfile syntax by review only unless Docker verification is requested.

Do not:

- Add PostgreSQL or Redis to the required compose file.

## 11. Integration Test Milestones

### MUX-TEST-001: Send API Happy Path With Mock Provider

Dependencies:

- `MUX-API-003`
- `MUX-WORKER-001`
- `MUX-PROVIDER-001`

Scenario:

- Start in-process server.
- Submit valid request.
- Assert 202 response.
- Worker sends via mock.
- JSONL contains queued, sending, sent.

### MUX-TEST-002: Failover Path With Fake Provider

Dependencies:

- `MUX-WORKER-002`

Scenario:

- First channel returns temporary failure.
- Second channel accepts.
- Assert attempt logs and final sent status.

### MUX-TEST-003: Idempotency Replay

Dependencies:

- `MUX-API-003`

Scenario:

- Submit same request twice.
- Assert two different request IDs.
- Assert same message ID.
- Assert one queued message.
- Assert no second rate-limit increment.

### MUX-TEST-004: Rate Limit Rejection

Dependencies:

- `MUX-LITE-004`
- `MUX-API-003`

Scenario:

- Exceed same-email minute limit.
- Assert 429.
- Assert no queue enqueue.

### MUX-TEST-005: Suppression Rejection

Dependencies:

- `MUX-LITE-003`
- `MUX-API-003`

Scenario:

- Suppressed recipient submits request.
- Assert `suppressed_recipient`.
- Assert no rate-limit increment.
- Assert no queue enqueue.

### MUX-TEST-006: Provider Event Status Advancement

Dependencies:

- `MUX-API-005`

Scenario:

- Start in-process server with webhooks enabled.
- Append a sent message snapshot.
- Submit a provider delivered event.
- Assert 202 response.
- Assert message status advances to `delivered`.
- Assert event timeline includes the provider event.
- Submit the same event again.
- Assert duplicate event does not append another event or status record.

## 12. Session Handoff Template

Use this template at the end of any implementation session:

```text
Completed tasks:
- MUX-...

Files changed:
- ...

Verification:
- make verify

Design changes:
- none / updated docs/mail-center-design.md

Next recommended task:
- MUX-...

Known blockers:
- none / ...
```

## 13. Current Implementation Snapshot

As of the current repository state, the MVP task sequence through `MUX-PACKAGE-003` has implementation coverage in the codebase.

Completed task range:

- `MUX-FOUND-001` through `MUX-PACKAGE-003`
- Integration milestones `MUX-TEST-001` through `MUX-TEST-006`

Current verification command:

```text
make verify
```

Next recommended work:

- Commit the current MVP snapshot once reviewed.
- Start a new task document before beginning post-MVP work such as PostgreSQL, Redis, admin UI, or additional providers.

Known blockers:

- none
