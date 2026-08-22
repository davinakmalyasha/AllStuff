import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  Bell,
  BellOff,
  FileUp,
  Image as ImageIcon,

  Pencil,
  Pin,
  Search,
  Send,
  Trash2,
  Video,
  Mic,
} from 'lucide-react'
import { api, uploadMedia, type ChatMessageDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { PageSpinner } from '@/components/ui/Spinner'
import { ws } from '@/lib/ws'
import { useAuth } from '@/stores/auth'
import { copyText } from '@/lib/format'
import { toast } from '@/components/ui/Toast'

const EMOJIS = ['👍', '❤️', '😂', '😮', '😢', '🙏', '🔥', '🎉', '✅', '❌', '🤔', '👏', '😍', '😎', '💯', '🥳', '🤝', '👌', '😅', '🙌']

export function ThreadPage({ businessMode = false }: { businessMode?: boolean }) {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { user } = useAuth()
  const [messages, setMessages] = useState<ChatMessageDTO[]>([])
  const [text, setText] = useState('')
  const [replyTo, setReplyTo] = useState<ChatMessageDTO | null>(null)
  const [typing, setTyping] = useState(false)
  const [pickerFor, setPickerFor] = useState<number | null>(null)
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [editing, setEditing] = useState<ChatMessageDTO | null>(null)
  const [searching, setSearching] = useState(false)
  const [searchQ, setSearchQ] = useState('')
  const [pendingFile, setPendingFile] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const typingTimer = useRef<ReturnType<typeof setTimeout>>()

  const { data, isLoading } = useQuery({
    queryKey: ['thread', id],
    queryFn: () => api<{ thread: unknown; messages: ChatMessageDTO[] }>(`/threads/${id}`),
    enabled: !!id,
  })

  useEffect(() => {
    if (data) setMessages(data.messages)
  }, [data])

  // Live events.
  useEffect(() => {
    ws.connect()
    ws.subscribe(id)
    const offs = [
      ws.on('message.new', (f) => {
        const m = f.payload as ChatMessageDTO
        if (m.thread_id === id) {
          setMessages((prev) => (prev.some((x) => x.id === m.id) ? prev : [...prev, m]))
          void qc.invalidateQueries({ queryKey: ['threads'] })
        }
      }),
      ws.on('message.edited', (f) => {
        const m = f.payload as ChatMessageDTO
        if (m.thread_id === id) setMessages((prev) => prev.map((x) => (x.id === m.id ? m : x)))
      }),
      ws.on('message.deleted', (f) => {
        const p = f.payload as { message_id: number; deleted_for: string }
        setMessages((prev) =>
          prev.map((x) => (x.id === p.message_id ? { ...x, deleted_for: p.deleted_for as ChatMessageDTO['deleted_for'] } : x)),
        )
      }),
      ws.on('reaction.updated', () => void qc.invalidateQueries({ queryKey: ['thread', id] })),
      ws.on('receipt.read', (f) => {
        // Another participant read up to last_read_message_id: bump read_count
        // on messages up to that point so "seen" indicators stay accurate.
        const p = f.payload as { thread_id: string; user_id: string; last_read_message_id: number }
        if (p.thread_id !== id || p.user_id === user?.id) return
        setMessages((prev) =>
          prev.map((x) =>
            x.id <= p.last_read_message_id && x.sender_id === user?.id && x.read_count < 1
              ? { ...x, read_count: x.read_count + 1 }
              : x,
          ),
        )
      }),
      ws.on('typing', (f) => {
        const p = f.payload as { thread_id: string; user_id: string; is_typing: boolean }
        if (p.thread_id === id && p.user_id !== user?.id) {
          setTyping(true)
          clearTimeout(typingTimer.current)
          typingTimer.current = setTimeout(() => setTyping(false), 2500)
        }
      }),
    ]
    return () => offs.forEach((off) => off())
  }, [id, user?.id, qc])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length, typing])

  const sendTyping = () => {
    if (text.trim()) void api(`/threads/${id}/typing`, { method: 'POST' }).catch(() => undefined)
  }

  const send = async () => {
    const body = text.trim()
    if (!body && !pendingFile) return
    setText('')
    setReplyTo(null)
    try {
      await api(`/threads/${id}/messages`, {
        method: 'POST',
        body: {
          body: body || undefined,
          type: pendingFile ? pendingKind : 'text',
          media_id: pendingFile,
          client_msg_id: crypto.randomUUID(),
          reply_to_id: replyTo?.id,
        },
      })
      setPendingFile(null)
      setRecording(false)
    } catch {
      setText(body)
    }
  }

  const uploadImage = async (file: File, kind: 'chat_image' | 'chat_file' | 'chat_video' | 'chat_audio' = 'chat_image') => {
    const r = await uploadMedia(kind, file)
    setPendingFile(r.media.id)
    setPendingKind(kind === 'chat_file' ? 'file' : kind === 'chat_video' ? 'video' : kind === 'chat_audio' ? 'audio' : 'image')
    setText('')
  }
  const [pendingKind, setPendingKind] = useState<'image' | 'file' | 'video' | 'audio'>('image')

  // Voice notes (PRD §5.5.2): MediaRecorder → webm → chat_audio upload.
  // Capped at 5 minutes; oversized recordings surface an error toast instead
  // of being silently dropped.
  const [recording, setRecording] = useState(false)
  const recorderRef = useRef<MediaRecorder | null>(null)
  const recTimerRef = useRef<number | null>(null)

  const toggleVoice = async () => {
    if (recording) {
      recorderRef.current?.stop()
      return
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const rec = new MediaRecorder(stream)
      recorderRef.current = rec
      const chunks: BlobPart[] = []
      rec.ondataavailable = (e) => chunks.push(e.data)
      rec.onstop = async () => {
        stream.getTracks().forEach((t) => t.stop())
        setRecording(false)
        if (recTimerRef.current !== null) {
          clearTimeout(recTimerRef.current)
          recTimerRef.current = null
        }
        const blob = new Blob(chunks, { type: 'audio/webm' })
        if (blob.size === 0) return
        if (blob.size > 200_000) {
          toast.error('Voice note too long — keep it under ~30 seconds.')
          return
        }
        try {
          await uploadImage(new File([blob], 'voice.webm', { type: 'audio/webm' }), 'chat_audio')
        } catch {
          toast.error('Could not upload voice note.')
        }
      }
      rec.start()
      setRecording(true)
      // Hard stop at the PRD's 5-minute voice-note ceiling.
      recTimerRef.current = window.setTimeout(() => rec.stop(), 5 * 60 * 1000)
    } catch {
      toast.error('Microphone unavailable')
    }
  }

  const edit = async () => {
    if (!editing) return
    const body = text.trim()
    if (!body) return
    await api(`/messages/${editing.id}`, { method: 'PATCH', body: { body } })
    setEditing(null)
    setText('')
  }

  const del = async (m: ChatMessageDTO, scope: 'me' | 'everyone') => {
    await api(`/messages/${m.id}?scope=${scope}`, { method: 'DELETE' })
    setMenuFor(null)
  }

  const [jump, setJump] = useState(0)
  useEffect(() => {
    if (jump) {
      const el = document.getElementById(`msg-${jump}`)
      el?.scrollIntoView({ behavior: 'smooth', block: 'center' })
      el?.classList.add('ring-2', 'ring-ink')
      setTimeout(() => el?.classList.remove('ring-2', 'ring-ink'), 2000)
      setJump(0)
    }
  }, [jump])

  const react = async (m: ChatMessageDTO, emoji: string) => {
    await api(`/messages/${m.id}/reaction`, { method: 'PUT', body: { emoji } })
    setPickerFor(null)
  }

  const [pinnedIds, setPinnedIds] = useState<number[]>([])
  const { data: pinnedData } = useQuery({
    queryKey: ['pinned', id],
    queryFn: () => api<{ pinned_message_ids: number[] }>(`/threads/${id}/pinned`),
    enabled: !!id,
  })
  useEffect(() => {
    if (pinnedData) setPinnedIds(pinnedData.pinned_message_ids)
  }, [pinnedData])

  const pin = async (messageId: number, on: boolean) => {
    await api(`/threads/${id}/pin/${messageId}`, { method: on ? 'PUT' : 'DELETE' })
    setPinnedIds((prev) => (on ? [...prev, messageId] : prev.filter((x) => x !== messageId)))
  }

  const { data: searchRes } = useQuery({
    queryKey: ['thread-search', id, searchQ],
    queryFn: () => api<{ messages: ChatMessageDTO[] }>(`/threads/${id}/search?q=${encodeURIComponent(searchQ)}`),
    enabled: searching && searchQ.trim().length >= 2,
  })

  // Thread mute (PRD §5.5.1): optimistic toggle; server keeps muted_until.
  const [muted, setMuted] = useState(false)
  const toggleMute = async () => {
    const next = !muted
    setMuted(next)
    try {
      await api(`/threads/${id}/mute`, { method: next ? 'PUT' : 'DELETE' })
      toast.success(next ? 'Notifications muted' : 'Notifications unmuted')
    } catch {
      setMuted(!next)
      toast.error('Could not update mute.')
    }
  }

  const own = (m: ChatMessageDTO) => m.sender_id === user?.id
  const display = useMemo(() => messages.filter((m) => !(m.deleted_for === 'me' && own(m))), [messages, user?.id])

  if (isLoading) return <PageSpinner />

  return (
    <div className="flex h-[calc(100vh-64px)] flex-col">
      {/* Header */}
      <div className="flex items-center gap-3 border-b border-border px-4 py-3">
        <button onClick={() => navigate(businessMode ? '/dashboard/chats' : '/me/messages')} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Back">
          <ArrowLeft className="h-4 w-4" />
        </button>
        <p className="flex-1 text-sm font-semibold text-ink">Thread</p>
        <button onClick={() => void toggleMute()} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label={muted ? 'Unmute notifications' : 'Mute notifications'} title={muted ? 'Unmute' : 'Mute'}>
          {muted ? <BellOff className="h-4 w-4" /> : <Bell className="h-4 w-4" />}
        </button>
        <button onClick={() => setSearching((v) => !v)} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Search">
          <Search className="h-4 w-4" />
        </button>
      </div>

      {searching && (
        <div className="border-b border-border p-3">
          <input
            value={searchQ}
            onChange={(e) => setSearchQ(e.target.value)}
            placeholder="Search in this thread…"
            className="h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
            autoFocus
          />
          {searchRes?.messages.map((m) => (
            <button key={m.id} onClick={() => { setSearching(false); setSearchQ('') }} className="mt-2 block w-full rounded-lg bg-surface2 px-3 py-2 text-left text-sm text-ink2">
              {m.body}
            </button>
          ))}
        </div>
      )}

      {/* Messages */}
      <div className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {display.map((m) => (
          <MessageRow
            key={m.id}
            m={m}
            own={own(m)}
            onReply={() => { setReplyTo(m); setMenuFor(null) }}
            onEdit={() => { setEditing(m); setText(m.body ?? ''); setMenuFor(null) }}
            onDelete={(scope) => void del(m, scope)}
            onPin={(on) => void pin(m.id, on)}
            pinned={pinnedIds.includes(m.id)}
            menuOpen={menuFor === m.id}
            setMenuOpen={(v) => setMenuFor(v ? m.id : null)}
            pickerOpen={pickerFor === m.id}
            setPicker={(emoji) => void react(m, emoji)}
            replyTarget={m.reply_to_id ? display.find((x) => x.id === m.reply_to_id) : undefined}
            showActions={!businessMode || m.sender_role !== 'owner'}
            onJump={setJump}
          />
        ))}
        {typing && <p className="px-1 text-xs text-ink3">typing…</p>}
        <div ref={bottomRef} />
      </div>

      {/* Reply quote */}
      {replyTo && (
        <div className="flex items-center gap-2 border-t border-border bg-surface2 px-4 py-2">
          <p className="flex-1 truncate text-xs text-ink2">Replying to: {replyTo.body ?? 'media'}</p>
          <button onClick={() => setReplyTo(null)} className="text-ink3 hover:text-ink">✕</button>
        </div>
      )}

      {/* Composer */}
      <div className="border-t border-border p-3">
        {pendingFile && <p className="mb-2 text-xs text-ink2">📎 {pendingKind} ready to send</p>}
        <div className="flex items-end gap-2">
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send image">
            <ImageIcon className="h-5 w-5" />
            <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadImage(f, 'chat_image') }} />
          </label>
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send file">
            <FileUp className="h-5 w-5" />
            <input type="file" accept=".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.csv" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadImage(f, 'chat_file') }} />
          </label>
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send video">
            <Video className="h-5 w-5" />
            <input type="file" accept="video/mp4,video/webm" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadImage(f, 'chat_video') }} />
          </label>
          <button
            onClick={() => void toggleVoice()}
            className={`rounded-lg p-2 hover:bg-surface2 ${recording ? 'animate-pulse text-red-500' : 'text-ink3 hover:text-ink'}`}
            title={recording ? 'Stop recording' : 'Voice note'}
          >
            <Mic className="h-5 w-5" />
          </button>
          {editing ? (
            <>
              <input
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && void edit()}
                className="h-10 flex-1 rounded-lg border border-ink bg-surface px-3 text-sm text-ink"
                placeholder="Editing…"
                autoFocus
              />
              <Button size="sm" onClick={() => void edit()} disabled={!text.trim()}>Save</Button>
              <Button variant="secondary" size="sm" onClick={() => { setEditing(null); setText('') }}>✕</Button>
            </>
          ) : (
            <>
              <input
                value={text}
                onChange={(e) => { setText(e.target.value); sendTyping() }}
                onKeyDown={(e) => e.key === 'Enter' && void send()}
                className="h-10 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
                placeholder={replyTo ? 'Reply…' : 'Message…'}
              />
              <Button size="sm" onClick={() => void send()} disabled={!text.trim() && !pendingFile}>
                <Send className="h-4 w-4" />
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function MessageRow({
  m,
  own,
  onReply,
  onEdit,
  onDelete,
  onPin,
  pinned,
  menuOpen,
  setMenuOpen,
  pickerOpen,
  setPicker,
  replyTarget,
  showActions,
  onJump,
}: {
  m: ChatMessageDTO
  own: boolean
  onReply: () => void
  onEdit: () => void
  onDelete: (scope: 'me' | 'everyone') => void
  onPin?: (on: boolean) => void
  pinned?: boolean
  menuOpen: boolean
  setMenuOpen: (v: boolean) => void
  pickerOpen: boolean
  setPicker: (emoji: string) => void
  replyTarget?: ChatMessageDTO
  showActions: boolean
  onJump: (id: number) => void
}) {
  if (m.deleted_for === 'everyone') {
    return (
      <div className={`flex ${own ? 'justify-end' : 'justify-start'}`}>
        <p className="rounded-xl bg-surface2 px-3 py-1.5 text-xs italic text-ink3">This message was deleted</p>
      </div>
    )
  }
  return (
    <div className={`group relative flex ${own ? 'justify-end' : 'justify-start'}`} id={`msg-${m.id}`}>
      <div className={`max-w-[75%] ${own ? 'order-1' : ''}`}>
        {replyTarget && (
          <button onClick={() => onJump(m.reply_to_id ?? 0)} className="mb-0.5 block max-w-full truncate rounded-t-lg bg-surface2 px-3 py-1 text-[10px] text-ink3 hover:text-ink" title="Jump to original">
            ↳ {replyTarget.body ?? 'media'}
          </button>
        )}
        <div className={`rounded-2xl px-3.5 py-2 text-sm ${own ? 'bg-accent text-accent-ink' : 'border border-border bg-surface text-ink'}`}>
          {m.type === 'image' && m.media_id && (
            <img src={`/api/v1/media/${m.media_id}/file`} alt="" className="mb-1.5 max-h-48 rounded-lg object-cover" />
          )}
          {m.type === 'video' && m.media_id && (
            <video src={`/api/v1/media/${m.media_id}/file`} controls className="mb-1.5 max-h-56 rounded-lg" />
          )}
          {m.type === 'file' && m.media_id && (
            <a href={`/api/v1/media/${m.media_id}/file`} target="_blank" rel="noreferrer" className="mb-1.5 flex items-center gap-2 rounded-lg bg-surface2 px-3 py-2 text-xs underline-offset-2 hover:underline">
              📎 {m.body ?? 'Download file'}
            </a>
          )}
          {m.type === 'audio' && m.media_id && (
            <audio src={`/api/v1/media/${m.media_id}/file`} controls className="mb-1.5 h-10 w-56 max-w-full" />
          )}
          {m.body && <p className="whitespace-pre-wrap break-words">{m.body}</p>}
          {m.link_preview && (
            <a href={String(m.link_preview.url ?? '#')} target="_blank" rel="noreferrer" className="mt-1.5 flex items-center gap-2 rounded-lg border border-border bg-surface2 p-2 no-underline">
              {m.link_preview.image ? <img src={String(m.link_preview.image)} alt="" className="h-10 w-10 rounded object-cover" /> : null}
              <span className="min-w-0">
                <span className="block truncate text-xs font-semibold text-ink">{String(m.link_preview.title ?? m.link_preview.url ?? '')}</span>
                {m.link_preview.description ? <span className="block truncate text-[10px] text-ink3">{String(m.link_preview.description)}</span> : null}
              </span>
            </a>
          )}
          {m.forwarded_from_message_id && <p className="mt-1 text-[10px] opacity-60">Forwarded</p>}
          {m.edited_at && <p className="mt-0.5 text-right text-[9px] opacity-50">edited</p>}
          <p className={`mt-0.5 text-right text-[9px] ${own ? 'opacity-60' : 'text-ink3'}`}>
            {new Date(m.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </p>
        </div>
        {showActions && (
          <div className={`mt-0.5 flex items-center gap-2 opacity-0 transition-opacity group-hover:opacity-100 ${own ? 'justify-end' : ''}`}>
            <button onClick={() => { void copyText(m.body ?? '').then((ok) => toast.success(ok ? 'Copied' : 'Copy failed')) }} className="text-[10px] text-ink3 hover:text-ink">copy</button>
            <button onClick={onReply} className="text-[10px] text-ink3 hover:text-ink">reply</button>
            <button onClick={() => setMenuOpen(pickerOpen ? false : !pickerOpen)} className="text-[10px] text-ink3 hover:text-ink">react</button>
            {own && <button onClick={onEdit} className="text-[10px] text-ink3 hover:text-ink"><Pencil className="h-2.5 w-2.5 inline" /> edit</button>}
            {own && <button onClick={() => setMenuOpen(true)} className="text-[10px] text-ink3 hover:text-ink"><Trash2 className="h-2.5 w-2.5 inline" /> delete</button>}
          </div>
        )}
        {onPin && (
          <button onClick={() => void onPin(!pinned)} className={`mt-0.5 flex items-center gap-1 text-[10px] ${pinned ? 'text-ink' : 'text-ink3 opacity-0 transition-opacity group-hover:opacity-100 hover:text-ink'}`}>
            <Pin className="h-2.5 w-2.5 inline" /> {pinned ? 'Pinned' : 'Pin'}
          </button>
        )}
      </div>

      {menuOpen && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setMenuOpen(false)} />
          <div className="absolute right-0 top-0 z-50 mt-6 w-44 rounded-xl border border-border bg-surface p-1.5 shadow-cardHover">
            <button onClick={() => { onDelete('me'); setMenuOpen(false) }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink hover:bg-surface2">Delete for me</button>
            <button onClick={() => { onDelete('everyone'); setMenuOpen(false) }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink hover:bg-surface2">Delete for everyone</button>
          </div>
        </>
      )}
      {pickerOpen && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setPicker('')} />
          <div className="absolute bottom-0 z-50 flex gap-1 rounded-xl border border-border bg-surface p-2 shadow-cardHover">
            {EMOJIS.map((e) => (
              <button key={e} onClick={() => setPicker(e)} className="text-lg hover:scale-125">{e}</button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
