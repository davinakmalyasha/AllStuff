// WebSocket client (PRD §5.5.3): cookie-authenticated, reconnect with
// backoff, subscribe/apply chat events. Single connection per tab.

type WsFrame = { id?: string; type: string; payload?: unknown }

type Handler = (frame: WsFrame) => void

const MAX_QUEUED = 50

class WsClient {
  private ws: WebSocket | null = null
  private handlers = new Map<string, Set<Handler>>()
  private subscribed = new Set<string>()
  private reconnectDelay = 1000
  private closed = false
  private queue: WsFrame[] = []
  private statusListeners = new Set<(connected: boolean) => void>()
  connected = false

  onStatusChange(cb: (connected: boolean) => void) {
    this.statusListeners.add(cb)
    cb(this.connected)
    return () => {
      this.statusListeners.delete(cb)
    }
  }

  private setConnected(v: boolean) {
    if (this.connected === v) return
    this.connected = v
    this.statusListeners.forEach((cb) => cb(v))
  }

  connect() {
    // close() is no longer terminal: a fresh connect() after logout/login
    // must work, so clear the flag here.
    this.closed = false
    if (this.ws) return
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${window.location.host}/api/v1/ws`)
    this.ws = ws
    ws.onopen = () => {
      this.setConnected(true)
      this.reconnectDelay = 1000
      for (const t of this.subscribed) this.send({ type: 'subscribe', payload: { thread_ids: [t] } })
      while (this.queue.length) this.send(this.queue.shift()!)
    }
    ws.onmessage = (e) => {
      try {
        const frame = JSON.parse(e.data as string) as WsFrame
        this.handlers.get(frame.type)?.forEach((h) => h(frame))
      } catch {
        /* ignore malformed */
      }
    }
    ws.onclose = () => {
      this.setConnected(false)
      this.ws = null
      if (!this.closed) {
        setTimeout(() => this.connect(), this.reconnectDelay)
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30000)
      }
    }
    ws.onerror = () => ws.close()
  }

  on(type: string, handler: Handler) {
    if (!this.handlers.has(type)) this.handlers.set(type, new Set())
    this.handlers.get(type)!.add(handler)
    return () => {
      this.handlers.get(type)?.delete(handler)
    }
  }

  subscribe(threadId: string) {
    this.subscribed.add(threadId)
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.send({ type: 'subscribe', payload: { thread_ids: [threadId] } })
    }
  }

  /** Stop receiving signals for a thread the tab navigated away from —
   * without this the subscription set grows for the whole session and every
   * reconnect resubscribes to every thread ever opened. */
  unsubscribe(threadId: string) {
    this.subscribed.delete(threadId)
  }

  send(frame: WsFrame) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(frame))
    } else {
      // Bound the offline queue; a capped buffer beats unbounded memory and
      // stale replays.
      this.queue.push(frame)
      if (this.queue.length > MAX_QUEUED) this.queue.shift()
    }
  }

  /** Disconnect now. A later connect() reopens cleanly. */
  close() {
    this.closed = true
    this.ws?.close()
    this.ws = null
    this.setConnected(false)
    this.subscribed.clear()
    this.queue = []
  }
}

export const ws = new WsClient()
