import { rm } from 'node:fs/promises';
import path from 'node:path';

/**
 * Warm the server before the suite: the first request to a route pays
 * compilation (dev) or ISR render (start), and that cold cost blew through
 * assertion timeouts (flaky full-suite runs that never reproduce per-file).
 */
const routes = [
  '/',
  '/discover',
  '/categories',
  '/leaderboards',
  '/compare',
  '/map',
  '/login',
  '/register',
]

export default async function () {
  // A reservation log left by an interrupted run would pointlessly delay the
  // first authentication request of the next run.
  const authRequestLog =
    process.env.E2E_AUTH_REQUEST_LOG ??
    path.join(process.cwd(), 'node_modules', '.cache', 'e2e-auth-requests.json');
  await rm(authRequestLog, { force: true });
  await Promise.all(
    routes.map((r) =>
      fetch(`http://localhost:3000${r}`).catch(() => {}),
    ),
  )
}
