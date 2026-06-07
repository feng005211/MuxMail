# Security Policy

## Supported Versions

The current supported snapshot is the `0.1.0` MVP.

## Reporting Vulnerabilities

Do not open public issues that include provider API keys, SMTP passwords, webhook secrets, API keys, verification codes, reset tokens, full recipient lists, or raw provider payloads.

Until a private security contact is published, report suspected vulnerabilities through the repository owner's private contact channel. Include:

- A short description of the issue.
- A minimal reproduction that does not call real email providers.
- The affected endpoint, command, configuration field, or provider adapter.
- The expected impact and any known workaround.

## Sensitive Data Rules

- Do not commit real provider API keys, SMTP passwords, webhook secrets, signing secrets, or App API keys.
- Use `env:` or `file:` secret references for real deployments.
- Keep `plain:` secret references limited to local examples and tests.
- Run strict config validation before production deployment:

```sh
muxmail config validate -c /etc/muxmail/config.yaml --strict
```

## Verification

Run the repository verification command before submitting security-sensitive changes:

```sh
make verify
```

Do not use Docker or real provider accounts for automated security reproduction unless the maintainer explicitly asks for that path.
