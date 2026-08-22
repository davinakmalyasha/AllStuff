import type { NotificationDTO } from '@/lib/api'

export const NOTIF_LABELS: Record<string, string> = {
  review_posted: 'New review on your business',
  review_replied: 'The owner replied to your review',
  comment_on_business: 'New comment on your business',
  comment_mention: 'You were mentioned in a comment',
  question_asked: 'New question on your business',
  question_answered: 'Your question was answered',
  business_update: 'New announcement from a business you follow',
  moderation_warning: 'Moderation notice',
  appeal_result: 'Your appeal was decided',
  message_received: 'New message',
  reaction_added: 'Someone reacted to your message',
  helpful_vote: 'Your review was marked helpful',
  product_review: 'New review on one of your products',
  verification_result: 'Verification decision',
  doc_re_request: 'Document re-requested',
  business_suspended: 'Your business was suspended',
  business_restored: 'Your business was restored',
}

export const NOTIF_FILTERS: { key: string; label: string }[] = [
  { key: '', label: 'All' },
  { key: 'message_received', label: 'Messages' },
  { key: 'review_posted', label: 'Reviews' },
  { key: 'product_review', label: 'Product reviews' },
  { key: 'comment_on_business', label: 'Comments' },
  { key: 'comment_mention', label: 'Mentions' },
  { key: 'question_asked', label: 'Questions' },
  { key: 'verification_result', label: 'Verification' },
  { key: 'business_update', label: 'Updates' },
  { key: 'moderation_warning', label: 'Moderation' },
  { key: 'business_suspended', label: 'Suspensions' },
]

/** Deep-link target for a notification (null = no navigation). */
export function notifUrl(n: NotificationDTO): string | null {
  const p = n.payload as Record<string, string>
  if (p.thread_id) return `/me/messages/${p.thread_id}`
  if (p.business_id) return `/b/${p.business_id}`
  return null
}
