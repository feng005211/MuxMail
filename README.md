# MuxMail

[中文说明](README.zh-CN.md)

MuxMail is a self-hosted transactional mail routing gateway for verification codes, password resets, and other critical product emails.

The MVP is intentionally lightweight: one process, one container, file-based configuration, in-memory queue/rate limiting/idempotency, static suppression YAML, and JSONL audit logs.

## Current MVP

- App-scoped API keys, scenes, templates, route policies, and provider channels.
- `POST /v1/mail/send` asynchronous send API.
- App-scoped message, attempt, suppression, provider event, and stats summary read APIs.
- Optional built-in Lite Admin UI at `/admin/` for dashboards, message inspection, provider events, suppressions, test sends, and safe config summaries.
- Optional normalized, Resend, and Brevo provider event receivers.
- Provider failover through Mock API, Resend API/SMTP, and Brevo API/SMTP.
- Fixed-window rate limits and idempotency in memory.
- JSONL message, attempt, event, and optional stats logs.
- `muxmail config validate` and `muxmail send dry-run`.

Supported providers, daily/monthly quota notes, and public pricing references are listed in [docs/provider-support.md](docs/provider-support.md).

## Non-Goals For MVP

- No Tenant model.
- No PostgreSQL or Redis requirement.
- No requirement to use Lite Admin, and no online config editing in Lite Admin.
- No inbound SMTP server.
- No provider webhook receiver enabled by default.
- No marketing bulk sending, attachments, open tracking, or click tracking.

## Verify

```powershell
$env:GOCACHE = (Join-Path (Get-Location) '.gocache')
go test ./...
go vet ./...
make build
```

On systems with a writable default Go cache, the same commands work without setting `GOCACHE`.

With `make` available:

```sh
make verify
```

`make verify` also runs `npm ci` inside `web/admin`, builds the Lite Admin UI, temporarily stages it for Go embedding, builds the binary, and restores the lightweight placeholder assets in the source tree.

## Validate And Dry-Run

Validate the example config:

```powershell
go run ./cmd/muxmail config validate -c config.example.yaml
```

Validate a production config and reject `plain:` secret references:

```powershell
go run ./cmd/muxmail config validate -c config.yaml --strict
```

`config.example.yaml` intentionally uses `plain:` placeholders, so strict validation is meant for real local or production config files after secrets are moved to `env:` or `file:` references.
For container deployments, start from `config.container.example.yaml`; it already uses `env:` secret references and `/var/lib/muxmail` data paths.

Dry-run one message without enqueueing or calling a provider:

```powershell
go run ./cmd/muxmail send dry-run -c config.example.yaml --app project_a --scene register_code --to user@example.com --locale en-US --var code=123456 --var expire_minutes=10
```

Equivalent make targets:

```sh
make validate-example
make validate-container-example
make dry-run-example
```

## Run Locally

1. Copy `config.example.yaml` to `config.yaml`.
2. Replace example secrets with `env:` or `file:` references for real deployments.
3. Set `logging.dir` and `suppression_file` to writable local paths.
4. Start the server.

```powershell
go run ./cmd/muxmail serve -c config.yaml
```

For source-tree runs, build `web/admin/dist` first with `make admin-build` or `cd web/admin && npm run build`; Docker image builds do this automatically.

Open the built-in Lite Admin UI:

```text
http://localhost:8080/admin/
```

The admin UI uses an App API key in the current browser session to call the same App-scoped APIs. It keeps the key in page memory only, does not persist it in browser storage, does not expose provider secrets or edit YAML configuration, and receives `/v1/` API responses with `Cache-Control: no-store`. The `/admin/` HTML shell is also served with `no-store` so browser caches do not keep an old Admin bundle after upgrades.

## API

Current version:

```text
0.1.0
```

Machine-readable API contract:

```text
docs/openapi.yaml
```

Release notes:

```text
CHANGELOG.md
```

Project process:

```text
CONTRIBUTING.md
SECURITY.md
```

```http
POST /v1/mail/send
Authorization: Bearer <app_api_key>
Content-Type: application/json
Idempotency-Key: <stable-request-key>
```

```json
{
  "scene": "register_code",
  "to": "user@example.com",
  "locale": "en-US",
  "vars": {
    "code": "123456",
    "expire_minutes": "10"
  },
  "context": {
    "user_ip": "203.0.113.10",
    "request_id": "business-request-001"
  }
}
```

The API returns `202 Accepted` after validation, log append, and queue enqueue. Provider delivery happens asynchronously in the worker loop.

List recent message snapshots for the authenticated App:

```http
GET /v1/mail/messages?limit=50&status=failed&scene=register_code
Authorization: Bearer <app_api_key>
```

List recent failed messages for the authenticated App:

```http
GET /v1/mail/messages/failed?limit=50&scene=register_code
Authorization: Bearer <app_api_key>
```

Query the latest Lite-mode message status:

```http
GET /v1/mail/messages/{message_id}
Authorization: Bearer <app_api_key>
```

The response is App-scoped and does not include full recipient email, message body, template variables, caller IP, or user IP.

Query the provider event timeline for one message:

```http
GET /v1/mail/messages/{message_id}/events
Authorization: Bearer <app_api_key>
```

The response is App-scoped and returns the recorded provider event sequence without exposing recipient email or raw provider webhook payloads.

Query the provider attempt timeline for one message:

```http
GET /v1/mail/messages/{message_id}/attempts
Authorization: Bearer <app_api_key>
```

The response is App-scoped and returns the recorded provider attempt sequence, including sending and final sent or failed records for each attempt number.

Query the suppression list for the authenticated App:

```http
GET /v1/suppressions?limit=50&reason=complaint&email=user@example.com
Authorization: Bearer <app_api_key>
```

This response intentionally includes full email addresses because suppression review and manual cleanup need the real recipient identity.

List recent provider events for the authenticated App:

```http
GET /v1/provider-events?limit=50&provider=brevo&event_type=bounced
Authorization: Bearer <app_api_key>
```

This response is App-scoped and returns recent provider events without exposing raw webhook payloads or recipient email addresses.

Query Lite-mode stats summary:

```http
GET /v1/stats/summary?window=24h
Authorization: Bearer <app_api_key>
```

Supported windows are `1h`, `24h`, and `7d`. With `stats: off`, the endpoint returns an empty summary. With `stats: file`, it aggregates `mail-stats.jsonl` for the authenticated App.

Provider event receiver:

```http
POST /v1/provider-events
Authorization: Bearer <webhook_shared_secret>
Content-Type: application/json
```

This endpoint is disabled by default. Enable it with `webhooks.enabled: true` and `webhooks.shared_secret_ref`. It accepts MuxMail normalized provider events and can advance messages from `sent` to `delivered`, `bounced`, or `complained`.
Webhook events must include complete provider account, channel, and provider message metadata, and they are accepted only when that identity matches a recorded sent attempt for the same App and message. If a sent attempt has an empty `provider_message_id` because an accepted provider response omitted it, the authenticated webhook must still provide `provider_message_id` and match the recorded provider account and channel.
For Lite JSONL logs, MuxMail stores a redacted source summary instead of the normalized request's raw `event_payload`.

Resend native webhook receiver:

```http
POST /v1/provider-events/resend
Content-Type: application/json
svix-id: <id>
svix-timestamp: <unix-seconds>
svix-signature: <signature>
```

Enable it with `webhooks.enabled: true` and `webhooks.resend_secret_ref`. MuxMail verifies the Svix signature and maps `email.delivered`, `email.bounced`, and `email.complained` events.

Brevo native webhook receiver:

```http
POST /v1/provider-events/brevo
Authorization: Bearer <brevo_webhook_token>
Content-Type: application/json
```

Enable it with `webhooks.enabled: true` and `webhooks.brevo_token_ref`. MuxMail maps Brevo `delivered`, `hardBounce`, and `spam` events, reads MuxMail metadata back from Brevo tags, and uses Brevo `ts_event` as the UTC event time.

Bounce and complaint events must include a valid single recipient email address and automatically upsert `suppression.yaml`. The recipient email is used only for the suppression update and is not written to JSONL logs.
Duplicate provider events with the same app/message/provider/provider account/provider channel/provider message/event type/occurred-at identity do not append another event record; they can still replay idempotent suppression and monotonic status repair.
Late delivered events do not roll a bounced or complained message back to delivered; the event is still logged.

Health endpoints:

```text
GET /healthz
GET /readyz
GET /version
```

## Deployment Notes

- Published images are available from GHCR:

```sh
docker pull ghcr.io/feng005211/muxmail:latest
```

- `muxmail serve -c config.yaml` is the MVP process model.
- The serve process starts both the HTTP API and the in-memory worker.
- TLS should terminate at a reverse proxy or panel such as 1Panel/OpenResty/Nginx.
- Mount config at `/etc/muxmail/config.yaml` and data at `/var/lib/muxmail`.
- In containers, set `logging.dir` to `/var/lib/muxmail/logs` and `suppression_file` to `/var/lib/muxmail/suppression.yaml`.
- Keep real API keys and provider secrets in environment variables or secret files.
- `compose.example.yaml` uses `ghcr.io/feng005211/muxmail:latest` and mounts `config.container.example.yaml` by default as a strict-mode-ready container example.
- Release versions are read from `VERSION`; GitHub Release tags must be `v${VERSION}`.

See [docs/deployment.md](docs/deployment.md) for Docker and 1Panel notes.
