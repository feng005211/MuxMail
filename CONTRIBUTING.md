# Contributing

Thanks for helping improve MuxMail.

## Development Scope

MuxMail's MVP keeps Lite mode first-class:

- File-based configuration.
- In-memory queue, rate limiting, and idempotency.
- JSONL message, attempt, event, and optional stats logs.
- PostgreSQL, Redis, statistics storage, webhooks, and admin UI remain optional enhancements.

Do not introduce required PostgreSQL or Redis dependencies into the core send path.

## Verification

Run the full repository check before submitting changes:

```sh
make verify
```

This runs tests, `go vet`, example config validation, container config strict validation, dry-run, and build.

## Releases

- `VERSION` is the single source of truth for release identity.
- `VERSION` must use `MAJOR.MINOR.PATCH`, for example `0.1.0`.
- GitHub Release tags must be `v${VERSION}`, for example `v0.1.0`.
- Before `1.0.0`, bump `PATCH` only for fixes, CI, documentation, or test-only changes that do not change public API, configuration shape, JSONL schema, runtime behavior, or deployment behavior.
- Before `1.0.0`, bump `MINOR` for every release-facing API, configuration, JSONL schema, runtime, provider, or deployment behavior change.
- Starting with `1.0.0`, use strict SemVer: `MAJOR` for breaking changes, `MINOR` for backward-compatible features, and `PATCH` for backward-compatible fixes.
- Update `CHANGELOG.md`, `docs/openapi.yaml`, README files, and deployment docs when a release-facing behavior changes.
- The publish workflow rejects a tag when it does not match `VERSION`.

## Tests

- Use fake or mock provider adapters in automated tests.
- Do not call real email providers in tests.
- Do not require Docker for default tests.
- Keep PostgreSQL and Redis tests separate from default verification if those runtimes are added later.

## Configuration And Secrets

- Never commit real provider API keys, SMTP passwords, webhook secrets, signing secrets, or App API keys.
- Use obvious placeholders in examples.
- Use `env:` or `file:` secret references for real deployment config.
- Keep `plain:` secret references limited to local examples and tests.

## Documentation

- Update `docs/mail-center-design.md` for architecture, data model, routing, provider, or deployment changes.
- Update `docs/openapi.yaml` when API behavior changes.
- Update `CHANGELOG.md` for release-facing changes.
