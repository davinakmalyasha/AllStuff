# BizVerse — Admin Runbook

Operational guide for the platform admin (PRD §5.8, §8.7).

## Daily checks

1. **Verification queue** (`/admin/verify`) — review pending businesses: info + documents (secure viewer, every view audited). Approve with a level (Verified = info checked; Fully Verified = info + documents). Rejections require a reason shown to the owner.
2. **Reports** (`/admin/moderation`) — decide open reports: dismiss / hide content / warn author / suspend (7d). Every decision is written to the immutable audit trail.
3. **Appeals** (`/admin/appeals`) — suspended/banned users may appeal; restore or reject with a decision.
4. **Anomalies** (`/admin/appeals` → anomalies tab) — engagement spikes flagged by anti-gaming; review and clear false positives.
5. **KPIs** (`/admin/analytics`) — registrations, verification funnel, engagement, open reports.

## Verification standards

- **Verified:** name matches the business; address is plausible and geocodable; contact methods are real; description is substantive (≥50 chars); hours cover ≥5 days; no banned words.
- **Fully Verified:** additionally, at least one verification document (business registration or license) must be clear, valid, unexpired, and matching the business name.
- Reject with a specific reason; owners can resubmit up to 3 times (enforced).

## Moderation policy

- **Hide:** illegal, hateful, or spammy content. Author sees "removed by moderator".
- **Warn:** first-time minor violations (posted as a notification).
- **Suspend (7d):** repeated violations or abuse in messaging.
- **Ban:** severe or persistent abuse; appeals still possible via the contact page.
- Banned words are enforced at send time on chat and validated on content (manage in `/admin/curation`).

## Content rules

- **Announcement banner** (`/admin/curation`) — short site-wide notices.
- **Featured businesses** — homepage strip; max 8; pick for quality, not just size.

## Maintenance

- **Migrations:** run automatically on deploy (`go run ./cmd/api -migrate` or CI). Backward-compatible only.
- **Backups:** `scripts/backup.ps1 -DatabaseUrl $env:DATABASE_URL` writes a gzipped dump to `./backups` (restore with `-Restore`). Schedule daily via Task Scheduler:
  ```
  schtasks /create /tn "bizverse-backup" /tr "powershell -File D:\MyProject\AllStuff\scripts\backup.ps1 -DatabaseUrl ""postgres://...""" /sc daily /st 03:00
  ```
  Keep 14 daily dumps; test a restore into a scratch DB quarterly. For prod prefer managed PITR (RDS/Cloud SQL) plus these dumps.
- **Metrics:** `GET /metrics` (Prometheus text): request counts by route/status, 5xx, rate-limited, WS connections, goroutines, last trend run. Open in dev; in prod (`APP_ENV=prod`) it requires an admin session.
- **Media:** stored on local disk (`MEDIA_DIR`) — attach a persistent volume in prod (S3/R2 adapter is roadmap). Orphan files/rows are cleaned daily by the ops job with a 24h grace; verification documents are AES-GCM encrypted at rest when `MEDIA_ENCRYPTION_KEY` (64 hex chars) is set.
- **Rate limits:** auth 5/min/IP, search 60/min, engagement 30/min/user, media 20/h, chat sends 30/min/user, 2FA 3/15min, API keys 300/min/key, global 120/min/IP. In-memory per instance — set `REDIS_URL` to enable multi-instance WS fan-out (pub/sub) and share nothing else yet.
- **Jobs:** trending + spike anomaly flagging (10 min), currency (1 h), digest Monday + search alerts + notification purge + account-deletion anonymization + snapshot prune + media cleanup (24 h). Check `last_trend_run` in `/health`.
- **Retention sub-jobs (daily):** consumed_tokens > 48h, revoked sessions > 90d, engagement_events > 90d, auth_events > 180d.
- **Job claiming:** jobs claim their cadence window in the `job_runs` table; failed runs release their slot and retry on the next tick.

## Incident response

- **Search outage:** the API returns 500s on `/search`; check Postgres and the jobs log. Trending recomputes every 10 min.
- **WS failures:** clients reconnect with backoff and resync via REST; no data loss (outbox + client_msg_id).
- **Rate-limit storms:** check for a single IP; ban abusive users via `/admin/users`.
- **Disk/object storage:** media uploads fail closed with clear errors; check free space.

## Troubleshooting

| Symptom | Check |
|---|---|
| Business page 500s | API log `request failed`; usually a scan mismatch after schema change — add a regression test |
| Push not delivered | `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY` set? Subscriptions present? Quiet hours? |
| Emails not sent | `RESEND_API_KEY` set (prod); dev logs to console |
| 2FA login loop | Challenge token valid 5 min; check clock sync |
| Migration fails on `CREATE INDEX CONCURRENTLY` | Safe to redeploy: the runner records the `schema_migrations` version only AFTER the concurrent index statements succeed, so the next run picks the migration back up. Manual repair if needed: `DROP INDEX CONCURRENTLY IF EXISTS <index>_ccnew;` then redeploy |
