// Absolute OG/canonical URLs for crawlers that never run JavaScript.
//
// index.html carries `__SITE_URL__`-prefixed sentinels instead of absolute
// URLs because the domain is not known at commit time. vite.config.ts replaces
// the sentinel at build time from SITE_URL (falling back to PUBLIC_URL);
// when neither is set it becomes the empty string and the tags stay relative,
// which is exactly today's dev/E2E behavior.
//
// The runtime meta.ts already sets absolute tags from window.location for real
// browsers, so this file exists only for scrapers (Slack/X/Facebook read the
// static HTML). App behavior must never depend on the sentinel being replaced,
// which the self-test below pins: an unset SITE_URL is a supported state, not
// an error.
//
// Run: node scripts/site-url.mjs   (self-test; wired into CI)

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import assert from 'node:assert/strict'

export const SENTINEL = '__SITE_URL__'

// The only tags allowed to carry the sentinel. An absolute URL anywhere else
// would rot the same way the hardcoded counts did, and a sentinel that spreads
// beyond this list means index.html grew a new crawler-facing tag that nobody
// reviewed.
const SENTINEL_TAGS = ['og:url', 'og:image', 'canonical']

export function injectSiteUrl(html, siteUrl) {
  const site = (siteUrl ?? '').replace(/\/+$/, '')
  return html.replaceAll(SENTINEL, site)
}

function selfTest() {
  // Set → absolutized, trailing slashes trimmed.
  assert.equal(
    injectSiteUrl(
      '<meta property="og:url" content="__SITE_URL__/" />',
      'https://bizverse.example/',
    ),
    '<meta property="og:url" content="https://bizverse.example/" />',
  )
  assert.equal(
    injectSiteUrl('<link rel="canonical" href="__SITE_URL__/" />', 'https://a.example'),
    '<link rel="canonical" href="https://a.example/" />',
  )
  // Unset/empty → today's relative tags. Dev and E2E build without SITE_URL,
  // so this path is the common one, not a degenerate case.
  for (const empty of [undefined, null, '']) {
    assert.equal(
      injectSiteUrl(
        '<meta property="og:image" content="__SITE_URL__/og-default.png" />',
        empty,
      ),
      '<meta property="og:image" content="/og-default.png" />',
    )
  }

  // index.html must carry the sentinel in exactly the reviewed tags: no
  // hardcoded production domain (it would leak into dev builds), and no tag
  // outside SENTINEL_TAGS (a new crawler-facing URL nobody reviewed).
  const root = join(dirname(fileURLToPath(import.meta.url)), '..')
  const html = readFileSync(join(root, 'index.html'), 'utf8')
  const stripped = html.replace(/<!--[\s\S]*?-->/g, '')
  assert.match(stripped, /__SITE_URL__\//, 'index.html lost its site-URL sentinel')
  // Crawler-facing tags must never hardcode an absolute origin: the map tile
  // hosts live in the CSP meta, which is deliberately not a crawler tag, so a
  // hardcoded domain can only arrive here as a copy-pasted production URL.
  const crawlerLines = stripped.split('\n').filter((l) => /og:|canonical/i.test(l))
  assert.ok(crawlerLines.length > 0, 'index.html has no crawler-facing tags')
  for (const line of crawlerLines) {
    assert.doesNotMatch(
      line,
      /https?:\/\//,
      `crawler tag hardcodes an absolute origin: ${line.trim()}`,
    )
  }
  const sentinelLines = stripped.split('\n').filter((l) => l.includes(SENTINEL))
  for (const line of sentinelLines) {
    const tagged = SENTINEL_TAGS.some((t) => line.includes(t))
    assert.ok(tagged, `sentinel appears outside the reviewed tags: ${line.trim()}`)
  }
  console.log('site-url: ok - transform cases pass, index.html sentinel set is exact')
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  selfTest()
}
