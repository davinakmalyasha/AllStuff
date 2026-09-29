import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { HelpCircle } from 'lucide-react'
import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { useAuthState } from '@/stores/auth'

interface AnswerDTO {
  id: string
  question_id: string
  user_id: string
  text: string
  is_owner: boolean
  created_at: string
  author_name: string
  author_username: string
}
interface QuestionDTO {
  id: string
  business_id: string
  user_id: string
  text: string
  status: string
  created_at: string
  author_name: string
  author_username: string
  answers?: AnswerDTO[]
}

/** Q&A on business pages (B5) â€” anyone can ask, owner answers highlighted. */
export function QASection({ businessId }: { businessId: string }) {
  const qc = useQueryClient()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const [text, setText] = useState('')
  const [answerFor, setAnswerFor] = useState<string | null>(null)
  const [answerText, setAnswerText] = useState('')

  const { data } = useQuery({
    queryKey: ['questions', businessId],
    queryFn: () => api<{ questions: QuestionDTO[] }>(`/businesses/${businessId}/questions?limit=20`),
  })

  const ask = useMutation({
    mutationFn: () => api(`/businesses/${businessId}/questions`, { method: 'POST', body: { text } }),
    onSuccess: () => {
      setText('')
      qc.invalidateQueries({ queryKey: ['questions', businessId] })
      toast.success('Question posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the question.'),
  })
  const answer = useMutation({
    mutationFn: () => api(`/questions/${answerFor}/answers`, { method: 'POST', body: { text: answerText } }),
    onSuccess: () => {
      setAnswerFor(null)
      setAnswerText('')
      qc.invalidateQueries({ queryKey: ['questions', businessId] })
      toast.success('Answer posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the answer.'),
  })

  return (
    <section>
      <h2 className="mono-label mb-3 flex items-center gap-2"><HelpCircle className="h-3.5 w-3.5" /> Questions & answers</h2>
      {user && (
        <div className="mb-4 flex gap-2">
          <input
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void ask.mutateAsync()}
            placeholder="Ask about this business â€” parking, menu, availabilityâ€¦"
            className="h-10 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
          />
          <Button onClick={() => void ask.mutateAsync()} disabled={text.trim().length < 3 || ask.isPending}>Ask</Button>
        </div>
      )}
      <div className="space-y-3">
        {data?.questions.map((q) => (
          <div key={q.id} className="rounded-lg border border-border p-3">
            <div className="flex items-center gap-2">
              <span className="text-xs font-medium text-ink">{q.author_name}</span>
              <span className="text-[10px] text-ink3">{formatDate(q.created_at)}</span>
            </div>
            <p className="mt-1 text-sm text-ink2">{q.text}</p>
            <div className="mt-2 space-y-2">
              {q.answers?.map((a) => (
                <div key={a.id} className={`rounded-lg px-3 py-2 text-sm ${a.is_owner ? 'border-l-2 border-ink bg-surface2' : 'ml-4 bg-surface'}`}>
                  <p className="text-[10px] text-ink3">{a.is_owner ? 'Owner' : a.author_name}</p>
                  <p className="mt-0.5 text-ink2">{a.text}</p>
                </div>
              ))}
            </div>
            {user && answerFor !== q.id && (
              <button onClick={() => setAnswerFor(q.id)} className="mt-2 text-xs text-ink3 hover:text-ink">Answer</button>
            )}
            {answerFor === q.id && (
              <div className="mt-2 flex gap-2">
                <input value={answerText} onChange={(e) => setAnswerText(e.target.value)} placeholder="Your answerâ€¦" className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
                <Button size="sm" onClick={() => void answer.mutateAsync()} disabled={!answerText.trim() || answer.isPending}>Post</Button>
              </div>
            )}
          </div>
        ))}
        {!data?.questions.length && <p className="text-sm text-ink3">No questions yet.</p>}
      </div>
    </section>
  )
}
