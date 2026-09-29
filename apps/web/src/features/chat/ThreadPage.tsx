import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  ArrowLeft,
  Ban,
  Bell,
  BellOff,
  Download,
  FileUp,
  Image as ImageIcon,
  Images,
  LogOut,
  MoreVertical,
  XCircle,

  Pencil,
  Pin,
  Search,
  Send,
  Trash2,
  Video,
  Mic,
} from 'lucide-react'
import {
  api,
  uploadMedia,
  type BusinessDTO,
  type ChatMessageDTO,
  type QuickReplyDTO,
  type ThreadDTO,
  type ThreadListItemDTO,
} from '@/lib/api'
import { useDebouncedValue } from '@/lib/hooks'
import { Button } from '@/components/ui/Button'
import { PageSpinner } from '@/components/ui/Spinner'
import { ws } from '@/lib/ws'
import { safeExternalUrl } from '@/lib/url'
import { useAuthState } from '@/stores/auth'
import { copyText, resetFileInput } from '@/lib/format'
import { toast } from '@/components/ui/Toast'
import { Confirm, Modal, useDialogA11y } from '@/components/ui/Modal'

const EMOJIS = ['ðŸ‘', 'â¤ï¸', 'ðŸ˜‚', 'ðŸ˜®', 'ðŸ˜¢', 'ðŸ™', 'ðŸ”¥', 'ðŸŽ‰', 'âœ…', 'âŒ', 'ðŸ¤”', 'ðŸ‘', 'ðŸ˜', 'ðŸ˜Ž', 'ðŸ’¯', 'ðŸ¥³', 'ðŸ¤', 'ðŸ‘Œ', 'ðŸ˜…', 'ðŸ™Œ']

// Monotonic temp ids: `-Date.now()` collided when two messages were sent in
// the same millisecond (duplicate React keys broke reconciliation).
let tempIdCounter = 0

/** Link previews carry user-pasted URLs: scheme-allowlisted before render. */
function LinkPreview({ preview }: { preview: Record<string, unknown> }) {
  const href = safeExternalUrl(typeof preview.url === 'string' ? preview.url : null)
  const image = typeof preview.image === 'string' ? safeExternalUrl(preview.image) : null
  if (!href) return null
  const isHttpImg = !!image && /^https?:\/\//i.test(image)
  return (
    <a href={href} target="_blank" rel="noreferrer" className="mt-1.5 flex items-center gap-2 rounded-lg border border-border bg-surface2 p-2 no-underline">
      {isHttpImg ? <img src={image!} alt="" className="h-10 w-10 rounded object-cover" /> : null}
      <span className="min-w-0">
        <span className="block truncate text-xs font-semibold text-ink">{String(preview.title ?? preview.url ?? '')}</span>
        {preview.description ? <span className="block truncate text-[10px] text-ink3">{String(preview.description)}</span> : null}
      </span>
    </a>
  )
}

export function ThreadPage({ businessMode = false }: { businessMode?: boolean }) {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const [messages, setMessages] = useState<ChatMessageDTO[]>([])
  const [text, setText] = useState('')
  const [replyTo, setReplyTo] = useState<ChatMessageDTO | null>(null)
  const [typing, setTyping] = useState(false)
  const [pickerFor, setPickerFor] = useState<number | null>(null)
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [editing, setEditing] = useState<ChatMessageDTO | null>(null)
  const [searching, setSearching] = useState(false)
  const [searchQ, setSearchQ] = useState('')
  const debouncedSearchQ = useDebouncedValue(searchQ, 250)
  const [pendingFile, setPendingFile] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const scrollBodyRef = useRef<HTMLDivElement>(null)
  const didInitialScroll = useRef(false)
  const typingTimer = useRef<ReturnType<typeof setTimeout>>()

  // ---- history pagination -------------------------------------------------
  // The API pages by `before` (an exclusive message-id upper bound) and reports
  // has_more. Older history used to be unreachable: the handler hard-limited to
  // 50 messages with no cursor, so in any longer thread the earlier half could
  // not be loaded by any control. The initial page is the newest 50; scrolling
  // to the top walks backwards with the oldest id received so far.
  const [older, setOlder] = useState<ChatMessageDTO[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const loadingOlderRef = useRef(false)

  const { data, isLoading } = useQuery({
    queryKey: ['thread', id],
    queryFn: () => api<{ thread: ThreadDTO; messages: ChatMessageDTO[]; has_more: boolean }>(`/threads/${id}`),
    enabled: !!id,
  })

  // Reset the backward cursor when the thread changes, or a previous thread's
  // older pages would leak into this one.
  useEffect(() => {
    setOlder([])
    setHasMore(false)
  }, [id])

  useEffect(() => {
    if (data) {
      setMessages(data.messages)
      setHasMore(data.has_more ?? false)
    }
  }, [data])

  const oldestLoadedId = useMemo(() => {
    const ids = [...older, ...messages].map((m) => m.id)
    return ids.length ? Math.min(...ids) : 0
  }, [older, messages])

  // Prepending older pages grows the scrollport ABOVE the viewport, so the
  // browser would jump the reader upward by the height of the inserted
  // content. Recording scrollHeight before the request and restoring the
  // delta afterwards keeps the message the reader was looking at in place.
  const preLoadHeightRef = useRef(0)

  const loadOlder = useCallback(async () => {
    if (loadingOlderRef.current || !hasMore || !oldestLoadedId) return
    loadingOlderRef.current = true
    setLoadingOlder(true)
    preLoadHeightRef.current = scrollBodyRef.current?.scrollHeight ?? 0
    try {
      const res = await api<{ messages: ChatMessageDTO[]; has_more: boolean }>(
        `/threads/${id}?before=${oldestLoadedId}&limit=50`,
      )
      setOlder((prev) => {
        const seen = new Set([...prev, ...messages].map((m) => m.id))
        const fresh = res.messages.filter((m) => !seen.has(m.id))
        return fresh.length ? [...fresh, ...prev] : prev
      })
      setHasMore(res.has_more ?? false)
    } catch {
      // Leave has_more set so the control stays available for a retry.
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)
    }
  }, [hasMore, oldestLoadedId, id, messages])

  // Restore the reading position after older messages are prepended. Runs on
  // every commit so the adjustment lands in the same frame as the new nodes.
  useEffect(() => {
    const el = scrollBodyRef.current
    if (!el || !preLoadHeightRef.current) return
    const delta = el.scrollHeight - preLoadHeightRef.current
    preLoadHeightRef.current = 0
    if (delta > 0) el.scrollTop += delta
  })

  // Trigger the next page when the scroll container reaches the top. The
  // container is observed rather than a window scroll listener, because the
  // thread body is its own scrollport.
  useEffect(() => {
    const el = scrollBodyRef.current
    if (!el || !hasMore) return
    const onScroll = () => {
      if (el.scrollTop <= 48) void loadOlder()
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [hasMore, loadOlder])

  // Header label: the detail payload carries ids only, so resolve the display
  // name from the thread list â€” business name for business threads, the other
  // participant for direct ones.
  const { data: labeled } = useQuery({
    queryKey: ['thread-labels'],
    queryFn: () => api<{ threads: ThreadListItemDTO[] }>('/threads'),
    enabled: !!id,
  })
  const listItem = labeled?.threads.find((t) => t.id === id)
  const headerTitle = listItem
    ? listItem.type === 'business'
      ? listItem.business_name ?? 'Chat'
      : listItem.other_name ?? 'Chat'
    : 'Chat'

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
    // Reconnect backfill: frames published while the socket was down are
    // gone forever (server keeps no replay), so refetch the tail on reopen â€”
    // previously messages sent during a blip never appeared.
    const offStatus = ws.onStatusChange((connected) => {
      if (connected) void qc.invalidateQueries({ queryKey: ['thread', id] })
    })
    return () => {
      offs.forEach((off) => off())
      offStatus()
      ws.unsubscribe(id)
    }
  }, [id, user?.id, qc])

  useEffect(() => {
    const el = scrollBodyRef.current
    if (!el) return
    // Assign scrollTop on the CONTAINER rather than calling scrollIntoView().
    //
    // `Element.scrollIntoView()` with no argument scrolls every scrollable
    // ancestor including the document, so the first message after a route
    // change yanks the whole page. Scoping the assignment to the thread's own
    // scrollport cannot affect the document.
    const toBottom = (smooth: boolean) => {
      if (smooth) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
      else el.scrollTop = el.scrollHeight
    }
    if (!didInitialScroll.current) {
      // Initial load lands on the newest message instantly (no animation).
      didInitialScroll.current = true
      toBottom(false)
      return
    }
    // Otherwise follow only while the reader is already near the bottom â€”
    // scrolling up to read history must not be yanked back on every frame.
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    if (distance < 120) toBottom(true)
  }, [messages.length])

  // Typing signal: throttled to one POST per 2.5s while actively typing â€”
  // the old per-keystroke send fired ~40 requests for a single message.
  const lastTypingSent = useRef(0)
  const sendTyping = () => {
    if (!text.trim()) return
    const now = Date.now()
    if (now - lastTypingSent.current < 2500) return
    lastTypingSent.current = now
    void api(`/threads/${id}/typing`, { method: 'POST' }).catch(() => undefined)
  }

  const send = async () => {
    const body = text.trim()
    if (!body && !pendingFile) return
    const clientMsgId = crypto.randomUUID()
    const repliedTo = replyTo
    setText('')
    setReplyTo(null)
    // Optimistic append: without it a dropped socket made sent messages
    // vanish (they only ever appeared via the WS echo).
    const temp: ChatMessageDTO = {
      id: --tempIdCounter,
      thread_id: id,
      sender_id: user?.id ?? '',
      sender_role: businessMode ? 'owner' : 'user',
      type: pendingFile ? pendingKind : 'text',
      body: body || null,
      media_id: pendingFile,
      client_msg_id: clientMsgId,
      reply_to_id: replyTo?.id ?? null,
      forwarded_from_message_id: null,
      link_preview: null,
      read_count: 0,
      edited_at: null,
      edit_history: [],
      deleted_for: 'none',
      deleted_at: null,
      created_at: new Date().toISOString(),
    }
    setMessages((prev) => [...prev, temp])
    try {
      const r = await api<{ message: ChatMessageDTO }>(`/threads/${id}/messages`, {
        method: 'POST',
        body: {
          body: body || undefined,
          type: pendingFile ? pendingKind : 'text',
          media_id: pendingFile,
          client_msg_id: clientMsgId,
          reply_to_id: replyTo?.id,
        },
      })
      // Reconcile optimistic row with the server copy.
      setMessages((prev) => prev.map((x) => (x.client_msg_id === clientMsgId ? r.message : x.id === r.message.id ? r.message : x)))
      void qc.invalidateQueries({ queryKey: ['threads'] })
      setPendingFile(null)
      setRecording(false)
    } catch {
      setMessages((prev) => prev.filter((x) => x.client_msg_id !== clientMsgId))
      // Restore the draft AND the reply context so the user can retry as-is.
      setText(body)
      setReplyTo(repliedTo)
      toast.error('Message failed to send.')
    }
  }

  const [uploadingMedia, setUploadingMedia] = useState(false)
  const uploadImage = async (file: File, kind: 'chat_image' | 'chat_file' | 'chat_video' | 'chat_audio' = 'chat_image') => {
    setUploadingMedia(true)
    try {
      const r = await uploadMedia(kind, file)
      setPendingFile(r.media.id)
      setPendingKind(kind === 'chat_file' ? 'file' : kind === 'chat_video' ? 'video' : kind === 'chat_audio' ? 'audio' : 'image')
    } catch (e) {
      toast.error((e as Error).message || 'Could not upload the attachment.')
    } finally {
      setUploadingMedia(false)
    }
  }
  const [pendingKind, setPendingKind] = useState<'image' | 'file' | 'video' | 'audio'>('image')

  // Voice notes (PRD Â§5.5.2): MediaRecorder â†’ webm â†’ chat_audio upload.
  // Capped at 5 minutes; oversized recordings surface an error toast instead
  // of being silently dropped.
  const [recording, setRecording] = useState(false)
  const recorderRef = useRef<MediaRecorder | null>(null)
  const recTimerRef = useRef<number | null>(null)

  // Unmount cleanup: stop a live recording, release every mic track and the
  // hard-stop timer â€” navigating away mid-recording used to hold the mic.
  useEffect(() => {
    return () => {
      const rec = recorderRef.current
      if (rec && rec.state !== 'inactive') {
        rec.onstop = null
        rec.stop()
      }
      rec?.stream.getTracks().forEach((t) => t.stop())
      recorderRef.current = null
      if (recTimerRef.current !== null) {
        clearTimeout(recTimerRef.current)
        recTimerRef.current = null
      }
    }
  }, [])

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
        // Server cap for chat_audio is 25 MB (~13+ min of webm audio); the
        // recording hard-stops at 5 minutes below. The old 200 KB rejection
        // contradicted the recorder and destroyed >30s takes.
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
    try {
      await api(`/messages/${editing.id}`, { method: 'PATCH', body: { body } })
      setEditing(null)
      setText('')
      void qc.invalidateQueries({ queryKey: ['thread', id] })
    } catch {
      toast.error('Could not save the edit.')
    }
  }

  const del = async (m: ChatMessageDTO, scope: 'me' | 'everyone') => {
    try {
      await api(`/messages/${m.id}?scope=${scope}`, { method: 'DELETE' })
      void qc.invalidateQueries({ queryKey: ['thread', id] })
    } catch {
      toast.error('Could not delete the message.')
    }
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
    try {
      await api(`/messages/${m.id}/reaction`, { method: emoji ? 'PUT' : 'DELETE', body: emoji ? { emoji } : undefined })
    } catch {
      toast.error('Could not update the reaction.')
    }
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
    try {
      await api(`/threads/${id}/pin/${messageId}`, { method: on ? 'PUT' : 'DELETE' })
      setPinnedIds((prev) => (on ? [...prev, messageId] : prev.filter((x) => x !== messageId)))
    } catch {
      toast.error('Could not update the pin.')
    }
    // Refetch either way so the optimistic set reconciles with server truth.
    void qc.invalidateQueries({ queryKey: ['pinned', id] })
  }

  const { data: searchRes } = useQuery({
    queryKey: ['thread-search', id, debouncedSearchQ],
    queryFn: () => api<{ messages: ChatMessageDTO[] }>(`/threads/${id}/search?q=${encodeURIComponent(debouncedSearchQ)}`),
    enabled: searching && debouncedSearchQ.trim().length >= 2,
  })

  // Thread mute (PRD Â§5.5.1): optimistic toggle only. Neither the thread
  // detail nor list payloads carry muted state client-side, so there is no
  // server value to seed from â€” the bell starts unmuted on each visit.
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

  // Pinned conversation (PRD Â§5.5.1, max 5): header toggle.
  const [pinnedThread, setPinnedThread] = useState(false)
  const [pinnedTick, setPinnedTick] = useState(0)
  useEffect(() => {
    api<{ threads: Array<{ id: string }> }>(`/me/pinned-threads`)
      .then((r) => setPinnedThread(r.threads.some((t) => t.id === id)))
      .catch(() => undefined)
  }, [id, pinnedTick])
  const togglePinThread = async () => {
    const next = !pinnedThread
    setPinnedThread(next)
    try {
      await api(`/threads/${id}/pinned-thread`, { method: next ? 'PUT' : 'DELETE' })
      void qc.invalidateQueries({ queryKey: ['threads'] })
    } catch {
      setPinnedThread(!next)
      toast.error(next ? 'Pin limit is 5 conversations.' : 'Could not unpin.')
    }
    // Re-sync from the pinned-threads list so optimism converges on truth.
    setPinnedTick((t) => t + 1)
  }

  const own = (m: ChatMessageDTO) => m.sender_id === user?.id
  // `older` holds pages fetched before the initial (newest) page, so the full
  // transcript is older-first followed by messages. Soft-deleted-for-me rows are
  // filtered out of the render but still count toward the pagination cursor.
  const display = useMemo(
    () =>
      [...older, ...messages].filter(
        (m) => !(m.deleted_for === 'me' && m.sender_id === user?.id),
      ),
    [older, messages, user?.id],
  )

  // ---- power features (PRD Â§5.5.2/Â§5.5.4) ----

  const thread = data?.thread
  const closed = thread?.status === 'closed'
  // Owner-only affordances (close) are gated on the business console route:
  // the server re-checks ownership on POST /threads/:id/close anyway.
  const isOwnerView = businessMode && thread?.type === 'business'

  const [headerMenu, setHeaderMenu] = useState(false)
  const [galleryOpen, setGalleryOpen] = useState(false)
  const [fullMedia, setFullMedia] = useState<string | null>(null)
  const [forwardFor, setForwardFor] = useState<ChatMessageDTO | null>(null)
  const [forwardQ, setForwardQ] = useState('')
  const [confirmAction, setConfirmAction] = useState<'close' | 'leave' | 'block' | null>(null)

  // Media gallery: images + videos shared in this thread.
  const { data: galleryData } = useQuery({
    queryKey: ['thread-media', id],
    queryFn: () => api<{ messages: ChatMessageDTO[] }>(`/threads/${id}/media`),
    enabled: galleryOpen,
  })
  const gallery = useMemo(
    () => (galleryData?.messages ?? []).filter((m) => (m.type === 'image' || m.type === 'video') && m.media_id),
    [galleryData],
  )

  // Block list â€” only consulted for direct threads.
  const otherUserId = listItem?.type === 'direct' ? listItem.other_id : null
  const { data: blocksData } = useQuery({
    queryKey: ['blocks'],
    queryFn: () => api<{ blocked_ids: string[] }>('/blocks'),
    enabled: !!id,
  })
  const isBlocked = !!otherUserId && (blocksData?.blocked_ids ?? []).includes(otherUserId)

  // Quick replies for business threads where the viewer manages the business
  // (owner console route, or membership in /businesses).
  const { data: myBizData } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!thread?.business_id,
  })
  const managesBusiness =
    !!thread?.business_id &&
    (businessMode || (myBizData?.businesses ?? []).some((b) => b.id === thread.business_id))
  const { data: quickRepliesData } = useQuery({
    queryKey: ['quick-replies', thread?.business_id],
    queryFn: () => api<{ quick_replies: QuickReplyDTO[] }>(`/businesses/${thread!.business_id}/quick-replies`),
    enabled: managesBusiness,
  })
  const insertQuickReply = (replyText: string) =>
    setText((prev) => (prev.trim() ? `${prev.trimEnd()} ${replyText}` : replyText))

  const exportThread = async () => {
    try {
      const payload = await api<unknown>(`/threads/${id}/export`)
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `bizverse-thread-${id}.json`
      a.click()
      URL.revokeObjectURL(url)
    } catch {
      toast.error('Could not export the conversation.')
    }
  }

  const forwardTo = async (targetThreadId: string) => {
    if (!forwardFor) return
    try {
      await api(`/messages/${forwardFor.id}/forward`, { method: 'POST', body: { thread_id: targetThreadId } })
      toast.success('Message forwarded.')
      void qc.invalidateQueries({ queryKey: ['threads'] })
      setForwardFor(null)
      setForwardQ('')
    } catch {
      toast.error('Could not forward the message.')
    }
  }

  const closeThread = async () => {
    try {
      await api(`/threads/${id}/close`, { method: 'POST' })
      toast.success('Conversation closed.')
      void qc.invalidateQueries({ queryKey: ['thread', id] })
      void qc.invalidateQueries({ queryKey: ['threads'] })
    } catch {
      toast.error('Could not close the conversation.')
    }
  }

  const leaveThread = async () => {
    try {
      await api(`/threads/${id}/leave`, { method: 'POST' })
      toast.success('You left the conversation.')
      void qc.invalidateQueries({ queryKey: ['threads'] })
      navigate(businessMode ? '/dashboard/chats' : '/me/messages')
    } catch {
      toast.error('Could not leave the conversation.')
    }
  }

  const toggleBlock = async () => {
    if (!otherUserId) return
    try {
      await api(`/blocks/${otherUserId}`, { method: isBlocked ? 'DELETE' : 'POST' })
      toast.success(isBlocked ? 'User unblocked.' : 'User blocked.')
      void qc.invalidateQueries({ queryKey: ['blocks'] })
    } catch {
      toast.error('Could not update the block.')
    }
  }

  const forwardTargets = useMemo(() => {
    const q = forwardQ.trim().toLowerCase()
    return (labeled?.threads ?? [])
      .filter((t) => t.id !== id)
      .filter((t) => !q || [t.business_name, t.other_name, t.last_body].some((v) => v?.toLowerCase().includes(q)))
  }, [labeled, id, forwardQ])

  if (isLoading) return <PageSpinner />

  return (
    <div className="flex h-[calc(100vh-64px)] flex-col">
      {/* Header */}
      <div className="flex items-center gap-3 border-b border-border px-4 py-3">
        <button onClick={() => navigate(businessMode ? '/dashboard/chats' : '/me/messages')} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Back" title="Back">
          <ArrowLeft className="h-4 w-4" />
        </button>
        <p className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{headerTitle}</p>
        <button onClick={() => void togglePinThread()} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label={pinnedThread ? 'Unpin conversation' : 'Pin conversation'} title={pinnedThread ? 'Unpin' : 'Pin (max 5)'}>
          <Pin className={`h-4 w-4 ${pinnedThread ? 'fill-current text-ink' : ''}`} />
        </button>
        <button onClick={() => void toggleMute()} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label={muted ? 'Unmute notifications' : 'Mute notifications'} title={muted ? 'Unmute' : 'Mute'}>
          {muted ? <BellOff className="h-4 w-4" /> : <Bell className="h-4 w-4" />}
        </button>
        <button onClick={() => setSearching((v) => !v)} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Search" title="Search">
          <Search className="h-4 w-4" />
        </button>
        <button onClick={() => setGalleryOpen(true)} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Shared media" title="Shared media">
          <Images className="h-4 w-4" />
        </button>
        <div className="relative">
          <button onClick={() => setHeaderMenu((v) => !v)} className="rounded-lg p-1.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Conversation options" title="Options" aria-expanded={headerMenu}>
            <MoreVertical className="h-4 w-4" />
          </button>
          {headerMenu && (
            <>
              <div className="fixed inset-0 z-40" onClick={() => setHeaderMenu(false)} />
              <div className="absolute right-0 top-full z-50 mt-1 w-48 rounded-xl border border-border bg-surface p-1.5 shadow-cardHover">
                <button
                  onClick={() => { setHeaderMenu(false); void exportThread() }}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink hover:bg-surface2"
                >
                  <Download className="h-3.5 w-3.5" /> Export chat
                </button>
                {isOwnerView && (
                  <button
                    onClick={() => { setHeaderMenu(false); setConfirmAction('close') }}
                    disabled={closed}
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink hover:bg-surface2 disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <XCircle className="h-3.5 w-3.5" /> {closed ? 'Conversation closed' : 'Close conversation'}
                  </button>
                )}
                <button
                  onClick={() => { setHeaderMenu(false); setConfirmAction('leave') }}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink hover:bg-surface2"
                >
                  <LogOut className="h-3.5 w-3.5" /> Leave conversation
                </button>
                {otherUserId && (
                  <button
                    onClick={() => { setHeaderMenu(false); setConfirmAction('block') }}
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-red-700 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30"
                  >
                    <Ban className="h-3.5 w-3.5" /> {isBlocked ? 'Unblock user' : 'Block user'}
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      </div>

      {closed && (
        <div className="border-b border-border bg-surface2 px-4 py-2 text-center text-xs text-ink2">
          {t('chat.closedBanner')}
        </div>
      )}

      {searching && (
        <div className="border-b border-border p-3">
          <input
            value={searchQ}
            onChange={(e) => setSearchQ(e.target.value)}
            placeholder={t('chat.searchPlaceholder')}
            className="h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
            autoFocus
          />
          {searchRes?.messages.map((m) => (
            <button key={m.id} onClick={() => { setSearching(false); setSearchQ(''); setJump(m.id) }} className="mt-2 block w-full rounded-lg bg-surface2 px-3 py-2 text-left text-sm text-ink2 hover:text-ink">
              {m.body}
            </button>
          ))}
        </div>
      )}

      {/* Messages */}
      <div ref={scrollBodyRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {/*
          Older-history control. Scrolling to the top also triggers loadOlder,
          but this button is the keyboard/touch-reachable equivalent: a
          scroll-position listener alone is unreachable without a pointer and
          invisible to screen readers.
        */}
        {hasMore && (
          <div className="flex justify-center pb-2">
            <button
              type="button"
              onClick={() => void loadOlder()}
              disabled={loadingOlder}
              className="rounded-full border border-border px-3 py-1.5 text-xs text-ink2 hover:bg-surface2 disabled:opacity-50"
            >
              {loadingOlder ? 'Loading earlier messagesâ€¦' : 'Load earlier messages'}
            </button>
          </div>
        )}
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
            togglePicker={() => setPickerFor((v) => (v === m.id ? null : m.id))}
            closePicker={() => setPickerFor(null)}
            setPicker={(emoji) => void react(m, emoji)}
            replyTarget={m.reply_to_id ? display.find((x) => x.id === m.reply_to_id) : undefined}
            showActions={!businessMode || m.sender_role !== 'owner'}
            onJump={setJump}
            onForward={() => { setForwardFor(m); setMenuFor(null) }}
          />
        ))}
        {typing && (
          <p className="px-1 text-xs text-ink3" aria-live="polite">{t('chat.typing')}</p>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Reply quote */}
      {replyTo && (
        <div className="flex items-center gap-2 border-t border-border bg-surface2 px-4 py-2">
          <p className="flex-1 truncate text-xs text-ink2">{t('chat.replyingTo')} {replyTo.body ?? 'media'}</p>
          <button onClick={() => setReplyTo(null)} className="text-ink3 hover:text-ink">âœ•</button>
        </div>
      )}

      {/* Composer */}
      <div className="border-t border-border p-3">
        {managesBusiness && (quickRepliesData?.quick_replies ?? []).length > 0 && (
          <div className="mb-2 flex gap-2 overflow-x-auto pb-1" role="listbox" aria-label="Quick replies">
            {quickRepliesData!.quick_replies.map((q) => (
              <button
                key={q.id}
                onClick={() => insertQuickReply(q.text)}
                className="shrink-0 rounded-full border border-border px-3 py-1 text-xs text-ink2 hover:bg-surface2 hover:text-ink"
                title="Insert quick reply"
              >
                {q.text}
              </button>
            ))}
          </div>
        )}
        {pendingFile && <p className="mb-2 text-xs text-ink2">{uploadingMedia ? 'â³' : 'ðŸ“Ž'} {pendingKind} ready to send</p>}
        <div className="flex items-end gap-2">
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send image">
            <ImageIcon className="h-5 w-5" />
            <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; resetFileInput(e); if (f) void uploadImage(f, 'chat_image') }} />
          </label>
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send file">
            <FileUp className="h-5 w-5" />
            <input type="file" accept=".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.csv" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; resetFileInput(e); if (f) void uploadImage(f, 'chat_file') }} />
          </label>
          <label className="cursor-pointer rounded-lg p-2 text-ink3 hover:bg-surface2 hover:text-ink" title="Send video">
            <Video className="h-5 w-5" />
            <input type="file" accept="video/mp4,video/webm" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; resetFileInput(e); if (f) void uploadImage(f, 'chat_video') }} />
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
                placeholder="Editingâ€¦"
                autoFocus
              />
              <Button size="sm" onClick={() => void edit()} disabled={!text.trim()}>Save</Button>
              <Button variant="secondary" size="sm" onClick={() => { setEditing(null); setText('') }}>âœ•</Button>
            </>
          ) : (
            <>
              <input
                value={text}
                onChange={(e) => { setText(e.target.value); sendTyping() }}
                onKeyDown={(e) => e.key === 'Enter' && void send()}
                className="h-10 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
                placeholder={replyTo ? t('chat.replyPlaceholder') : t('chat.composerPlaceholder')}
                disabled={closed}
              />
              <Button size="sm" onClick={() => void send()} disabled={closed || (!text.trim() && !pendingFile)} aria-label={t('chat.send')}>
                <Send className="h-4 w-4" />
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Forward message */}
      <Modal open={!!forwardFor} onClose={() => { setForwardFor(null); setForwardQ('') }} title={t('chat.forwardTitle')} maxWidth="max-w-md">
        <input
          value={forwardQ}
          onChange={(e) => setForwardQ(e.target.value)}
          placeholder="Search conversationsâ€¦"
          className="h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
          autoFocus
        />
        <div className="mt-2 max-h-72 space-y-1 overflow-y-auto">
          {forwardTargets.map((t) => (
            <button
              key={t.id}
              onClick={() => void forwardTo(t.id)}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left hover:bg-surface2"
            >
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface2 text-sm font-semibold text-ink2">
                {(t.type === 'business' ? t.business_name ?? 'B' : t.other_name ?? 'U').charAt(0)}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-ink">
                  {t.type === 'business' ? t.business_name ?? 'Business' : t.other_name ?? 'User'}
                </span>
                <span className="block truncate text-xs text-ink3">{t.last_body ?? 'Start the conversation'}</span>
              </span>
            </button>
          ))}
          {forwardTargets.length === 0 && <p className="px-3 py-6 text-center text-sm text-ink3">No other conversations.</p>}
        </div>
      </Modal>

      {/* Shared media */}
      <Modal open={galleryOpen} onClose={() => { setGalleryOpen(false); setFullMedia(null) }} title={t('chat.galleryTitle')} maxWidth="max-w-2xl">
        {gallery.length === 0 ? (
          <p className="py-10 text-center text-sm text-ink3">No photos or videos shared here yet.</p>
        ) : (
          <div className="grid max-h-[60vh] grid-cols-2 gap-1.5 overflow-y-auto sm:grid-cols-3">
            {gallery.map((m) => (
              <button
                key={m.id}
                onClick={() => m.type === 'image' && setFullMedia(`/api/v1/media/${m.media_id}/file`)}
                className="group relative overflow-hidden rounded-lg"
                aria-label={m.type === 'image' ? 'View full size' : undefined}
              >
                {m.type === 'video' ? (
                  <video src={`/api/v1/media/${m.media_id}/file`} muted preload="metadata" className="h-28 w-full object-cover" />
                ) : (
                  <img src={`/api/v1/media/${m.media_id}/file`} alt="" loading="lazy" className="h-28 w-full object-cover transition-transform group-hover:scale-105" />
                )}
                {m.type === 'video' && (
                  <span className="absolute inset-0 flex items-center justify-center bg-black/20">
                    <Video className="h-5 w-5 text-white" />
                  </span>
                )}
              </button>
            ))}
          </div>
        )}
      </Modal>

      {/* Full-size image viewer */}
      {fullMedia && (
        <div className="fixed inset-0 z-[60] flex cursor-zoom-out items-center justify-center bg-black/80 p-4" onClick={() => setFullMedia(null)}>
          <img src={fullMedia} alt="" className="max-h-full max-w-full rounded-lg object-contain" />
        </div>
      )}

      {/* Destructive action confirmations */}
      <Confirm
        open={confirmAction === 'close'}
        onClose={() => setConfirmAction(null)}
        onConfirm={() => void closeThread()}
        title={t('chat.confirmCloseTitle')}
        message={t('chat.confirmCloseMessage')}
        confirmLabel={t('chat.closeCta')}
        danger
      />
      <Confirm
        open={confirmAction === 'leave'}
        onClose={() => setConfirmAction(null)}
        onConfirm={() => void leaveThread()}
        title={t('chat.confirmLeaveTitle')}
        message={t('chat.confirmLeaveMessage')}
        confirmLabel={t('chat.leaveCta')}
        danger
      />
      <Confirm
        open={confirmAction === 'block'}
        onClose={() => setConfirmAction(null)}
        onConfirm={() => void toggleBlock()}
        title={isBlocked ? t('chat.confirmUnblockTitle') : t('chat.confirmBlockTitle')}
        message={
          isBlocked
            ? t('chat.confirmUnblockMessage')
            : t('chat.confirmBlockMessage')
        }
        confirmLabel={isBlocked ? t('chat.unblockCta') : t('chat.blockCta')}
        danger={!isBlocked}
      />
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
  togglePicker,
  closePicker,
  setPicker,
  replyTarget,
  showActions,
  onJump,
  onForward,
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
  togglePicker: () => void
  closePicker: () => void
  setPicker: (emoji: string) => void
  replyTarget?: ChatMessageDTO
  showActions: boolean
  onJump: (id: number) => void
  onForward?: () => void
}) {
  const { t } = useTranslation()
  const pickerRef = useDialogA11y(pickerOpen, closePicker)
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
            â†³ {replyTarget.body ?? 'media'}
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
              ðŸ“Ž {m.body ?? 'Download file'}
            </a>
          )}
          {m.type === 'audio' && m.media_id && (
            <audio src={`/api/v1/media/${m.media_id}/file`} controls className="mb-1.5 h-10 w-56 max-w-full" />
          )}
          {m.body && <p className="whitespace-pre-wrap break-words">{m.body}</p>}
          {m.link_preview && (
            <LinkPreview preview={m.link_preview} />
          )}
          {m.forwarded_from_message_id && <p className="mt-1 text-[10px] opacity-60">{t('chat.forwardedBadge')}</p>}
          {m.edited_at && <p className="mt-0.5 text-right text-[9px] opacity-50">{t('chat.editedBadge')}</p>}
          <p className={`mt-0.5 text-right text-[9px] ${own ? 'opacity-60' : 'text-ink3'}`}>
            {new Date(m.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </p>
        </div>
        {showActions && (
          // Visible on keyboard focus and coarse pointers too â€” hover-only
          // actions were unreachable without a mouse.
          <div className={`mt-0.5 flex items-center gap-2 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(pointer:coarse)]:opacity-100 ${own ? 'justify-end' : ''}`}>
            <button onClick={() => { void copyText(m.body ?? '').then((ok) => toast.success(ok ? 'Copied' : 'Copy failed')) }} className="text-[10px] text-ink3 hover:text-ink">{t('chat.actCopy')}</button>
            <button onClick={onReply} className="text-[10px] text-ink3 hover:text-ink">{t('chat.actReply')}</button>
            {onForward && <button onClick={onForward} className="text-[10px] text-ink3 hover:text-ink">{t('chat.actForward')}</button>}
            <button onClick={togglePicker} className="text-[10px] text-ink3 hover:text-ink">{t('chat.actReact')}</button>
            {own && <button onClick={onEdit} className="text-[10px] text-ink3 hover:text-ink"><Pencil className="h-2.5 w-2.5 inline" /> {t('chat.actEdit')}</button>}
            {own && <button onClick={() => setMenuOpen(true)} className="text-[10px] text-ink3 hover:text-ink"><Trash2 className="h-2.5 w-2.5 inline" /> {t('chat.actDelete')}</button>}
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
          {/* Backdrop only dismisses the picker â€” reacting never happens via it. */}
          <div className="fixed inset-0 z-40" onClick={closePicker} />
          <div
            ref={pickerRef}
            tabIndex={-1}
            role="dialog"
            aria-modal="true"
            aria-label="Pick a reaction"
            className="absolute bottom-0 z-50 flex gap-1 rounded-xl border border-border bg-surface p-2 shadow-cardHover outline-none"
          >
            {EMOJIS.map((e) => (
              <button key={e} onClick={() => setPicker(e)} className="text-lg hover:scale-125">{e}</button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
