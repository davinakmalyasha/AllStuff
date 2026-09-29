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
- **Rate limits:** auth 5/min/IP, search 60/min, engagement 30/min/user, media 20/h, chat sends 30/min/user, 2FA 3/15min, API keys 300/min/key, global `RATELIMIT_GLOBAL` (120)/min/IP. The full thirteen-tier table is in `ARCHITECTURE.md` §1.
  **`REDIS_URL` shares more than WebSocket fan-out.** It backs three things: the
  rate limiter, the TOTP single-use replay guard, and cross-instance WS fan-out.
  An earlier version of this line said Redis "shares nothing else yet", which is
  the reading that leads an operator to scale replicas without setting it — and
  the 5/min auth and 3/15min 2FA buckets then become 5/min × N and
  3/15min × N, which is precisely the multi-account lockout the tiers exist to
  prevent. **Set `REDIS_URL` before running more than one replica.**
- **Jobs:** trending + spike anomaly flagging (10 min), currency (1 h), billing
  reconciliation against Stripe (6 h), digest (hourly tick, sends weekly),
  search alerts + notification purge + account-deletion anonymisation +
  snapshot prune + media cleanup (24 h). Check `last_trend_run` in `/health`.
  `billing_reconcile` is the one that can silently grant or revoke paid access
  when a webhook is lost; it is not in the original list of this runbook.
- **Retention sub-jobs (daily):** consumed_tokens > 48h, revoked sessions > 90d,
  engagement_events > 90d, auth_events > 180d. `billing_webhook_events` has **no**
  retention job.
- **Job claiming:** jobs claim their cadence window in the `job_runs` table;
  failed runs release their slot and retry on the next tick. There is no retry
  counter and no dead-letter queue, so a job that fails on the last tick of its
  period waits a full period.

## Migration failures

Migrations run automatically at boot under an advisory lock. The runner records
`schema_migrations.version` **after** the transaction commits *and* after every
`CREATE INDEX CONCURRENTLY` in the file succeeds, so a file that fails part-way
is retried on the next boot rather than being marked applied.

From `0028` forward, retrying is safe by construction: every `ADD CONSTRAINT` is
preceded by its own drop, every concurrently-built index is drop-paired, and
seeds use `ON CONFLICT`. `TestMigrateReplayIsSafe` proves this for every
migration by deleting its version marker and re-running.

### `CREATE INDEX CONCURRENTLY` fails

Expected, self-healing, and **not** an emergency. A failed concurrent build
leaves an index with `indisvalid = false`. Every build from `0028` forward is
preceded by `DROP INDEX CONCURRENTLY IF EXISTS`, so the next boot discards the
invalid relation and rebuilds from clean. `Migrate()` also refuses to start if
any index is invalid, naming it.

If you need to intervene by hand:

```sql
SELECT c.relname FROM pg_index i
  JOIN pg_class c ON c.oid = i.indexrelid
 WHERE NOT i.indisvalid;                    -- what to drop
DROP INDEX CONCURRENTLY IF EXISTS <relname>; -- then redeploy
```

### Startup fails with `invalid index(es) present`

`Migrate()` is refusing to boot because an index is invalid, rather than letting
the gap go unnoticed. Run the two statements above and redeploy. This state
should now be unreachable from a clean migration, but the guard is deliberately
loud: the alternative is an index that was never built surfacing months later as
a query that got slow for no visible reason.

### `duplicate_object` on a constraint

Should not happen from `0028` forward — that was one of the defects fixed there.
If you see it, the file predates the fix or the version marker was deleted
manually. Find the constraint, drop it, re-run.

### Migration wedged: rows present, marker absent

This is the `0002` seed hazard, and it only affects databases bootstrapped before
the fix landed. The signature is a boot loop ending in `23505` on
`categories_slug_key`.

**Do not automate this.** Writing a version marker by hand asserts "I have
verified this file's effects are present", and that should be a human's decision.
Check first:

```sql
-- 1. Is this actually the wedge signature? 0001 recorded, 0002 not.
SELECT version FROM schema_migrations ORDER BY version;

-- 2. Is the data complete? Expect 6 roots / 30 leaves / 36 total.
SELECT count(*) FILTER (WHERE parent_id IS NULL)     AS roots,
       count(*) FILTER (WHERE parent_id IS NOT NULL) AS leaves,
       count(*)                                     AS total
  FROM categories;

-- 3. Are the ids the ones the seed inserts? Expect 0 rows.
SELECT c.id FROM categories c
 WHERE c.id::text LIKE 'a1000000-%'
   AND c.id NOT IN (
     'a1000000-0000-4000-8000-000000000001', 'a1000000-0000-4000-8000-000000000002',
     'a1000000-0000-4000-8000-000000000003', 'a1000000-0000-4000-8000-000000000004',
     'a1000000-0000-4000-8000-000000000005', 'a1000000-0000-4000-8000-000000000006');
```

Only when all three are clean — 36 rows present, no unexpected ids — record the
marker so the file is skipped on the next boot:

```sql
INSERT INTO schema_migrations (version, applied_at)
VALUES ('0002_seed_categories.sql', now());
```

If step 2 or 3 shows anything unexpected, the data is partial. Restore from
backup or delete the incomplete rows and let the fixed seed re-run.


## Incident response

- **Search outage:** the API returns 500s on `/search`; check Postgres and the jobs log. Trending recomputes every 10 min.
- **WS failures:** clients reconnect with backoff and resync via REST; no data loss (outbox + client_msg_id).
- **Rate-limit storms:** check for a single IP; ban abusive users via `/admin/users`.
- **Disk/object storage:** media uploads fail closed with clear errors; check free space.

## Troubleshooting

| Symptom | Check |
|---|---|
| Business page 500s | API log `request failed`; usually a scan mismatch after schema change — add a regression test |
| Push not delivered | `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY` set? Subscriptions present? Quiet hours are evaluated in the **recipient's** timezone |
| Emails not sent | `RESEND_API_KEY` **or** `SMTP_ADDR` set; prod refuses to boot with neither, because the fallback prints raw password-reset tokens to stdout |
| 2FA login loop | Challenge token is short-lived; check clock sync, and whether `REDIS_URL` is reachable (the replay guard fails open on a Redis error, so codes are reusable during an outage) |
| Migration fails on `CREATE INDEX CONCURRENTLY` | Self-healing — see *Migration failures* above. Redeploy; the drop-paired rebuild discards the invalid index |
| Startup fails with `invalid index(es) present` | Deliberate. Drop the named index and redeploy |
| Migration wedged: rows present, marker absent | The `0002` seed hazard — see *Migration wedged* above |
| Duplicate `media.path` rows block `0029` | The `media_path_key` pre-flight is operator-gated. Audit the groups, confirm the files exist under `MEDIA_DIR`, then `SET bizverse.allow_dedupe = 'on';` and re-run |
| Every user sharing one IP hits auth 5/min | `TRUST_X_FORWARDED_FOR` is false behind a proxy, so `clientIP()` returns the proxy egress address for everyone. This is the misconfiguration the `.env.example` comment warns about |
