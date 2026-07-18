# Security Policy

## Supported Versions

Only the latest release receives security fixes.

| Version | Supported |
| ------- | --------- |
| Latest 1.x | ✅ |
| Older releases | ❌ |

Upgrade path: pull the latest images (`ghcr.io/artyomsv/marauder-*`) or
rebuild from the latest tag. See
[docs/getting-started.md](../docs/getting-started.md).

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

Report privately via GitHub Security Advisories:
[Report a vulnerability](https://github.com/artyomsv/marauder/security/advisories/new).

Please include:

- Affected component (backend, frontend, cfsolver, deploy stack) and version
- Steps to reproduce or a proof of concept
- Impact assessment (what an attacker gains)

## What to Expect

- Acknowledgement within **7 days**
- Status updates as the issue is triaged and fixed
- Credit in the release notes once a fix ships (unless you prefer to stay
  anonymous)

## Scope Notes

Marauder is a self-hosted application. Reports about the following are
in scope:

- Authentication/authorization bypass (JWT, OIDC, session handling)
- Credential encryption weaknesses (`MARAUDER_MASTER_KEY`, AES-256-GCM blobs)
- Injection or SSRF through tracker URLs, client configs, or notifier configs
- Container image or default deployment misconfigurations

Out of scope:

- Vulnerabilities requiring an already-compromised host or database
- Issues in third-party trackers or torrent clients themselves
- Denial of service against your own self-hosted instance
