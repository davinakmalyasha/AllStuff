import { rm } from 'node:fs/promises';
import path from 'node:path';

/**
 * Warm the dev server before the suite: Vite transforms each route's modules
 * on first request, and cold on-demand compilation made the first test to
 * touch a route blow through assertion timeouts (flaky full-suite runs that
 * never reproduce per-file).
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
      fetch(`http://localhost:5173${r}`).catch(() => {}),
    ),
  )
}
