import { useEffect, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Pencil, X } from 'lucide-react'
import { api, type UserDTO } from '@/lib/api'
import { TIMEZONES } from '@/lib/timezones'
import { useAuthState } from '@/stores/auth'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { toast } from '@/components/ui/Toast'
import { PROFILE_LINK_FIELDS, profileLinkEntries } from '@/lib/url'

/**
 * Profile editor.
 *
 * WHY THIS EXISTS
 * ---------------
 * `PATCH /me` has fully supported name, bio, timezone, profile_links and
 * username-since for the whole life of the product (service/users.go
 * UpdateProfile), and the /me page rendered all of them as read-only `<dd>`.
 * The only writable field was the avatar, via a separate upload path.
 *
 * The consequence was not cosmetic. `users.timezone` is `NOT NULL DEFAULT 'UTC'`
 * and nothing could ever change it, so `quietHoursNow` evaluated quiet hours in
 * UTC for every user on the platform — defeating the per-recipient-timezone
 * design in the notification path entirely. A user could not fix their display
 * name either.
 *
 * The server is the sole validator (name charset, bio length, link scheme and
 * key allowlist). This component deliberately does NOT duplicate those rules:
 * a client-side copy would drift and would give a false impression that a value
 * is acceptable. Field errors come back from the API and are rendered as-is.
 */

// Keys must match profileLinkKeys in services/api/internal/service/users.go.
// The server rejects unknown keys, so sending an unrecognised one is a hard
// 400 — this is an allowlist for the form, not a free-text map.
const LINK_FIELDS = PROFILE_LINK_FIELDS



interface Draft {
  name: string
  bio: string
  timezone: string
  links: Record<string, string>
}

function draftFrom(user: UserDTO): Draft {
  const existing = (user.profile_links ?? {}) as Record<string, unknown>
  const links: Record<string, string> = {}
  for (const f of LINK_FIELDS) {
    const v = existing[f.key]
    if (typeof v === 'string') links[f.key] = v
  }
  return {
    name: user.name,
    bio: user.bio ?? '',
    timezone: user.timezone || 'UTC',
    links,
  }
}

export function ProfileEditor({ user }: { user: UserDTO }) {
  const fetchMe = useAuthState((s) => s.fetchMe)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Draft>(() => draftFrom(user))
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})

  // Re-seed when the signed-in user changes underneath us (e.g. after
  // fetchMe), but never while the form is open — that would discard edits on any
  // background refetch.
  useEffect(() => {
    if (!editing) setDraft(draftFrom(user))
  }, [user, editing])

  const save = useMutation({
    mutationFn: () => {
      // Only send fields the user actually touched. Sending the whole object
      // would rewrite bio to "" and clear every link the moment a field is
      // emptied, which is surprising when the intent was to change one thing.
      const body: Record<string, unknown> = {}
      const before = draftFrom(user)
      if (draft.name.trim() !== before.name) body.name = draft.name.trim()
      if (draft.bio.trim() !== before.bio.trim()) body.bio = draft.bio.trim()
      if (draft.timezone !== before.timezone) body.timezone = draft.timezone
      if (JSON.stringify(draft.links) !== JSON.stringify(before.links)) {
        // An empty value means "remove this link"; the server's validator
        // accepts an empty string, and storing "" drops the key on read.
        const out: Record<string, string> = {}
        for (const [k, v] of Object.entries(draft.links)) if (v.trim()) out[k] = v.trim()
        body.profile_links = out
      }
      return api<UserDTO>('/me', { method: 'PATCH', body })
    },
    onSuccess: async () => {
      setFieldErrors({})
      setEditing(false)
      // fetchMe re-reads the server, so the header, the profile page and every
      // other consumer of the auth store agree immediately. Optimistically
      // patching the store instead would leave the rest of the session on the
      // old value.
      await fetchMe()
      toast.success('Profile updated')
    },
    onError: (e: unknown) => {
      const err = e as { fields?: Record<string, string>; message?: string }
      if (err.fields) {
        setFieldErrors(err.fields)
        toast.error('Please fix the highlighted fields.')
        return
      }
      toast.error(err.message || 'Could not save your profile.')
    },
  })

  function cancel() {
    setDraft(draftFrom(user))
    setFieldErrors({})
    setEditing(false)
  }

  if (!editing) {
    const links = (user.profile_links ?? {}) as Record<string, unknown>
    // Resolve through the scheme allowlist at render time, not just at write
    // time. The API validates on save, but these rows can predate that check
    // (or arrive via an import/admin path), and a stored `javascript:` URL
    // rendered straight into href is a stored-XSS sink. Anything rejected is
    // omitted rather than rendered broken.
    const shown = profileLinkEntries(links)
    return (
      <Card className="mb-4">
        <div className="mb-3 flex items-center justify-between">
          <p className="mono-label">Profile</p>
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="h-3.5 w-3.5" /> Edit
          </Button>
        </div>
        <dl className="grid gap-4 sm:grid-cols-2">
          <div>
            <dt className="mono-label">Timezone</dt>
            <dd className="mt-1 text-sm text-ink">{user.timezone}</dd>
          </div>
          <div>
            <dt className="mono-label">Bio</dt>
            <dd className="mt-1 text-sm text-ink2">{user.bio ?? '—'}</dd>
          </div>
        </dl>
        {shown.length > 0 && (
          <ul className="mt-4 flex flex-wrap gap-2">
            {shown.map((f) => (
              <li key={f.key}>
                <a
                  href={f.href}
                  target="_blank"
                  rel="noopener noreferrer nofollow"
                  className="rounded-full border border-border px-2.5 py-0.5 text-xs text-ink2 hover:text-ink"
                >
                  {f.label}
                </a>
              </li>
            ))}
          </ul>
        )}
      </Card>
    )
  }

  return (
    <Card className="mb-4">
      <div className="mb-4 flex items-center justify-between">
        <p className="mono-label">Edit profile</p>
        <Button variant="ghost" size="sm" onClick={cancel} aria-label="Cancel editing">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          setFieldErrors({})
          save.mutate()
        }}
      >
        <Input
          label="Display name"
          value={draft.name}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          error={fieldErrors.name}
          maxLength={60}
          required
        />

        <div>
          <label htmlFor="profile-bio" className="mb-1.5 block text-sm font-medium text-ink">
            Bio
          </label>
          <textarea
            id="profile-bio"
            value={draft.bio}
            onChange={(e) => setDraft({ ...draft, bio: e.target.value })}
            rows={3}
            maxLength={300}
            aria-invalid={fieldErrors.bio ? true : undefined}
            aria-describedby={fieldErrors.bio ? 'profile-bio-err' : 'profile-bio-hint'}
            className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:outline-none focus:ring-2 focus:ring-ink3"
          />
          {fieldErrors.bio ? (
            <p id="profile-bio-err" role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {fieldErrors.bio}
            </p>
          ) : (
            <p id="profile-bio-hint" className="mt-1 text-xs text-ink3">
              {300 - draft.bio.length} characters remaining
            </p>
          )}
        </div>

        <div>
          <label htmlFor="profile-tz" className="mb-1.5 block text-sm font-medium text-ink">
            Timezone
          </label>
          <select
            id="profile-tz"
            value={draft.timezone}
            onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}
            aria-invalid={fieldErrors.timezone ? true : undefined}
            className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus:outline-none focus:ring-2 focus:ring-ink3"
          >
            {/* The current value is included even when it is not in the curated
                list, so a user who has a zone we do not enumerate can still keep
                it instead of being silently reset to UTC on first save. */}
            {!TIMEZONES.includes(draft.timezone as (typeof TIMEZONES)[number]) && (
              <option value={draft.timezone}>{draft.timezone}</option>
            )}
            {TIMEZONES.map((tz) => (
              <option key={tz} value={tz}>
                {tz}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-ink3">
            Used for notification quiet hours. Defaults to UTC if never set.
          </p>
          {fieldErrors.timezone && (
            <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {fieldErrors.timezone}
            </p>
          )}
        </div>

        <fieldset>
          <legend className="mb-1.5 text-sm font-medium text-ink">Links</legend>
          <p className="mb-2 text-xs text-ink3">
            https:// or mailto: only. Leave blank to remove a link.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            {LINK_FIELDS.map((f) => (
              <Input
                key={f.key}
                label={f.label}
                placeholder={f.placeholder}
                value={draft.links[f.key] ?? ''}
                onChange={(e) =>
                  setDraft({ ...draft, links: { ...draft.links, [f.key]: e.target.value } })
                }
                inputMode="url"
                autoComplete="off"
              />
            ))}
          </div>
          {fieldErrors.profile_links && (
            <p role="alert" className="mt-2 text-xs text-red-600 dark:text-red-400">
              {fieldErrors.profile_links}
            </p>
          )}
        </fieldset>

        <div className="flex gap-2 pt-1">
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : 'Save changes'}
          </Button>
          <Button type="button" variant="secondary" onClick={cancel}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  )
}
