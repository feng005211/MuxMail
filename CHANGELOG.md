# Changelog

## 0.1.0 - MVP Snapshot

Initial Lite-mode MVP implementation.

### Added

- File-based App, API key, Scene, Template, Provider Account, Provider Channel, route policy, and rate limit configuration.
- `POST /v1/mail/send` asynchronous send API with App API key authentication, request validation, template rendering, suppression checks, idempotency, fixed-window rate limiting, JSONL queued logging, and memory queue admission.
- In-memory worker with provider failover, retry backoff, message and attempt JSONL logging, and stats hooks.
- Mock API provider, Resend API/SMTP providers, and Brevo API/SMTP providers.
- Lite read APIs for message snapshots, failed messages, attempt timelines, provider event timelines, suppressions, provider event listing, and stats summary.
- Optional normalized provider event receiver plus native Resend and Brevo webhook receivers.
- Static YAML suppression store with bounce and complaint upsert from provider events.
- `muxmail config validate`, strict secret validation, and `muxmail send dry-run`.
- `muxmail version`, `GET /version`, and root `VERSION` file as the release version source of truth.
- OpenAPI 3.1 spec for the current Lite API.
- Dockerfile, Docker Compose example, container config example, `.dockerignore`, GitHub Actions image publishing, and deployment notes for single-container Lite mode.

### Verification

- `make verify`

### MVP Non-Goals

- Tenant model.
- PostgreSQL or Redis runtime requirement.
- Admin UI.
- Inbound SMTP server.
- Provider webhook receivers enabled by default.
- Marketing bulk sending, attachments, open tracking, or click tracking.
