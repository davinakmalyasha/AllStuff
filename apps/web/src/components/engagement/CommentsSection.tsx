import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Heart, MessageSquare, Pencil, Trash2 } from 'lucide-react'
import { api, type CommentDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm } from '@/components/ui/Modal'
import { ReportButton } from '@/components/engagement/ReportShare'
import { toast } from '@/components/ui/Toast'
import { formatDateTime } from '@/lib/format'
import { useAuthState } from '@/stores/auth'

interface MentionUser {
  id: string
  name: string
  username: string
}

/** Threaded comments with likes, @mentions, edit/delete, reports (PRD §5.6.2). */
export function CommentsSection({ businessId }: { businessId: string }) {
  const qc = useQueryClient()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const [text, setText] = useState('')
  const [replyTo, setReplyTo] = useState<{ id: string; name: string } | null>(null)
  const [replyText, setReplyText] = useState('')
  const [editId, setEditId] = useState<string | null>(null)
  const [editText, setEditText] = useState('')
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const [mentionQuery, setMentionQuery] = useState<string | null>(null)
  const [mentionIdx, setMentionIdx] = useState(0)
  const composerRef = useRef<HTMLInputElement>(null)

  const { data } = useQuery({
    queryKey: ['comments', businessId],
    queryFn: () => api<{ comments: CommentDTO[] }>(`/businesses/${businessId}/comments?limit=100`),
  })
  const refresh = () => qc.invalidateQueries({ queryKey: ['comments', businessId] })

  const { data: mentionResults } = useQuery({
    queryKey: ['user-search', mentionQuery],
    queryFn: () => api<{ users: MentionUser[] }>(`/users/search?q=${encodeURIComponent(mentionQuery ?? '')}`).catch(() => ({ users: [] })),
    enabled: mentionQuery !== null && mentionQuery.length > 0,
  })

  const createMut = useMutation({
    mutationFn: (body: { text: string; parent_id?: string }) =>
      api(`/businesses/${businessId}/comments`, { method: 'POST', body }),
    onSuccess: () => {
      setText('')
      setReplyTo(null)
      setReplyText('')
      refresh()
      toast.success('Comment posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the comment.'),
  })

  const updateMut = useMutation({
    mutationFn: ({ id, text }: { id: string; text: string }) => api(`/comments/${id}`, { method: 'PATCH', body: { text } }),
    onSuccess: () => {
      setEditId(null)
      refresh()
      toast.success('Comment updated')
    },
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => api(`/comments/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      setDeleteId(null)
      refresh()
      toast.success('Comment deleted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not delete the comment.'),
  })

  const likeMut = useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) =>
      api(`/comments/${id}/like`, { method: on ? 'PUT' : 'DELETE' }),
    onError: () => toast.error('Could not update the like.'),
  })

  // @-mention autocomplete (Batch 2): trigger on "@prefix".
  const activeText = replyTo ? replyText : text
  const setActiveText = (v: string) => (replyTo ? setReplyText(v) : setText(v))

  const handleComposeChange = (v: string) => {
    setActiveText(v)
    const at = v.lastIndexOf('@')
    if (at >= 0 && at === v.length - 1 - (v.slice(at).includes(' ') ? v.slice(at).length - 1 : 0)) {
      // "@" typed at end (or before a trailing space).
      const rest = v.slice(at + 1)
      if (!rest.includes(' ')) {
        setMentionQuery(rest)
        setMentionIdx(0)
        return
      }
    }
    if (!v.endsWith('@') && !/@[a-z0-9_]+ $/.test(v) && v.includes('@')) {
      const lastAt = v.lastIndexOf('@')
      const after = v.slice(lastAt + 1)
      if (!after.includes(' ')) {
        setMentionQuery(after)
        setMentionIdx(0)
        return
      }
    }
    setMentionQuery(null)
  }

  const insertMention = (u: MentionUser) => {
    const current = activeText
    const lastAt = current.lastIndexOf('@')
    const prefix = lastAt >= 0 ? current.slice(0, lastAt) : ''
    const next = `${prefix}@${u.username} ${current.slice(lastAt).replace(/^@[a-z0-9_]*/, '')}`
    setActiveText(next)
    setMentionQuery(null)
  }

  useEffect(() => {
    if (mentionQuery !== null && mentionResults?.users?.length) {
      setMentionIdx((i) => Math.min(i, mentionResults.users.length - 1))
    }
  }, [mentionResults, mentionQuery])

  const renderComment = (c: CommentDTO, depth: number) => (
    <div key={c.id} className={depth > 0 ? 'ml-6 mt-2' : 'mt-2'}>
      <div className="rounded-lg border border-border p-3">
        <div className="flex items-center gap-2">
          <Link to={`/u/${c.author_username}`} className="text-xs font-medium text-ink hover:underline">{c.author_name}</Link>
          <span className="text-[10px] text-ink3">@{c.author_username}</span>
          <span className="ml-auto text-[10px] text-ink3">{formatDateTime(c.created_at)}</span>
        </div>
        {editId === c.id ? (
          <div className="mt-1.5 flex gap-2">
            <input value={editText} onChange={(e) => setEditText(e.target.value)} className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" autoFocus />
            <Button size="sm" onClick={() => void updateMut.mutateAsync({ id: c.id, text: editText })} disabled={!editText.trim()}>Save</Button>
            <Button variant="secondary" size="sm" onClick={() => setEditId(null)}>✕</Button>
          </div>
        ) : (
          <p className="mt-1.5 text-sm text-ink2">{renderMentions(c.text)}</p>
        )}
        <div className="mt-1.5 flex items-center gap-3 text-xs text-ink3">
          <button onClick={() => void likeMut.mutateAsync({ id: c.id, on: true })} className="flex items-center gap-1 hover:text-ink">
            <Heart className="h-3 w-3" /> {c.like_count}
          </button>
          {user && depth < 3 && (
            <button onClick={() => { setReplyTo({ id: c.id, name: c.author_username }); setReplyText(`@${c.author_username} `) }} className="hover:text-ink">Reply</button>
          )}
          {user && c.user_id === user.id && (
            <>
              <button onClick={() => { setEditId(c.id); setEditText(c.text) }} className="hover:text-ink"><Pencil className="inline h-3 w-3" /> edit</button>
              <button onClick={() => setDeleteId(c.id)} className="hover:text-ink"><Trash2 className="inline h-3 w-3" /> delete</button>
            </>
          )}
          <ReportButton targetType="comment" targetId={c.id} compact />
        </div>
        {c.children?.map((child) => renderComment(child, depth + 1))}
      </div>
    </div>
  )

  return (
    <section>
      <h2 className="mono-label mb-3">Comments</h2>
      {user && (
        <div className="relative mb-4">
          <div className="flex gap-2">
            <input
              ref={composerRef}
              value={activeText}
              onChange={(e) => handleComposeChange(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && mentionQuery === null) {
                  void createMut.mutateAsync({ text: activeText, parent_id: replyTo?.id })
                }
                if (e.key === 'ArrowDown' && mentionQuery !== null) {
                  e.preventDefault()
                  setMentionIdx((i) => Math.min((mentionResults?.users?.length ?? 1) - 1, i + 1))
                }
                if (e.key === 'ArrowUp' && mentionQuery !== null) {
                  e.preventDefault()
                  setMentionIdx((i) => Math.max(0, i - 1))
                }
                if (e.key === 'Escape' && mentionQuery !== null) {
                  setMentionQuery(null)
                }
                if (e.key === 'Enter' && mentionQuery !== null) {
                  e.preventDefault()
                  const u = mentionResults?.users?.[mentionIdx]
                  if (u) insertMention(u)
                }
              }}
              role="combobox"
              aria-expanded={mentionQuery !== null && (mentionResults?.users?.length ?? 0) > 0}
              aria-controls="mention-list"
              aria-activedescendant={mentionQuery !== null ? `mention-opt-${mentionIdx}` : undefined}
              placeholder={replyTo ? `Replying to @${replyTo.name}…` : 'Join the conversation… (@username to mention)'}
              className="h-10 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
            />
            <Button onClick={() => void createMut.mutateAsync({ text: activeText, parent_id: replyTo?.id })} disabled={activeText.trim().length === 0 || createMut.isPending}>
              <MessageSquare className="h-4 w-4" /> Post
            </Button>
          </div>
          {mentionQuery !== null && (mentionResults?.users?.length ?? 0) > 0 && (
            <div id="mention-list" role="listbox" aria-label="Mention suggestions" className="absolute top-11 z-30 w-full overflow-hidden rounded-xl border border-border bg-surface shadow-cardHover">
              {mentionResults!.users.map((u, i) => (
                <button
                  key={u.id}
                  id={`mention-opt-${i}`}
                  role="option"
                  aria-selected={i === mentionIdx}
                  onClick={() => insertMention(u)}
                  onMouseEnter={() => setMentionIdx(i)}
                  className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm ${i === mentionIdx ? 'bg-surface2' : 'hover:bg-surface2'}`}
                >
                  <span className="font-medium text-ink">{u.name}</span>
                  <span className="text-xs text-ink3">@{u.username}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
      {!user && (
        <p className="mb-4 text-sm text-ink3">
          <Link to={`/login?next=${window.location.pathname}`} className="underline underline-offset-4 hover:text-ink">Sign in</Link> to join the conversation.
        </p>
      )}
      {!data?.comments.length ? (
        <Card className="py-8 text-center text-sm text-ink3">No comments yet.</Card>
      ) : (
        data.comments.map((c) => renderComment(c, 0))
      )}

      <Confirm
        open={!!deleteId}
        onClose={() => setDeleteId(null)}
        onConfirm={() => deleteId && void deleteMut.mutateAsync(deleteId)}
        title="Delete comment"
        message="This removes the comment and its replies."
        confirmLabel="Delete"
        danger
      />
    </section>
  )
}

function renderMentions(text: string) {
  const parts = text.split(/(@[a-z0-9_]{3,30})/g)
  return parts.map((p, i) =>
    /^@[a-z0-9_]{3,30}$/.test(p) ? (
      <Link key={i} to={`/u/${p.slice(1)}`} className="font-medium text-ink hover:underline">{p}</Link>
    ) : (
      <span key={i}>{p}</span>
    ),
  )
}
