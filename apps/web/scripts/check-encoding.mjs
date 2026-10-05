/**
 * Fails the build on mojibake: UTF-8 that was decoded as cp1252/windows-1252
 * and re-encoded as UTF-8.
 *
 * WHY THIS IS A GATE AND NOT A LINT
 * ---------------------------------
 * Mojibake stays valid UTF-8 and stays valid TypeScript. `tsc`, ESLint and
 * `vite build` all pass straight over it. The only thing that shows the damage
 * is a browser rendering the string. That is not a theoretical risk for this
 * repository: 3e4d259 repaired 128 occurrences across 25 files, its own message
 * lists eighteen lines it could not repair mechanically, and those eighteen have
 * since been repaired here too - including a 20-entry emoji picker that was
 * shipping as garbage glyphs, which is a user-visible functional bug and not
 * cosmetics.
 *
 * The class has now recurred twice, and `.editorconfig`'s `charset = utf-8`
 * did not prevent it, because the damage came from scripted writes that do not
 * consult an editor config. So the check that catches it has to live in CI.
 *
 * WHAT IT MATCHES
 * ---------------
 * Two signatures only, both of which are impossible in correct text:
 *
 *   1. U+FFFD REPLACEMENT CHARACTER - the character was destroyed rather than
 *      transformed, so there is nothing left to repair. `docs/ARCHITECTURE.md`
 *      still carries four of these.
 *
 *   2. A cp1252 lead character (a-circumflex, A-tilde, A-circumflex, eth,
 *      D-circumflex, a-acute, i-acute, n-tilde) immediately followed by a
 *      C1 control or a cp1252 punctuation character in the range U+2013-U+20AC.
 *      That pairing is what a mis-decoded three- or four-byte UTF-8 sequence
 *      looks like, and it does not occur in any of the correct non-ASCII text in
 *      this repository - which is em dashes, section signs, arrows, times,
 *      infinity, stars and emoji, all of which are single code points or astral
 *      characters with no cp1252 lead in front of them.
 *
 * A deliberately loose regex is the wrong tool here. An earlier version of this
 * idea flagged bare em dashes, which are legitimate and appear hundreds of
 * times, so a build that flagged them would be a build nobody reads.
 *
 * WHAT IT CANNOT CATCH
 * --------------------
 * One form of this damage leaves no codepoint signature: a character replaced by
 * a plain ASCII `?`. `docs/ARCHITECTURE.md` carries one - an arrow that arrived
 * as `engagement_events.flagged ? admin review queue` - and no signature can
 * separate it from the thousands of legitimate question marks in query strings,
 * ternaries and URLs. That case was repaired by reading the surrounding prose.
 *
 * So this gate is a backstop, not the primary defence. The primary defence is
 * not writing the file wrong, and `.editorconfig` already pins
 * `charset = utf-8` for every contributor whose editor honours it.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

// This file lives at <root>/apps/web/scripts/, so the repository root is three
// levels up. Getting this wrong silently narrows the scan to apps/ and reports
// a clean tree, which is the worst possible failure for a gate.
const ROOT = fileURLToPath(new URL('../../../', import.meta.url))

/** Directories that are never source. */
const SKIP_DIRS = new Set([
  'node_modules', 'dist', '.git', 'coverage', 'test-results',
  'playwright-report', '.playwright-mcp', '.gocache', '.pgdata', 'data',
])

const EXTS = new Set([
  '.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs',
  '.go', '.sql', '.md', '.json', '.yml', '.yaml',
  '.html', '.css', '.ps1', '.sh',
])

// U+FFFD: data loss, nothing to reverse.
const REPLACEMENT = /\uFFFD/

// A cp1252 lead char followed by a C1 control or a cp1252 punctuation char.
// U+2013-U+20AC is the en-dash-through-euro-sign block, i.e. exactly the range
// that UTF-8 continuation bytes E2 80 xx decode into when read as cp1252.
const LEAD = '\u00E2\u00C3\u00C2\u00F0\u00D0\u00E3\u00EF\u00F1'
const TRAIL = '\\u0080-\\u009F\\u00A0-\\u00BF\\u2013-\\u20AC'
const MOJIBAKE = new RegExp(`[${LEAD}][${TRAIL}]`)

/** @returns {string[]} relative paths that fail. */
function scan(dir, hits = []) {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      scan(full, hits)
      continue
    }
    if (!EXTS.has(extname(entry))) continue

    const text = readFileSync(full, 'utf8')
    const lines = text.split('\n')
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i]
      if (!REPLACEMENT.test(line) && !MOJIBAKE.test(line)) continue
      // Report the code points rather than the glyphs: the console this may run
      // under cannot encode them, and a mangled report is indistinguishable from
      // a passing one.
      const found = [...line.matchAll(/[\uFFFD\u0080-\u009F\u00C2\u00C3\u00E2\u00EF\u00F0\u00F1\u00D0\u00E3\u2013-\u20AC]/g)]
      const detail = [...new Set(found.map((m) =>
        'U+' + m[0].codePointAt(0).toString(16).toUpperCase().padStart(4, '0')
      ))].join(' ')
      hits.push(`${relative(ROOT, full).replace(/\\/g, '/')}:${i + 1}  ${detail}`)
    }
  }
  return hits
}

const hits = scan(ROOT)

if (hits.length > 0) {
  console.error(`::error::mojibake found in ${hits.length} place(s).`)
  console.error('UTF-8 was decoded as cp1252 and re-encoded. It compiles, so nothing')
  console.error('else will ever report this. See scripts/repair-encoding.py, which')
  console.error('reverses a run only when the reversed bytes are valid UTF-8 decoding')
  console.error('to exactly one code point.')
  console.error('')
  for (const hit of hits) console.error('  ' + hit)
  process.exit(1)
}

console.log('encoding: no mojibake found')