# AGENTS.md

This file defines project-specific instructions for agents working on MuxMail.

MuxMail is an open-source, self-hosted mail routing gateway. Its default experience must remain lightweight: one Docker container should be able to run with file-based configuration, in-memory queue/rate limiting, and JSONL logs. PostgreSQL, Redis, statistics, webhooks, and the admin UI are optional enhancements.

## 0. Codex Scope & Loading

- This file is the repository-root instruction file for Codex.
- Keep this file focused on durable project rules. Put detailed product design in `docs/mail-center-design.md`.
- Before making architecture, data model, routing, provider, or deployment changes, read `docs/mail-center-design.md`.
- If a nested `AGENTS.md` or `AGENTS.override.md` is added later, the nested file may override root rules only for its subtree.
- Higher-priority runtime, system, and safety instructions always take precedence over this file.
- Within project-level guidance, follow this order when instructions conflict: direct user request, nested project instructions, this root `AGENTS.md`, then design documents.
- Do not duplicate large design sections here. Link to the relevant document instead so Codex stays under its instruction loading budget.

## 1. Code Commenting Specifications

To ensure code readability and maintainability, follow these commenting standards strictly.

- **Language Requirement**: All code comments, including package comments, type comments, method comments, function comments, inline comments, and API documentation comments, must be written in English.
- **Public API Coverage**: Every exported type, exported function, public method, handler, provider adapter, storage interface, and API endpoint must include a clear comment explaining its purpose.
- **Private Helper Coverage**: Private helpers need comments only when their purpose, invariants, side effects, or failure behavior are not obvious from the code.
- **Non-Obvious Logic**: Add concise inline comments for non-obvious business rules, critical state transitions, retry behavior, idempotency handling, quota handling, and provider failover.
- **No Noise Comments**: Do not add comments that merely restate the code. Prefer comments that explain intent, invariants, edge cases, and failure behavior.
- **Chinese Explanations**: Deep Chinese explanations for complex business logic, algorithms, architectural bottlenecks, or critical state mutations belong in `docs/` design documents or ADRs, not inside source code comments.

## 2. Decision Determinism & Conflict Resolution

When executing tasks, designing architectures, or proposing technical solutions, ambiguity, vagueness, or fence-sitting expressions are prohibited.

Avoid vague phrases such as:

- "You could use A or B"
- "It depends"
- "Around 100ms to 500ms"
- "Either is fine"

### 2.1 The Determinism Principle

- Deliver one concrete solution by default.
- Evaluate MuxMail's open-source positioning, lightweight deployment goal, maintainability, performance, and operational constraints before choosing.
- Commit to the single most suitable technical path or exact numeric default.
- Do not pass ordinary engineering decisions back to the user.
- Do not invent numeric targets, limits, prices, protocols, or provider behavior. Verify unstable facts from official or primary sources.

### 2.2 The Interruption & Inquiry Mechanism

If a definitive decision is impossible due to insufficient context or conflicting business requirements:

1. Halt automatic execution.
2. Present a clear comparison of the viable options, including pros, cons, and operational trade-offs.
3. Ask one direct, targeted question that lets the user make the strategic choice.

Use this mechanism only when the decision meaningfully changes product behavior, data compatibility, security posture, or deployment architecture.

## 3. Docker Debugging Policy

- Do not run local Docker or Docker Compose commands for debugging, tests, or development unless the user explicitly asks for it.
- Do not start local containers as a substitute for unit tests or direct binary execution.
- It is allowed to edit Dockerfiles, Compose files, deployment documentation, and container-related configuration.
- Prefer local unit tests, package tests, static checks, and direct process execution when verification is needed.
- Do not present Docker commands as the primary verification path in final responses unless the user explicitly asked for Docker verification.

## 4. Product Architecture Rules

- Keep App as the top-level business isolation unit. Do not introduce Tenant unless the user explicitly requests a SaaS or multi-organization model.
- Preserve Lite mode as a first-class path: file config, memory queue, memory rate limiter, JSONL logs, and single-container deployment.
- Treat PostgreSQL, Redis, statistics, webhooks, and the admin UI as optional enhancements.
- Avoid hard dependencies on PostgreSQL or Redis in core send flow code.
- Implement infrastructure behind interfaces such as `ConfigStore`, `Queue`, `RateLimiter`, `MessageLog`, `StatsSink`, and `Provider`.
- Provider-specific behavior must stay inside provider adapters. Core routing logic must not depend on Brevo, Resend, Mailgun, AWS SES, Tencent SES, or Aliyun DirectMail details directly.

## 5. Domain & Provider Rules

- Prefer one Sender Domain per Provider Account.
- Prefer provider isolation by subdomain, such as `auth.example.com` for the main auth channel and `auth-bak.example.com` for the backup auth channel.
- Do not design quota bypass mechanisms based on rotating multiple free accounts from the same provider.
- Route by App, Scene, recipient domain, provider health, quota state, and failover priority.
- Verification and password reset emails must be treated as critical transactional mail and kept separate from marketing mail.

## 6. Configuration & Secret Handling

- Never commit real provider API keys, SMTP passwords, webhook secrets, or signing secrets.
- Store example secrets as obvious placeholders, such as `change-me` or `muxmail_example_key`.
- File-based configuration must be human-readable and suitable for self-hosted users.
- API keys must be stored hashed when persistence exists. Never store API keys in plaintext outside initial one-time display or local example files.
- Logs must not contain full API keys, provider secrets, verification codes, reset tokens, or full sensitive payloads.
- Mask sensitive values in logs and errors.

## 7. API & Security Rules

- App identity must be derived from the API key, not from a client-supplied `app_id`.
- Support idempotency for send requests before implementing high-volume retry behavior.
- Record both caller IP and user-provided end-user IP when available.
- Treat user-provided context as untrusted input.
- Validate recipient email, scene code, template variables, and provider route configuration before enqueueing.
- Return stable, structured error codes from APIs.

## 8. Reliability Rules

- The send path must be asynchronous by default: accept request, validate, enqueue, return `message_id`.
- Provider retries must distinguish temporary failures from permanent failures.
- Hard bounces and complaints must be suppressible per App.
- Failover should choose the next eligible provider account, not blindly rotate providers.
- Keep message attempts auditable in both file-log mode and database mode.
- Default timeouts must be explicit. Do not rely on library defaults for provider HTTP calls.

## 9. Testing Rules

- Add focused tests for routing decisions, provider failover, idempotency, rate limiting, and configuration parsing.
- Use fake provider adapters in tests. Do not call real email providers in automated tests.
- Do not require PostgreSQL or Redis for core unit tests.
- Database and Redis tests, when added, must be clearly separated from default test runs.
- Do not use Docker to run tests unless explicitly requested by the user.
- Do not invent test commands. Inspect repository files such as `go.mod`, `package.json`, `Makefile`, or task files before choosing commands.
- Once the Go backend exists, the default backend verification command should be `go test ./...` unless the repository documents a different command.
- Once the admin frontend exists, the default frontend verification command should be the package script declared in `web/admin/package.json`.

## 10. Documentation Rules

- Keep `docs/mail-center-design.md` synchronized with architecture changes.
- When changing configuration shape, update examples and document migration notes.
- For user-facing configuration, prefer complete examples over fragmented snippets.
- Document every provider adapter with required DNS records, credential fields, supported features, and known limitations.
- Use Mermaid diagrams for architecture or workflow updates when diagrams clarify the design.
- For OpenAI or Codex behavior, consult official OpenAI documentation before changing instructions or making claims.

## 11. Code Style & Maintainability

- Prefer small, explicit interfaces over broad abstractions.
- Keep core routing, provider adapters, storage, queueing, and rate limiting in separate packages.
- Avoid global mutable state except for immutable configuration loaded at startup.
- Prefer deterministic tests over time-sensitive sleeps.
- Use structured logs.
- Return wrapped errors with useful context while avoiding secret leakage.
- Do not introduce a large framework unless it materially reduces complexity.

## 12. Frontend Rules

- The admin UI is an optional enhancement, not required for Lite mode.
- Prefer React, Vite, TypeScript, Ant Design, TanStack Query, and Recharts.
- The admin UI should be operational and dense, focused on Apps, Scenes, Providers, Routes, Logs, Events, and Stats.
- Avoid marketing-style landing pages inside the admin UI.
- Forms must validate configuration before saving.

## 13. Repository Hygiene

- Keep generated artifacts, local logs, local config overrides, and build outputs out of git unless intentionally tracked as examples.
- Provide example files with safe names such as `config.example.yaml`.
- Do not rename major concepts casually. Current canonical terms are App, Scene, Template, Sender Domain, Provider Account, Route Policy, Rate Limit Policy, Message, Attempt, Event, and Suppression.
- Avoid unrelated refactors while implementing a specific change.
