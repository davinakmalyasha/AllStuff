import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'

// Keep every /auth/register and /auth/login submit within the production
// auth-tier limit of 5 requests per IP per minute.
//
// All browser traffic reaches the API through one Vite proxy IP, and each
// spec needs its own accounts, so an uncoordinated suite can send about ten
// auth requests in a short burst. This records intended send times in a shared
// file and keeps at most four in any rolling 65-second window. It does not
// change, weaken, bypass, or observe any production limiter.
const WINDOW_MS = 60_000
const SAFETY_MS = 5_000
const MAX_AUTH_REQUESTS = 4
const stateFile =
  process.env.E2E_AUTH_REQUEST_LOG ??
  path.join(process.cwd(), 'node_modules', '.cache', 'e2e-auth-requests.json')

async function readRequestTimes(): Promise<number[]> {
  try {
    const raw: unknown = JSON.parse(await readFile(stateFile, 'utf8'))
    if (!Array.isArray(raw)) return []
    return raw.filter(
      (value): value is number => typeof value === 'number' && Number.isFinite(value),
    )
  } catch {
    return []
  }
}

async function writeRequestTimes(times: number[]): Promise<void> {
  await mkdir(path.dirname(stateFile), { recursive: true })
  await writeFile(stateFile, JSON.stringify(times.slice(-MAX_AUTH_REQUESTS)), 'utf8')
}

export async function reserveAuthRequestSlot(): Promise<void> {
  for (;;) {
    const now = Date.now()
    const recent = (await readRequestTimes())
      .filter((time) => now - time < WINDOW_MS)
      .sort((a, b) => a - b)
    if (recent.length < MAX_AUTH_REQUESTS) {
      await writeRequestTimes([...recent, now])
      return
    }
    const delay = Math.max(0, recent[0] + WINDOW_MS + SAFETY_MS - Date.now())
    await new Promise<void>((resolve) => {
      setTimeout(resolve, delay)
    })
  }
}
