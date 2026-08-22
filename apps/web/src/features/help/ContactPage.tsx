import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Send } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { usePageMeta } from '@/lib/meta'

/** Support contact + suspension appeal (B2). */
export function ContactPage() {
  usePageMeta('Contact us')
  const [subject, setSubject] = useState('')
  const [message, setMessage] = useState('')
  const [appeal, setAppeal] = useState('')
  const [sent, setSent] = useState(false)

  const contact = useMutation({
    mutationFn: () => api('/support/contact', { method: 'POST', body: { subject, message } }),
    onSuccess: () => setSent(true),
  })

  const submitAppeal = useMutation({
    mutationFn: () => api('/me/appeal', { method: 'POST', body: { reason: appeal } }),
    onSuccess: () => setAppeal(''),
  })

  return (
    <div className="container-page max-w-2xl py-10 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Contact us</h1>
        <p className="mt-1 text-sm text-ink2">We read everything. Expect a reply within 48 hours.</p>
      </div>

      <Card className="space-y-4">
        <p className="mono-label">Send a message</p>
        <Input label="Subject" value={subject} onChange={(e) => setSubject(e.target.value)} placeholder="What is this about?" />
        <textarea rows={5} value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Tell us more…" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
        {sent && <p className="text-sm text-ink2">Message sent. We will get back to you.</p>}
        <Button onClick={() => void contact.mutateAsync()} disabled={!subject.trim() || message.trim().length < 10 || contact.isPending}>
          <Send className="h-4 w-4" /> Send
        </Button>
      </Card>

      <Card className="space-y-4">
        <p className="mono-label">Suspended or banned? Submit an appeal</p>
        <textarea rows={4} value={appeal} onChange={(e) => setAppeal(e.target.value)} placeholder="Explain what happened…" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
        {submitAppeal.error && <p className="text-sm text-red-600 dark:text-red-400">{(submitAppeal.error as Error).message}</p>}
        <Button variant="secondary" onClick={() => void submitAppeal.mutateAsync()} disabled={appeal.trim().length < 10 || submitAppeal.isPending}>Submit appeal</Button>
      </Card>
    </div>
  )
}
