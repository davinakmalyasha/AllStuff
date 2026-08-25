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
  await Promise.all(
    routes.map((r) =>
      fetch(`http://localhost:5173${r}`).catch(() => {}),
    ),
  )
}
