# Security Policy

## Reporting a vulnerability

Please do **not** open a public issue for a security vulnerability.

Report it through **GitHub's private vulnerability reporting** on this
repository: *Security* → *Report a vulnerability*. That opens a private thread
visible only to the maintainer, which is the right default and needs no email
address to be configured.

Include:

- what you found, and what an attacker gains
- the request or configuration that triggers it
- the version or commit you tested
- a proof of concept if you have one

If private reporting is not enabled on this repository, email the maintainer
address listed in the repository's profile or `README`. **There is deliberately
no email address hardcoded here**: a placeholder like `security@example.invalid`
is worse than no address at all, because it looks like a working contact route
to a reader assessing whether this project takes security seriously, and it
silently discards reports to a domain that does not exist.

You will get an acknowledgement within 72 hours and a fix or a mitigation plan
within 14 days. If the report is out of scope or cannot be reproduced, you will
be told why — that is a real answer, not a brush-off.

## What this project handles

Named explicitly, because the threat model follows from the data:

| Surface | Why it matters |
|---|---|
| **Business verification documents** (registration, licence, tax ID, identity) | Encrypted at rest with AES-256-GCM; never served publicly; every admin view writes a `verification_document_views` row. This is the most sensitive thing the system stores. |
| **Session and TOTP secrets** | Argon2id password hashes; AES-GCM-encrypted TOTP secrets; Argon2id-hashed recovery codes; SHA-256-only refresh token storage. |
| **Chat messages and attachments** | Attachments are served only to authenticated thread participants. Chat media is never cacheable. |
| **Payment data** | Stripe only. No card numbers transit this system. |
| **User PII** | Email, name, avatar, timezone, IP addresses in `auth_events`. GDPR export and 14-day deletion are implemented. |
| **Uploaded media** | Magic-byte sniffing, per-kind size caps, ClamAV scanning that **fails closed**. |

## Known limitations

Stated here rather than discovered later:

- **Media is stored on local disk** (`MEDIA_DIR`). A single API replica with a
  persistent volume is the supported production topology. There is no S3/R2
  adapter, so horizontal scaling of the API requires an external volume.
- **The database connects as its own owner.** There is no least-privilege role
  and no row-level security; every tenant boundary is enforced in Go. A single
  missed `WHERE` clause in a repository method is a cross-tenant read, not a
  500. Migrations for both are reserved (see `REGISTRY.md`) and are the highest
  priority outstanding security work.
- **Rate limiting fails open.** If Redis is unreachable, per-IP limits degrade
  to per-instance limits rather than returning 503. This is deliberate —
  failing closed converts a cache blip into a site-wide outage — but it does
  mean a Redis outage weakens the auth and 2FA buckets. The rationale is in
  `docs/ARCHITECTURE.md`.
- **TOTP replay protection also fails open** on a Redis error, for the same
  reason.
- **No 2FA for regular users is enforced.** TOTP is available to anyone; it is
  only *mandatory* for the `admin` role.
- **Facebook, Apple and GitHub OAuth are not implemented.** Google only, despite
  what `docs/PRD.md` §5.9.1 says. See "Documentation drift" below.

## Documentation drift

The docs in this repository are written to be checkable, and this section exists
because some of them are wrong and staying wrong would be worse than not
writing them.

- `docs/API.md` is generated against the real route table and is the endpoint
  reference. Treat it as authoritative.
- `docs/ARCHITECTURE.md` describes design intent and the notable traps. It is
  not an endpoint list.
- `docs/PRD.md` is a **frozen 2026-08-14 product contract**. The shipped code
  has moved past it in roughly seventeen places (monetisation, business claims,
  the public API, Q&A, follows, announcements, saved searches, city pages,
  leaderboards and more). Do not use it as an acceptance criterion.

If you find a doc that disagrees with the code, that is a bug worth reporting
through the same channel.
