import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ChevronDown, ChevronRight, LifeBuoy } from 'lucide-react'
import { Card } from '@/components/ui/Card'
import { usePageMeta } from '@/lib/meta'

const FAQS: { q: string; a: string }[] = [
  { q: 'Is BizVerse really free?', a: 'Yes. Every feature — storefronts, chat, reviews, leaderboards — is free forever. There are no plans to charge for core features.' },
  { q: 'How does the verification badge work?', a: 'Businesses can reach two levels: Verified (info checked by our team) and Fully Verified (info + uploaded documents such as a business registration or license).' },
  { q: 'How do leaderboards work?', a: 'Engagement — views, likes, recommends, reviews, comments, saves — is scored with time decay. What is booming right now ranks higher, and newer businesses get a Rising boost so hidden gems are found.' },
  { q: 'Can I message any business?', a: 'Yes, and any user. Messaging is full-featured: media, replies, reactions, edit, delete, and read receipts. Block anyone you do not want to hear from.' },
  { q: 'How do I delete my account?', a: 'Settings → Security → Delete account. You have a 14-day grace period and can cancel any time before it completes.' },
  { q: 'My business was rejected. What now?', a: 'You will see the reason on the verification page. Fix the issue and resubmit — up to 3 attempts. After that, contact us through the contact form.' },
]

function FaqItem({ q, a }: { q: string; a: string }) {
  const [open, setOpen] = useState(false)
  return (
    <button onClick={() => setOpen((v) => !v)} className="card w-full p-4 text-left">
      <div className="flex items-center gap-2">
        {open ? <ChevronDown className="h-4 w-4 shrink-0 text-ink3" /> : <ChevronRight className="h-4 w-4 shrink-0 text-ink3" />}
        <span className="text-sm font-medium text-ink">{q}</span>
      </div>
      {open && <p className="mt-2 text-sm leading-relaxed text-ink2">{a}</p>}
    </button>
  )
}

/** Help center: FAQ + guides + contact (B2). */
export function HelpPage() {
  usePageMeta('Help center')
  return (
    <div className="container-page max-w-2xl py-10">
      <div className="mb-8 flex items-center gap-3">
        <LifeBuoy className="h-6 w-6 text-ink" />
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Help center</h1>
          <p className="text-sm text-ink2">Answers about discovering, messaging, and owning businesses.</p>
        </div>
      </div>

      <h2 className="mono-label mb-3">Frequently asked questions</h2>
      <div className="space-y-2">
        {FAQS.map((f) => <FaqItem key={f.q} q={f.q} a={f.a} />)}
      </div>

      <h2 className="mono-label mb-3 mt-8">Guides</h2>
      <div className="grid gap-3 sm:grid-cols-2">
        <Link to="/help/business" className="card p-4 transition-shadow hover:shadow-cardHover">
          <p className="text-sm font-semibold text-ink">For business owners</p>
          <p className="mt-1 text-xs text-ink2">Register, get verified, build your storefront, and grow with analytics.</p>
        </Link>
        <Link to="/contact" className="card p-4 transition-shadow hover:shadow-cardHover">
          <p className="text-sm font-semibold text-ink">Contact us</p>
          <p className="mt-1 text-xs text-ink2">Questions, appeals, or issues — reach the team directly.</p>
        </Link>
      </div>
    </div>
  )
}

export function BusinessHelpPage() {
  usePageMeta('Owner guide')
  const steps = [
    { t: 'Register your account', d: 'Sign up and verify your email — it takes a minute.' },
    { t: 'Create your business', d: 'The wizard walks through info, location, contact, hours, and documents.' },
    { t: 'Get verified', d: 'Our team reviews your info and documents. Fully Verified gives the highest trust badge.' },
    { t: 'Design your storefront', d: 'Pick a theme, arrange sections, and preview live on desktop and mobile.' },
    { t: 'Add products & services', d: 'Variants, pricing, stock, and photos — your catalog is your own.' },
    { t: 'Publish & grow', d: 'Publish your page, reply to reviews, post announcements, and watch your analytics.' },
  ]
  return (
    <div className="container-page max-w-2xl py-10">
      <h1 className="text-2xl font-semibold tracking-tight">Getting started for owners</h1>
      <p className="mt-1 text-sm text-ink2">Everything your business needs, free forever.</p>
      <div className="mt-8 space-y-4">
        {steps.map((s, i) => (
          <Card key={s.t} className="flex gap-4">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent font-mono text-sm font-semibold text-accent-ink">{i + 1}</span>
            <div>
              <p className="text-sm font-semibold text-ink">{s.t}</p>
              <p className="mt-1 text-sm text-ink2">{s.d}</p>
            </div>
          </Card>
        ))}
      </div>
    </div>
  )
}
