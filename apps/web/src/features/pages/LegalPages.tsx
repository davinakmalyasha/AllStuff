import { usePageMeta } from '@/lib/meta'

export function TermsPage() {
  usePageMeta('Terms of service')
  return (
    <div className="container-page max-w-2xl py-10 space-y-4 text-sm leading-relaxed text-ink2">
      <h1 className="text-2xl font-semibold tracking-tight text-ink">Terms of service</h1>
      <p>BizVerse connects people with businesses. By using the platform you agree to:</p>
      <ul className="list-disc space-y-2 pl-5">
        <li>Providing accurate information about your business and its verification documents.</li>
        <li>Not posting content that is unlawful, defamatory, or infringes others' rights.</li>
        <li>Not engaging in manipulation of rankings, reviews, or leaderboards.</li>
        <li>Treating other users with respect in comments, reviews, and messages.</li>
      </ul>
      <p>We may suspend or remove accounts that violate these terms. Businesses own their content; the platform is provided "as is" without warranties.</p>
      <p>Last updated: August 2026.</p>
    </div>
  )
}

export function PrivacyPage() {
  usePageMeta('Privacy policy')
  return (
    <div className="container-page max-w-2xl py-10 space-y-4 text-sm leading-relaxed text-ink2">
      <h1 className="text-2xl font-semibold tracking-tight text-ink">Privacy policy</h1>
      <ul className="list-disc space-y-2 pl-5">
        <li><b className="text-ink">What we collect:</b> account details, business information, verification documents, and engagement activity.</li>
        <li><b className="text-ink">How it's used:</b> to run the directory, surface honest rankings, and connect you with businesses.</li>
        <li><b className="text-ink">Verification documents:</b> encrypted at rest, visible only to platform admins, access is audited.</li>
        <li><b className="text-ink">Your rights:</b> export all your data anytime, delete your account with a 14-day grace period, opt out of digest emails.</li>
        <li><b className="text-ink">Cookies:</b> strictly necessary (sessions, CSRF, theme preference).</li>
      </ul>
      <p>Last updated: August 2026.</p>
    </div>
  )
}
