# BizVerse — Product Requirements Document

> Working title: **BizVerse** (rename freely). Status: **complete and approved**.
> Build directive: **golden standard — no MVP-tier scope**. Every feature ships complete; the word "deferred" no longer applies to in-scope features (Decisions Log §13).

| # | Section | Status |
|---|---------|--------|
| 1 | Product Overview & Vision | ✅ approved |
| 2 | Personas & Roles | ✅ approved |
| 3 | Core User Journeys | ✅ approved |
| 4 | Goals & Non-Goals | ✅ approved |
| 5 | Feature Specifications | ✅ approved |
| 6 | Information Architecture | ✅ approved |
| 7 | Data Model | ✅ approved |
| 8 | Business Rules & Validation | ✅ approved |
| 9 | Non-Functional Requirements | ✅ approved |
| 10 | Tech Stack & Architecture | ✅ approved |
| 11 | Edge Cases & Error Handling | ✅ approved |
| 12 | Milestones & Delivery Plan | ✅ approved |
| 13 | Decisions Log | ✅ approved |

---

## 1. Product Overview & Vision ✅

**One-liner:** A universal, free platform where every business in the world gets a customizable storefront, and every user finds them through one searchable, category-driven directory.

**Problem:** Businesses without websites are invisible outside social media. Users must hop between Instagram/TikTok threads to piece together *what* exists, *where* it is, *what it costs*, and *how to reach it*. There is no canonical, organized place for "every business in a category, in one place."

**Solution:** A two-sided platform:

- **Discovery side (user):** Browse/search every registered business by category (food, photobooth, café, salon, barber, etc.), location, price, and rating — via a powerful search, filters, and a **big interactive map**. View rich, owner-crafted business pages with full contact info and social media links.
- **Engagement economy:** Users **like, comment, recommend, review, and favorite** businesses. These signals drive **live trending scores** and **leaderboards** (global, per-category, per-location) so what's booming is visibly marked — while a fairness layer keeps **hidden gems** discoverable ("Rising" tier) instead of being eliminated by established names.
- **Storefront side (owner):** A universal dashboard where any business owner builds their own mini-website — branding, theme, products/services, hours, chat inbox — with zero coding, free, forever.

**Core value props:**

- *For users:* one place to find, compare (side-by-side), and contact any business — no app-switching, no social-media digging.
- *For owners:* a professional online presence with a product catalog and customer chat, without paying for a web developer.
- *For the platform:* free for everyone; growth driven by the directory itself being the best place to discover local businesses.

**Key product principles:**

1. **Free, always** — no paywalls on any core feature (monetization is a separate future decision, never a scope reduction).
2. **Category-agnostic** — any business type fits: product-based (food) and service-based (barbershop) equally.
3. **Owner-crafted** — every business page is unique, not a template with a logo swapped in.
4. **Engagement-driven but fair** — trending/leaderboards reward real activity; rising-tier visibility guarantees hidden gems are found.
5. **Performance & SEO** — a directory lives on Google; pages must load fast and be indexable.
6. **Moderation over censorship** — admin verifies registrations, but owners own their content.
7. **Golden standard, no MVP-tier** — every feature ships complete: full option sets, no "deferred" placeholders. Scope is defined by the spec, never by shortcuts.

---

## 2. Personas & Roles ✅

**R1 — Guest (unauthenticated user):** browses categories, searches, uses the map, compares businesses, and reads business pages. All discovery features work without an account. Converting guests → registered users is a platform KPI (engagement and chat require an account).

**R2 — Registered User:** everything a guest can do, plus: save businesses/products into **collections** (organized lists — "Wedding vendors", "Date night", default "Favorites"), **like, comment, recommend, and review** businesses and products, **full messaging** (user↔user, user↔business, business↔user — text, media, reply, edit, delete, reactions), receive notifications (in-app, Web Push, email), and manage their profile, security (2FA, sessions), and data (export, deletion).

**R3 — Business Owner:** a registered user with at least one business attached. Gains the Dashboard: full storefront customization, product/service management with **variants, stock, and multi-currency pricing**, full messaging inbox, engagement monitoring (likes/comments/recommends/reviews/helpful votes/collection saves), reply to reviews and comments, **verification document management**, and full analytics (views, engagement velocity, leaderboard position, funnel).

**R4 — Platform Admin (founder):** verification/approval of new business registrations **including document review (business registration/license, tax ID, identity) with badge-level assignment**, full category tree management, content moderation (products, reviews, comments, messages, reactions, attachments, reported content), user management (warn/suspend/ban), homepage curation (featured businesses), trending/leaderboard configuration, platform-wide analytics, site configuration.

**Role rules:**

- One user account can own **multiple businesses** (an owner with a café + a photobooth business).
- Roles are additive: Owner ⊃ User ⊃ Guest. Admin is exclusive.
- Role system must be extensible (future: moderators, partners) — enforced via permissions/claims, not hardcoded if-checks.

---

## 3. Core User Journeys ✅

### J1 — Discovery (guest)

1. Lands on homepage: prominent **search bar**, **category grid**, **"Trending Now" leaderboard panel**, **map widget with trending markers**, featured businesses.
2. Searches (free text) or picks a category → results page with **list + map toggle**, filters (**category, location/radius, price range, rating, open now**) and sorts (**trending, rating, newest, nearest**).
3. **Compare**: selects 2–4 businesses → side-by-side comparison of category, price level, rating, location, hours, and top products/services.
4. Opens a business **detail page**: hero images, owner branding, description, products/services, hours, location + map, **contact (phone, email, website) and social media links**, reviews, comments, like/recommend buttons, favorite, share.
5. Contacts the business via **chat** (or direct contact links). Guest is prompted to register to start a chat.

### J2 — Engagement loop (registered user)

1. Likes, recommends, comments (full nested threads), reviews (business + product), votes "helpful" on reviews, or saves a business/product into a **collection**.
2. Every action feeds the business's **trending score** (time-weighted); leaderboards and map markers update.
3. User discovers hidden gems via the **"Rising" tier** (velocity-based, see engagement model below).
4. User receives notifications: replies to comments, @mentions, review replies, message reactions, new messages.
5. User messages other users and businesses directly, with the full messaging feature set (§5.5).

### J3 — Comparison flow (guest or user)

1. From search results or a category page, toggles "Compare" on 2–4 businesses (visible as a persistent compare tray).
2. Opens the compare view: side-by-side table (category, price level, rating, distance, hours, contact, top products, service types).
3. Launches chat or contact directly from the compare view; clears tray when done.

### J4 — Owner lifecycle

1. Registers (or logs in) → **creates a business profile** (category, name, description, location, contact, socials) → uploads **verification documents** (business registration/license, tax ID, optional owner identity) → submits for **verification**.
2. Admin reviews info + documents → **Dashboard unlocks**; badge level assigned (Verified / Fully Verified).
3. Builds the **storefront**: theme, colors, logo, cover/hero, page sections, layout; uploads **products/services** (name, price, description, images, availability).
4. Publishes → business appears in directory, search, and map.
5. Engages: **chat inbox** with customers, replies to reviews/comments, monitors likes/recommends/favorites.
6. Analyzes: views over time, engagement velocity, top products, chat volume, leaderboard position, trending status.
7. Maintains: edits content anytime, pauses/hides the business (e.g. closed for holidays), or permanently closes it.

### J5 — Admin operations

1. Reviews **verification queue**: business info + uploaded documents (secure viewer, anti-fraud checklist); approves/rejects with a reason and assigns the badge level.
2. Handles **reported content** (products, reviews, comments, chats): take down, warn, or ignore.
3. Manages the **category tree** (create/edit/move categories, assign icons).
4. Curates **featured businesses** and homepage sections.
5. Manages users: warn, suspend, ban; reviews user reports.
6. Monitors platform analytics: registrations, activation rate, engagement volume, top categories, leaderboard health.

### Engagement & trending model (foundation for J2, feeds leaderboards/map)

| Signal | Relative weight | Notes |
|--------|-----------------|-------|
| Page view | 1 | Deduplicated per user per day |
| Save to collection | 3 | Bookmark into a user collection |
| Like | 5 | On business or product |
| Comment | 8 | Public comment |
| Recommend | 10 | Stronger than a like, shown as "Recommended by X" |
| Review | 12 | With rating; one per user per business |
| Chat initiated | 8 | First message per user per business per day |

- Scores are **time-decayed** (exponential decay) over rolling windows: 24h, 7d, 30d.
- **Leaderboards:** global, per-category, per-location; top N shown on homepage and category pages; map markers show trending businesses with a distinct "booming" style.
- **Fairness / hidden gems:** a velocity metric (decayed score ÷ baseline scaled by business age and prior score) ranks businesses in a **"Rising" tier** that receives guaranteed visibility slots — so new and small businesses surface instead of being eliminated by established names. Rising status is time-limited and continuously recomputed.

---

## 4. Goals & Non-Goals ✅

### Goals

| # | Goal |
|---|------|
| G1 | Any business, in any category, anywhere can register **free** and get a professional storefront in minutes. |
| G2 | Users can find, compare, and contact any business through a **complex discovery system**: full-text search, category tree, location/radius, price, rating, open-now, plus a big interactive map. |
| G3 | Engagement (likes, comments, recommends, reviews, collections, helpful votes, chat) drives **honest trending** and leaderboards; hidden gems get rising-tier exposure, never eliminated. |
| G4 | Owners fully control their page: branding, layout, content, products, chat. Zero code. |
| G5 | Admin (founder) controls quality: verification, moderation, curation, analytics. |
| G6 | Platform quality: fast, SEO-indexable, secure, responsive on all devices, English-first with i18n-ready architecture. |
| G7 | Built to last: real-world production quality, not a demo — full platform, everything detailed, all features delivered. |

### KPIs

- **Supply:** registered businesses, verification conversion, activation rate (publish within 48h of verification).
- **Demand:** DAU/MAU, search usage, search → detail → contact/chat conversion.
- **Engagement:** total signals, engagement per business, leaderboard freshness (how often top slots change).
- **Fairness:** share of Rising slots held by businesses younger than 30 days (target floor), share of top-100 leaderboard slots held by non-established businesses.
- **Quality:** p95 page load < 2s, uptime ≥ 99.5%, SEO indexation of ≥ 95% of public business pages.

### Out of scope (deliberate product boundaries — separate products, not scope reductions)

- Payments, checkout, orders, in-app purchases, delivery/logistics (the platform is discovery + communication; monetization is a separate future decision).
- Native mobile apps (responsive web + installable PWA covers mobile; app-store wrappers are packaging, not new features).
- AI-generated content or AI-generated ranking (ranking is deterministic and transparent; an AI layer can be added later as a separate system).
- Ads or promoted listings (violates the free principle).

---

## 5. Feature Specifications ⏳

### 5.1 Discovery & Search

#### 5.1.1 Homepage (guest & user)

- Sticky header: logo, global search bar, "Businesses" (directory), category dropdown, Map link, auth buttons; profile menu when logged in.
- Sections in fixed order:
  1. **Hero** — headline + large search input with category quick-picks and a "Near me" location prefill.
  2. **Category grid** — all top-level categories with icons and live business counts.
  3. **Trending Now** — global top-10 leaderboard panel (see engagement model §3).
  4. **Map widget** — bounded mini-map with trending/booming markers, links to full map (§5.2).
  5. **Rising** — hidden-gems strip (velocity-ranked, §3), with "What's this?" tooltip explaining fairness.
  6. **Featured** — admin-curated businesses (§5.8).
  7. Browse-all CTA.
- Footer: all categories, about, contact, admin link (admin-only route guard).

#### 5.1.2 Search & results page

- **Search backend:** full-text across business name, tagline, description, tags, category path, and product/service names. Typo tolerance, prefix matching, stopword handling; relevance ranking with popularity signals as tie-breakers.
- **Autocomplete:** debounced (250ms) as-you-type top-10 suggestions (business names + matching categories), keyboard-navigable, client-cached.
- **Filters:** category (multi-select, leaf categories expandable), location (text "city / address" or geolocation "Near me") with radius slider (1–50 km), price level (1–4), minimum rating (any / 3.5+ / 4+ / 4.5+), Open now toggle, **Verified only toggle (incl. "Fully verified only")**, has-chat toggle, currency-aware price display (converted to viewer's locale currency with source-rate annotation).
- **Sorts:** Trending (default), Top rated, Newest, Nearest, Relevance (text search only).
- **Results:** business cards — cover, logo, name, category, rating, price level, distance, open-now chip, Booming/Rising badges, **verification badge (Verified / Fully Verified)**. List ↔ Map toggle; map re-queries on current viewport bounds (debounced 400ms).
- **Pagination:** infinite scroll; result count; empty state with "Clear filters" + category suggestions; "No results in this area — Add your business" CTA.
- **Compare:** checkbox per card (max 4) → floating compare tray (§5.1.5).

#### 5.1.3 Category browsing

- Admin-managed category tree: top-level categories with icons, children under parents (§5.8).
- Category page: header (icon, name, count, description), per-category leaderboard (top 5 + view all), same search/filter engine pre-scoped to the category, breadcrumbs.
- Every category page is public and SEO-indexable.

#### 5.1.4 Map — see §5.2.

#### 5.1.5 Compare

- Compare tray persists across result pages (localStorage, synced to account when logged in). Max 4 businesses.
- Comparison view: businesses as columns; rows = photo, name, category, **verification level**, rating + review count, price level, **prices converted to one currency**, distance, hours + open-now, address, contact (phone/email), socials, top 3 products/services, feature chips (verified, chat available, Booming/Rising). Missing data renders "—".
- Per-column actions: Chat, View page, Remove. Compare URL is shareable (business IDs in query string).

#### 5.1.6 General rules

- All public pages server-rendered for SEO; auto-generated meta titles/descriptions from business/category data; Schema.org structured data (LocalBusiness, AggregateRating, Product) on detail pages.

### 5.2 Big Interactive Map

- **Tech:** free map stack (MapLibre GL + OSM tiles, no API keys); provider-agnostic tile interface so commercial tiles can be swapped without UI changes.
- **Surfaces:** standalone full-screen map page, homepage widget, embedded mini-map on business pages.
- **Markers:** category-colored pins; **Booming markers use a pulse/glow style**; Rising markers show an "up" badge. Tooltip: name, category, rating, distance.
- **Clustering:** auto-cluster at low zoom; click cluster → zoom in.
- **Layer filters:** category chips toggle marker layers on the fly.
- **Viewport search:** search/filters apply to current map bounds; re-query on move end.
- **Near me:** geolocation button centers map; radius slider filters distance.
- **Interaction:** marker click → mini card (name, rating, badges, distance, open-now, "View" button); mobile shows a bottom sheet. Click-through to detail page.
- **Directions & share:** mini card and detail page offer directions (OSM/Google links), share business link, and "Save to collection" for logged-in users.
- **Empty state:** "No businesses in this area yet" + owner registration CTA.

### 5.3 Business Detail Page

Public URL: `/b/<slug>` (auto-generated from name; owner can request one change).

#### 5.3.1 Hero & header

- Cover image gallery (carousel + lightbox), logo overlay, name, category link, **verification badge with level (Verified / Fully Verified — tooltip explains the difference)**, trending badges (Booming / Rising #rank), rating summary (score, count → jumps to reviews), price level.
- Action bar: **Favorite, Like, Recommend, Share** (copy link / native share), **Message** (chat). Guests see register prompts instead.

#### 5.3.2 About

- Owner description (rich text, limited formatting), tags, optional founded year, highlighted products (owner-pinned, max 3).

#### 5.3.3 Products & services

- Card grid: image, name, type chip (Product / Service), **starting price or "Call for price" (converted to viewer's currency)**, short description, per-product like/favorite, **per-product rating summary** if it has product reviews, "New"/"Popular" badges.
- Product modal: gallery, full description, **variant selector (options + per-variant price/availability/stock state)**, product reviews with rating breakdown, "Ask about this" → opens chat with pre-filled context, save to collection.
- Section hidden when empty.

#### 5.3.4 Hours & location

- Weekly hours table (per-day), open-now indicator computed in the business's timezone, timezone displayed.
- Address, embedded mini-map, full-map link, directions link.

#### 5.3.5 Contact & socials

- Owner-provided, optional, all `target=_blank`: phone (tel:), email (mailto:), website, WhatsApp, Instagram, TikTok, Facebook, X, YouTube, Line, Telegram. Show only what's provided.

#### 5.3.6 Engagement & community

- **Reviews:** rating breakdown bars; sort (newest / highest / most helpful); pagination; write review (registered only, rating 1–5 + text, one per user per business, editable, deletable); **helpful votes**; owner replies; report button (§5.6).
- **Product reviews:** the same model scoped per product (one per user per product, rating + text, helpful votes, owner replies).
- **Comments:** public feed (max 500 chars), **full nested threading (any depth; deep threads collapse)**, **comment likes**, **@mentions with notifications**, timestamps, avatars, edit within 10 min, report button. Part of the engagement model (§3 weights).
- **Similar businesses:** same leaf category or nearest 6 (same city), card strip.

#### 5.3.7 SEO

- Meta + OG/Twitter cards (cover image), canonical URL, Schema.org LocalBusiness + AggregateRating, sitemap covering all public pages.

### 5.4 Owner Dashboard & Storefront Builder

Route `/dashboard` (owner role). Shell: sidebar (Overview, Storefront, Products, Reviews & Chats, Analytics, Settings) + business switcher for multi-business owners, "View page" and "Preview" quick links.

#### 5.4.1 Business creation wizard

- Steps: (1) Info — name, category (hierarchical picker), tagline, description → (2) Location — address text + map pin or coordinates; city/country derived → (3) Contact & socials — phone, email, website, socials → (4) Hours — weekly template → (5) **Verification documents** — business registration/license, tax ID, optional owner identity (rules in §8.2) → (6) Review & submit.
- Per-step validation; drafts auto-saved and resumable.
- On submit: status `pending_review`; owner notified of approve/reject (with reason + resubmit flow) and of the assigned **verification level** (Verified / Fully Verified).

#### 5.4.2 Storefront builder

- **Design:** 5+ preset themes + a **full custom theme editor** (CSS-variable-driven: colors, gradients, radius, spacing, shadows, dark mode toggle); primary/accent/secondary pickers with contrast validation; 5+ font pairings with preview; logo upload (SVG/PNG, transparency supported); cover upload with overlay gradient and focal-point picker.
- **Layout editor:** ordered, toggleable, reorderable sections — Hero, About, Highlights (3–4 icon feature cards), Products, Gallery, Hours, Contact.
- **Live preview:** desktop/mobile toggle, instant updates; "Preview as guest" opens the real public URL in draft mode.
- **Publish model:** auto-saved drafts; explicit Publish / Unpublish. Public page always reflects the last published state; drafts never public.

#### 5.4.3 Products & services

- CRUD fields: name (required), type (product | service), short description + rich details, **currency (defaults to business currency)**, base price (optional; "Call for price" flag for services), images (up to 10, drag-reordered, first = cover; jpg/png/webp, ≤ 5MB, auto-resized variants), tags (max 5), availability toggle, featured toggle (max 8), manual sort order, SEO title/description.
- **Variants & options (full):** option groups (e.g. Size: S/M/L, Color: Red/Blue), unlimited groups × values; each variant (combination) has its own SKU, price, currency, image set, stock quantity or availability, and optional "Call for price". At least one variant required when option groups exist; base price applies to option-less products.
- **Stock & availability:** per-variant stock quantity with low-stock flag, or boolean availability for services; out-of-stock variants stay visible with "Out of stock" state, never hidden.
- **Badges:** optional "New" / "Popular" flags (display-only, no rank impact).
- **Product reviews:** owners see product review counts and reply to them like business reviews (§5.6).
- Bulk actions: delete selected, duplicate (duplicate copies variants too).

#### 5.4.4 Business settings

- Info, category (change triggers re-verification), location, hours, contact, socials, tags.
- Danger zone: **Pause** (hidden from search/map; page shows "Temporarily closed"; chats preserved) and **Close permanently** (irreversible; data archived per retention; chats kept for both sides' records).
- Verification status always visible, with resubmit flow after rejection.

#### 5.4.5 Owner analytics

- Period selector (7d / 30d / all): unique page views (time series), engagement counts with deltas (likes, recommends, comments, reviews, helpful votes, collection saves), chat volume (threads, messages, response time), top 5 products by views with variant breakdown, trending score, leaderboard position (global + category), Booming/Rising status, **search-to-visit-to-chat funnel**.

#### 5.4.6 Owner notifications

- In-app notification center: new review, new comment, new chat message, verification result, document re-request, moderation outcomes. Unread badges on dashboard sidebar and chat; push/email per the §5.7 channel matrix.

### 5.5 Messaging System (full)

Real-time messaging between any registered users (**user↔user**) and between users and businesses (**user↔business**; all owners/co-owners of the business share the business side). First message requires a registered account (guest → register prompt, draft preserved and resumed after signup).

#### 5.5.1 Threads & routing

- Thread types: `direct` (two users) and `business` (one user + one business; owners share the business side via the dashboard business switcher, §5.9.3 co-owners included).
- Entries: "Message" on business page, product modal ("Ask about this"), **user profile page**, compare view.
- Unified inbox across all threads; business threads additionally filterable by business.
- Thread metadata: counterpart (business or user) name/avatar, last message preview, unread count, last activity, **pinned threads (max 5 per participant)**, mute state.

#### 5.5.2 Message features (full set)

- **Types:** text (≤ 4000 chars), image (≤ 10MB, auto thumbnails), **file** (PDF, docs, spreadsheets ≤ 25MB, safe-mime allowlist), **voice note** (recorded in-app, ≤ 5 min), **video** (≤ 200MB, transcoded to playable mp4 + poster via FFmpeg pipeline), **link** (server-fetched preview: title/description/image, SSRF-safe, cached 24h), **system events** (closed, deleted, membership changes).
- **Reply:** quote any message; shows snippet, tap jumps to the original.
- **Edit:** sender only, any time; edit history stored and visible ("Edited" → shows versions).
- **Delete:** delete for me (any time) or **delete for everyone** (≤ 15 min after send, or anytime if the other side never read it); tombstones shown; deletion audited; the other participant gets a system notice.
- **Forward:** forward to another thread/contact with a "Forwarded" badge; cannot forward content from blocked users.
- **Reactions:** full emoji picker, 1 active reaction per user per message, counts per emoji, real-time.
- **Read receipts:** delivered (server ack) + read (timestamp, per-participant last-read); per-thread unread counts.
- **Typing indicators:** per-thread, 3-second expiry.
- **Search in chat:** full-text within the thread, context snippets, jump-to-message.
- **Media gallery:** grid of all shared media in the thread; tap to view/download.
- **Pinning:** participants pin messages (max 5); pinned panel at thread top.
- **Export chat:** per-thread JSON export (all messages + metadata) — part of user data export.
- **Quick replies (business side only):** up to 20 canned replies per business, insertable with one tap, searchable.
- **Close/resolve (business side):** owner closes a thread ("resolved"); closed threads show a notice; any new message reopens it.

#### 5.5.3 Real-time transport

- WebSocket with reconnect + exponential backoff; client **outbox pattern** (IndexedDB); at-least-once with `client_msg_id` dedupe.
- Multi-instance: Redis pub/sub fan-out; per-connection presence for typing/read state.
- Push: **Web Push (VAPID)** for installed/PWA users; in-app unread badges always; optional per-thread email (throttled, §5.7).
- Offline behavior: sent messages queue locally, sync on reconnect, statuses flow pending → delivered → read.

#### 5.5.4 Safety & moderation

- Report message / thread / attachment (auto-includes last 10 messages as evidence) → admin queue.
- **Block/unblock users:** blocked party cannot send in any thread; history preserved; block list manageable.
- Leave a thread (user side): removes from inbox; other side unaffected.
- Banned-word filtering on send (hint shown, no silent drop); 3 consecutive rejections → 5-min send cooldown.
- Attachment scanning: magic-byte validation, size caps, **ClamAV virus scan**; infected uploads rejected.
- Spam heuristics: identical-message bursts across > 5 threads in 10 min → anomaly flag + temporary send block (§5.5.2 rate limits).

### 5.6 Engagement, Reviews & Comments Engine

#### 5.6.1 Engagement actions

| Action | Actor | Target | Rules |
|--------|-------|--------|-------|
| Like | user | business, product | toggleable; one active per target; feeds score §3 |
| Recommend | user | business | toggleable; shows "Recommended by N people" on page; feeds score §3 |
| Save to collection | user | business, product | adds to a user collection (default "Favorites" auto-created); feeds score §3 |
| Comment | user | business | max 500 chars, full nested threading, comment likes, @mentions; editable within 10 min; feeds score §3 |
| Review | user | business, product | rating 1–5 + text (min 10 chars, max 2000); one per user per target; editable/deletable any time; feeds score §3 |
| Helpful vote | user | review | up/down, one per user per review; powers "most helpful" sort; feeds no score (keeps the model honest) |

- All actions require registration; guests see prompts with return URL.
- Actions are idempotent; optimistic UI with server reconciliation.
- Users can undo any action. Counters shown on business page (likes, recommends, collection saves are public counts; individual collection membership is private by default).

#### 5.6.2 Reviews

- Display: rating distribution bars (5→1), average score (1 decimal), total count, latest reviews with pagination (20/page), sort newest / highest / most helpful.
- Review card: user (name + avatar, **links to public profile**), rating stars, text, date, edit history indicator, owner reply (one, editable, deletable, shown below), **helpful votes (up/down, one per user, toggleable)**, report.
- Product reviews: identical model scoped to products (one per user per product); product rating aggregates into the product card and detail modal.
- Abuse: admin can hide a review (visible to author only, "removed" placeholder for others), warn author; review deletion by user is hard delete.

#### 5.6.3 Trending & leaderboard engine (implementation spec)

- Score recomputation: every 10 minutes via batch job (debounced on engagement events).
- Per-business score = Σ(signal weight × decay(t)) where decay(t) = e^(−λ·age_hours), λ per window (24h/7d/30d config).
- Leaderboards: global top 100, per leaf category top 50, per city top 50 (computed materialized views).
- **Booming:** top N by 24h velocity (score delta), recalculated hourly; badge + map pulse marker.
- **Rising (hidden gems):** velocity normalized by baseline (business age + trailing 30d score, smoothed); top 20 youngest-by-velocity businesses; guaranteed homepage strip slot + top-3 placement inside one category leaderboard view; eligibility resets weekly.
- Anti-gaming: dedupe views per user/day; ignore actions from suspended accounts; detect burst anomalies (flag for admin review); cap same-user action contribution to a business score per day.

### 5.7 Notifications

- **Types:** new message, message reaction, reply to my comment, @mention, review posted on my business, review replied to, helpful vote on my review, product review, verification result (with level), document re-request, moderation outcome (report/warning), business suspended/restored, system announcements.
- **Channels:** in-app center (bell, unread count, paginated, mark-all-read, filter by type); real-time WS while online; **Web Push (VAPID) for PWA users**; email — per-type opt-in (chat: immediate but throttled to 1 per thread per 5 min; others: immediate), plus optional weekly digest.
- **Settings:** per-type × per-channel toggle matrix (in-app on/off, push on/off, email on/off/digest-only) + **quiet hours** (e.g. 22:00–08:00: push/email deferred, in-app unaffected).
- Retention: in-app 90 days for users, 180 days for owners; admin notifications 1 year.

### 5.8 Admin Panel

Route `/admin` (admin role only; separate layout). Sections:

#### 5.8.1 Verification queue
- Pending businesses: full profile review (all fields + images) + **document review** — uploaded business registration/license, tax ID, identity documents viewed in a secure viewer (watermarked, access-logged); anti-fraud checklist (name match, document validity, address plausibility, expiry check).
- Approve with a **verification level**: Verified (info verified) or Fully Verified (info + documents verified); or reject with required reason + optional document re-request; auto-email owner; resubmit flow.
- Rejected / re-requested lists (with reasons + timestamps).
- Document handling: encrypted at rest, never served publicly, auto-redacted previews, retention per §9.3, full audit trail of who viewed what and when.

#### 5.8.2 Moderation
- Queues: reported reviews, comments, messages/threads, **reactions, chat attachments**, products, businesses, users. Each item: context view (target page/thread), evidence panel, actions: dismiss / warn author (message) / hide content / **delete for everyone (messages)** / suspend user.
- Admin can also directly hide/restore any review, comment, or product with a visible "removed by moderator" state.
- Action log: every moderation action recorded (who, what, when, reason) — immutable audit trail.

#### 5.8.3 Categories
- CRUD on the full tree: create top-level, child, icon, slug, description; reorder; merge; counts shown.
- Deleting a category requires reassignment of businesses (bulk move) — no orphaned businesses.

#### 5.8.4 User management
- Search users (name/email); view profile (businesses, reviews, reports, bans history).
- Warn (message + record), suspend (X days, optional reason), ban (permanent). Suspended/baned users: cannot login/engage; content stays visible with author shown as "user".

#### 5.8.5 Curation & config
- Featured businesses (pick + ordering), homepage Rising strip control (auto/paused), leaderboard config (weights, windows, N sizes), site-wide settings (site name, contact email, announcement banner).

#### 5.8.6 Analytics (admin)
- Platform KPIs (§4): registrations, business activations, engagement volume by type, top categories/cities, leaderboard turnover, conversion funnel (search → detail → chat), system health (errors, WS connections, job latencies).

### 5.9 Accounts, Auth & Roles

#### 5.9.1 Registration & login
- Email + password (argon2id), email verification (required before engaging: chat/reviews/comments/likes/collections/business creation), password reset (secure token, 15 min expiry, single-use).
- OAuth: Google, Facebook, Apple, GitHub (OpenID Connect; first-time OAuth accounts auto-verified).
- **2FA (TOTP):** optional enrollment, 10 single-use recovery codes (regenerable); re-challenge on new device/session; **2FA required for admin accounts**.
- Sessions: JWT access (15 min) + rotating refresh token (30 days), httpOnly+Secure+SameSite cookies; **server-side session registry** → per-session revocation, "revoke all other sessions" on password change / 2FA change / ban; new-device login alert email.
- Rate limiting on auth endpoints (5/min per IP; 2FA challenge: 3 attempts then 15-min lockout).

#### 5.9.2 Profiles
- Display: name, avatar (upload or OAuth), bio (max 300 chars), timezone, **profile links (personal website, socials)**; public profile page `/u/:username` shows reviews, comments, public collections, owned businesses.
- Privacy: name + avatar public when engaging; email never public; collections private by default (owner can publish); block list enforced across messaging.
- Security page (`/me/security`): 2FA enrollment, active sessions list with per-session revoke, recovery codes management, login history.
- Data: full export (JSON: profile, businesses, products, reviews, comments, chats, collections) on demand; delete account with 14-day grace (restorable); final deletion anonymizes public content and removes private data.

#### 5.9.3 Roles & permissions
- Roles: guest (implicit) ⊂ user ⊂ owner (has ≥1 verified business) ⊂ admin. Owner status is derived from owned businesses, not a flag.
- Permission checks in one middleware: claim-based (`business.manage`, `content.moderate`, `admin.*`); admin panel routes check `admin` claim; future roles (moderator, partner) add claims without schema change.
- Business ownership: N businesses per user; **multi-owner per business** — invite co-owners by email (accept/reject; roles: co-owner = full manage, viewer = analytics only); ownership changes audited.

#### 5.9.4 Security baseline
- All endpoints authenticated where required; public read-only endpoints; output encoding/XSS-safe React; CORS locked to frontend origin; file uploads validated by magic bytes + size caps + **ClamAV virus scan** (files + attachments), stored with random names on a separate origin; audit log for auth-sensitive events (login, password change, 2FA change, ban, session revoke) **and for verification-document views**; verification documents encrypted at rest with restricted access.

---

## 6. Information Architecture ⏳

Every route, who can see it, what it contains. Naming: `/b/:slug` = business page, `/c/:slug` = category page.

### 6.1 Public routes (guest + logged-in)

| Route | Page | Key content |
|-------|------|-------------|
| `/` | Homepage | Hero + search, category grid, Trending Now (top 10), map widget, Rising strip, Featured, footer with all categories |
| `/search` | Search results | Query + filters + sorts, list/map toggle, compare checkboxes, infinite scroll |
| `/map` | Full-screen map | All filters, viewport search, "Near me", layer chips, booming pulse markers, bottom sheet (mobile) |
| `/categories` | Directory | Full category tree with counts, search within |
| `/c/:slug` | Category page | Header (icon/name/count), category leaderboard, scoped search/filter results, breadcrumbs |
| `/b/:slug` | Business page | Hero+actions, about, products, hours, contact & socials, reviews, comments, similar businesses |
| `/u/:username` | Public user profile | Reviews, comments, public collections, owned businesses |
| `/compare?b=a,b,c,d` | Comparison | Side-by-side columns (§5.1.5); shareable via query params |

### 6.2 Auth routes (guest only — redirect if logged in)

| Route | Page |
|-------|------|
| `/login` | Email/password or OAuth; redirect back to original URL (`?next=`) |
| `/register` | Account creation + OAuth |
| `/forgot-password` | Request reset |
| `/reset-password?token=` | Set new password |
| `/verify-email?token=` | Email verification landing |

### 6.3 User routes (registered user)

| Route | Page |
|-------|------|
| `/me` | Profile: name, avatar, bio, links, timezone, account settings |
| `/me/collections` | Collections list (create/edit/delete, privacy toggle) |
| `/me/collections/:id` | Collection detail: businesses/products with notes + manual sort |
| `/me/reviews` | My reviews + helpful votes (edit/delete) |
| `/me/messages` | Inbox: all threads (direct + business), search, filters |
| `/me/messages/:threadId` | Conversation view (full §5.5 feature set) |
| `/me/security` | 2FA enrollment, recovery codes, active sessions + revoke, login history |
| `/me/notifications` | Notification settings matrix (§5.7) + quiet hours |
| `/me/export` | Data export requests + downloads |

### 6.4 Owner routes (owner claim required)

All under `/dashboard` shell (sidebar + business switcher):

| Route | Page |
|-------|------|
| `/dashboard` | Overview: engagement summary, alerts, quick links |
| `/dashboard/register` | Business creation wizard (§5.4.1) |
| `/dashboard/storefront` | Layout & design editor with live preview (§5.4.2) |
| `/dashboard/products` | Product/service CRUD, bulk actions (§5.4.3) |
| `/dashboard/chats` | Unified inbox across businesses (filtered by switcher) |
| `/dashboard/chats/:threadId` | Thread view with quick replies (§5.5) |
| `/dashboard/reviews` | Reviews for my businesses + reply management |
| `/dashboard/comments` | Comments + replies on my businesses |
| `/dashboard/analytics` | Period analytics, trending status (§5.4.5) |
| `/dashboard/settings` | Business info, hours, location, contact, socials, danger zone |
| `/dashboard/settings/verification` | Status, verification level, documents, resubmit flow |

### 6.5 Admin routes (admin claim required)

| Route | Page |
|-------|------|
| `/admin` | KPI dashboard, system health |
| `/admin/verify` | Verification queue: info + document review, level assignment, rejected list |
| `/admin/moderation` | Reports queue (reviews/comments/messages/products/businesses) |
| `/admin/moderation/:reportId` | Context view + actions + audit trail |
| `/admin/categories` | Category tree CRUD, merge, bulk reassign |
| `/admin/users` | Search, profile drill-down, warn/suspend/ban |
| `/admin/curation` | Featured picker, Rising strip control, leaderboard config |
| `/admin/settings` | Site config, announcement banner |
| `/admin/analytics` | Platform KPIs (§5.8.6) |

### 6.6 Routing rules

- `/b/:slug` is canonical; business page always resolves via slug (unique, immutable after creation).
- `/u/:username` is unique and editable once (reserved usernames list enforced).
- SPA routing with lazy-loaded chunks; every public page pre-rendered (SSR/prerender) for SEO.
- Unknown category/business slug → 404 with related suggestions ("Did you mean...?").
- Owner/admin routes: guard middleware redirects to `/login?next=...`; wrong role → 403 page.
- Compare tray is global UI, present on search/category/compare routes only.

---

## 7. Data Model ⏳

Conventions: `id` UUIDv7 PK everywhere; `created_at`/`updated_at` on all tables; soft-delete via `deleted_at` where noted; enums as PostgreSQL native enums; JSONB for flexible blocks (theme, layout, contact, hours).

### 7.1 Core entities

**users**
`id`, `email` (unique, citext), `password_hash` (nullable for OAuth-only), `name`, `avatar_url`, `bio`, `timezone`, `email_verified_at`, `role` enum (user|admin; owner is derived, not stored), `status` enum (active|suspended|banned), `suspended_until`, `ban_reason`, `deleted_at`, timestamps. Indexes: email, status.

**businesses**
`id`, `owner_id` FK → users, `name`, `slug` (unique), `tagline`, `description` (rich text), `category_id` FK → categories, `status` enum (draft|pending_review|verified|rejected|suspended|paused|closed), `rejection_reason`, `logo_url`, `cover_url`, `gallery` JSONB (array of media ids, ordered), `price_level` smallint 1–4, `currency` char(3) (owner-set, default USD), `address`, `lat`, `lng`, `city`, `country`, `hours` JSONB (per-day open/close + flags), `contact` JSONB (phone, email, website, whatsapp, socials map), `tags` text[], `founded_year`, `is_featured`, `featured_order`, `theme` JSONB (template, colors, fonts), `layout` JSONB (ordered sections + per-section config), `highlighted_product_ids` uuid[], `last_published_at`, `published_snapshot` JSONB (published layout/theme, separate from draft), `verified_at`, `verification_level` enum (verified|fully_verified) nullable, `deleted_at`, timestamps. Indexes: slug, category_id, status, (lat,lng) gist, city, is_featured.

**categories**
`id`, `parent_id` FK → categories (null = top-level), `name`, `slug` (unique), `icon` (key into icon set), `description`, `sort_order`, timestamps. Unique (parent_id, name). Recursive CTE for tree queries.

**products**
`id`, `business_id` FK, `type` enum (product|service), `name`, `description`, `currency` char(3), `base_price` numeric nullable (option-less products), `call_for_price` bool, `cover_image_id`, `image_ids` uuid[], `tags` text[] (max 5), `is_available`, `is_featured`, `featured_order`, `sort_order`, `is_published` (draft vs live), `badge` enum (none|new|popular), `seo_title`, `deleted_at`, timestamps. Indexes: business_id, (business_id, is_published).

**product_options** — `id`, `product_id` FK, `name` (e.g. Size), `values` text[] (e.g. S/M/L), `sort_order`. Max 3 groups per product.

**product_variants** — `id`, `product_id` FK, `name` (composed or custom), `sku` (unique per business), `options` JSONB (option name → value), `price` numeric nullable, `currency` char(3), `stock_qty` int nullable, `in_stock` bool, `image_id`, `sort_order`. Unique (product_id, options). Max 200 variants per product.

### 7.2 Engagement entities

**reviews**
`id`, `business_id` FK, `product_id` FK → products nullable (null = business review), `user_id`, `rating` smallint 1–5, `text` (10–2000 chars), `reply` text nullable, `reply_at`, `reply_edited_at`, `status` enum (visible|hidden), `hidden_by`, `hidden_reason`, `edited_at`, `deleted_at`, timestamps. Unique (business_id, user_id) where product_id null; Unique (product_id, user_id) where product_id not null. Indexes: (business_id, status), (product_id, status), (user_id).

**review_helpful_votes** — `id`, `review_id` FK, `user_id`, `vote` smallint (1 up, -1 down), `created_at`. Unique (review_id, user_id).

**comments**
`id`, `business_id`, `user_id`, `parent_id` FK → comments (nested, any depth), `text` (≤500), `status` (visible|hidden), timestamps. Indexes: (business_id, parent_id), (business_id, created_at).

**comment_likes** — `id`, `comment_id` FK, `user_id`, `created_at`. Unique (comment_id, user_id).

**likes** — `id`, `user_id`, `target_type` enum (business|product), `target_id`, `created_at`. Unique (user_id, target_type, target_id). (Recommend/favorite share this shape but need distinct meaning → separate tables:)

**recommends** — `id`, `user_id`, `business_id`, `created_at`. Unique (user_id, business_id).

**collections** — `id`, `user_id`, `name` (≤50), `slug` (unique per user), `description`, `is_public` bool (default false), `cover_item_id`, `sort_order`, `is_default` bool (first collection "Favorites" auto-created), `deleted_at`, timestamps. Indexes: user_id. Max 50 collections per user.

**collection_items** — `id`, `collection_id` FK, `target_type` enum (business|product), `target_id`, `note` (≤200), `sort_order`, `created_at`. Unique (collection_id, target_type, target_id). Max 500 items per collection.

**engagement_events** (append-only, feeds trending)
`id`, `user_id`, `target_type` (business|product), `target_id`, `signal` enum (view|like|recommend|comment|review|collection_save|chat_start), `weight` int, `occurred_at`, `dedupe_key` (unique — user+target+signal+day; views deduped per user/day). Indexes: (occurred_at), (target_type, target_id).

### 7.3 Chat entities

**chat_threads** — `id`, `type` enum (direct|business), `business_id` FK nullable, `status` enum (open|closed|left), `closed_by`, `closed_at`, `last_message_at`, `created_at`. Indexes: business_id, last_message_at. (Direct: per user pair; business: per user+business — enforced via participants + type.)

**chat_participants** — `id`, `thread_id` FK, `user_id`, `role` enum (user|owner|admin), `last_read_message_id`, `muted_until` nullable, `pinned_message_ids` uuid[], `notification_prefs` JSONB (email on/off/throttled), `left_at`, `created_at`. Unique (thread_id, user_id). Indexes: (user_id, last_read_message_id).

**chat_messages** — `id`, `thread_id` FK, `sender_id` FK → users, `sender_role` enum (user|owner|admin), `type` enum (text|image|file|audio|video|link|system), `body` (≤4000; system messages carry structured payload), `reply_to_id` FK → chat_messages nullable, `forwarded_from_message_id` nullable, `media_id` nullable, `link_preview` JSONB nullable (url, title, description, image), `client_msg_id` (unique per thread), `read_count`, `edited_at`, `edit_history` JSONB (array of {text, at}), `deleted_for` enum (none|me|everyone), `deleted_at`, `created_at`. Indexes: (thread_id, id DESC), (thread_id, read_at) partial, tsvector GIN for thread search.

**message_reactions** — `id`, `message_id` FK, `user_id`, `emoji`, `created_at`. Unique (message_id, user_id) — one active reaction per user per message.

**chat_attachments** — `id`, `message_id` FK, `media_id`, `kind` enum (image|file|audio|video), `width`, `height`, `duration_ms`, `created_at`.

**quick_replies** — `id`, `business_id`, `text` (≤500), `sort_order`, `created_at`. Max 20 per business.

**blocks** — `id`, `blocker_id`, `blocked_id`, `created_at`. Unique (blocker_id, blocked_id).

### 7.4 Platform entities

**notifications** — `id`, `user_id`, `type` enum (§5.7), `payload` JSONB, `is_read`, `channel` (in_app|email), `created_at`, `expires_at` (retention per §5.7). Indexes: (user_id, is_read, created_at).

**reports** — `id`, `reporter_id`, `target_type` enum (review|comment|message|product|business|user), `target_id`, `reason`, `evidence` JSONB (e.g. last 10 messages), `status` enum (open|resolved|dismissed), `resolved_by`, `resolved_at`, timestamps.

**moderation_actions** — `id`, `admin_id`, `action` enum (hide|restore|warn|suspend|ban|unban|approve|reject), `target_type`, `target_id`, `reason`, `payload` JSONB, `created_at`. Append-only audit trail. Indexes: (target_type, target_id).

**media** — `id`, `uploader_id`, `kind` enum (logo|cover|gallery|product|avatar|chat_image|chat_file|chat_audio|chat_video|document_verification), `original_name`, `mime` (validated by magic bytes), `size`, `width`, `height`, `duration_ms`, `path` (random-named, CDN-ready), `variants` JSONB (images: thumb/medium/large WebP/AVIF; audio: mp3/m4a; video: mp4 + poster frame), `virus_scan_status` enum (pending|clean|infected|error), `created_at`. No delete cascade — content refs preserved.

**trend_snapshots** — `id`, `window` enum (24h|7d|30d), `business_id`, `score` numeric, `velocity` numeric, `rank_global`, `rank_category`, `rank_city`, `is_booming`, `is_rising`, `taken_at`. Indexes: (window, taken_at, rank_global). Materialized snapshot per recompute job (§5.6.3); enables leaderboard history.

**sessions** (refresh-token registry) — `id`, `user_id`, `token_hash` (sha256), `ip`, `user_agent`, `created_at`, `last_seen_at`, `revoked_at`. Indexes: user_id, token_hash.

**user_2fa** — `id`, `user_id` unique, `totp_secret_encrypted`, `enabled_at`, `recovery_codes_hash` JSONB (10 hashed, single-use), `last_used_at`.

**push_subscriptions** — `id`, `user_id`, `endpoint`, `keys` JSONB, `user_agent`, `created_at`, `last_seen_at`. Unique endpoint.

**currency_rates** — `id`, `code` char(3) unique, `rate_usd` numeric, `fetched_at`. Refreshed hourly from the rate feed (§10.1).

**verification_documents** — `id`, `business_id` FK, `kind` enum (registration|license|tax_id|identity|utility), `media_id` FK (kind document_verification), `status` enum (pending|approved|rejected|expired), `reviewer_id`, `review_note`, `reviewed_at`, `expires_at`, `created_at`. Indexes: business_id, status.

**verification_document_views** — `id`, `document_id` FK, `admin_id`, `created_at`. Append-only audit: who viewed which document when.

**banned_words** — `id`, `word`, `created_at` (admin-managed list for send-time filtering).

**auth_events** (audit) — `id`, `user_id`, `event` enum (login|login_fail|register|password_reset|logout|ban|suspend|unban|2fa_enable|2fa_disable|session_revoke|document_view), `ip`, `user_agent`, `created_at`.

**business_invites** — `id`, `business_id`, `email`, `role` enum (co_owner|viewer), `token`, `invited_by`, `accepted_at`, `expires_at` (7 days), `revoked_at`.

### 7.5 Relationships summary

```
users 1—N businesses (owner; N—M via business_invites)
businesses N—1 categories
businesses 1—N products, products 1—N options/variants
users N—M businesses (collections, recommends)
users N—M businesses/products (reviews 1:1 per target, threaded comments, likes)
threads N—M participants (direct: 2 users; business: user + business owners)
messages 1—N reactions; messages self-reference (reply_to, forwarded_from)
users N—M users via direct threads + blocks
engagement_events references users + businesses/products (append-only)
reports/moderation_actions polymorphic target_type + target_id
trend_snapshots derived from engagement_events (batch job)
verification_documents N—1 businesses; document views audited
```

### 7.6 Concurrency & integrity notes

- Unique constraints enforce the core invariants (one review per user+business or user+product, one direct thread per user pair, one business thread per user+business via participants, dedupe keys).
- All writes for a single aggregate (e.g. publish snapshot) run in a transaction.
- `published_snapshot` on businesses = published state; drafts live in `theme`/`layout` columns — publish = copy draft into snapshot atomically.
- Money stored as (amount decimal(12,2), currency char(3)); conversions applied at read time from `currency_rates`; stale rates (> 24h) render the original price with a staleness hint.
- Geo queries via PostGIS (or lat/lng + earthdistance if PostGIS is unavailable); PostGIS preferred for radius queries at scale.

---

## 8. Business Rules & Validation ⏳

Single source of truth for every rule the backend enforces. Unless noted, violations return 400 with a field-level error map.

### 8.1 Accounts

- Email: valid format, unique (citext, case-insensitive); password ≥ 8 chars with at least one letter and one number; argon2id hashing, unique salt per user.
- **2FA:** TOTP optional, required for admin accounts; 10 single-use recovery codes (regenerating invalidates previous); 3 failed 2FA attempts → 15-min challenge lockout.
- Suspended user: cannot log in, engage, or chat (error: "Account temporarily suspended until <date>"). Banned: permanent, no login, content remains visible with author shown as "user".
- **Sessions:** registry-backed; password/2FA change or ban revokes all sessions except the current one; new-device login triggers an alert email.
- One account per email; OAuth accounts auto-verified; email accounts require verification before: chat, reviews, comments, likes, collections, business creation.

### 8.2 Businesses

- **Slug:** generated from name (lowercase, ascii, hyphens), uniqueness enforced; duplicate → append `-2`, `-3`; immutable after creation.
- **Verification requirements (all mandatory):** name (2–80 chars), category (leaf category required; one category per business), description (min 50 chars), valid address with lat/lng (geocoded or pinned on map), at least one contact method (phone, email, or website), logo, hours defined for at least 5 days, no banned words in any text field.
- **Verification levels:** `Verified` (all info verified by admin) and `Fully Verified` (info + documents verified). Documents: business registration or license (required for Fully Verified), tax ID (recommended), optional owner identity. Document rules: clear scan/photo, no cropping, expiry < 90 days rejected, max 10MB each, PDF/JPG/PNG.
- **Status transitions:** draft → pending_review → verified → {suspended, paused, closed}. Rejected stays pending-eligible via resubmit (counts reset; max 3 resubmission attempts, then manual support). Paused: hidden from search/map/leaderboards; page shows "Temporarily closed". Closed: permanently hidden; page 410/Gone.
- Suspended (admin): hidden from search and map, page shows suspended notice; owner can appeal via the support flow (re-open request with reason, admin reviews).
- Owner can request **one slug change** and **category change requires re-verification**.
- **Price level:** 1–4, enforced; 0 = unset allowed pre-publish.

### 8.3 Products

- Name required (2–100 chars); currency required (defaults to business currency); price ≥ 0 (2 decimals, stored with currency).
- Options: max 3 option groups, max 20 values each; variants = combinations (max 200 per product); each variant: unique SKU, own price, own stock, optional own image.
- "Call for price" only valid when type = service or variant price unset; at least 1 image required to publish; featured products max 8; out-of-stock variants remain visible with state.
- Product can be drafted (unpublished) independently of business publish state.
- Banned words checked on name, description, tags, variant names.

### 8.4 Engagement

- Review: one per (user, business) and one per (user, product); rating integer 1–5; text 10–2000 chars; edit any time (edits recorded); delete is hard delete + audit event.
- Helpful vote: one per (user, review), up/down toggle; cannot vote your own review.
- Comment: ≤ 500 chars; nested replies at any depth (display collapses past depth 3); edit window 10 minutes (history kept); comment likes: one per (user, comment); @mentions resolve to registered users (notification fired).
- Like/recommend/save-to-collection: idempotent toggles; owner's own actions on their business are excluded from the trending score.
- Report: one open report per (reporter, target); target must exist; evidence auto-attached for messages/attachments.

### 8.5 Chat

- Thread auto-created on first message; owner reply reopens closed threads; leaving a thread (user) hides it from the inbox but messages remain in export.
- Message ≤ 4000 chars; rate limit 1 msg/sec, 60/hour per user per thread; identical-message burst across > 5 threads in 10 min → anomaly flag + temporary send block.
- Edit: sender only, any time, edit history retained. Delete for me: any time. Delete for everyone: ≤ 15 min after send, or anytime if never read by the other side; audited; other participant sees a system notice.
- Reactions: 1 active per user per message (any emoji); reaction spam (> 20 in 5 min) rate-limited.
- Forward: copies a message to another thread with a "Forwarded" badge; content from blocked users cannot be forwarded.
- Banned-word filtering on send with hint (no silent drop); 3 consecutive rejections → 5-min cooldown.
- Block: blocked party cannot send in any thread; existing threads show "unavailable"; unblock restores.
- Files: allowlist (pdf, docx, xlsx, pptx, txt, csv) ≤ 25MB; images ≤ 10MB; audio ≤ 5 min; video ≤ 200MB; all scanned (ClamAV); infected uploads rejected.
- Deleting your account: threads and messages from that user are anonymized (sender shown as "user"), not deleted (business records preserved).

### 8.6 Trending & leaderboards

- Only **verified** businesses participate; suspended/paused/closed excluded.
- Score = Σ(weight × e^(−λ·age)); λ per window (24h: 0.03, 7d: 0.006, 30d: 0.002 — tunable in admin).
- Booming: top 25 by 24h velocity; Rising: top 20 by normalized velocity (baseline = 5 + trailing 30d score × 0.2 + age days × 0.05); eligibility resets weekly; a business cannot hold Booming and Rising simultaneously.
- Anti-gaming: views deduped per (user, business, day); owner self-actions excluded; actions from users flagged for anomalies excluded pending review; velocity spike > 10× the business's 30d average triggers an anomaly flag for admin.
- Leaderboard freshness: recompute every 10 min; UI shows "updated 10 min ago" timestamp.

### 8.7 Moderation

- Admin hide → content invisible to everyone except author (shows "removed by moderator"); restore returns it.
- Warn: notification + record in user profile. Suspend: 1–90 days. Ban: permanent.
- Every action logged in `moderation_actions` (who/what/when/why) — immutable.
- Banned words list admin-managed; changes apply to new content only.

### 8.8 Validation rules summary

- All IDs are UUIDv7; unknown IDs → 404. All enum fields validated against schema.
- Money: (decimal(12,2), currency char(3)) pairs, no floats; conversions only at read time from `currency_rates`.
- Pagination: `cursor` or `offset` params, max page size 50.
- All write endpoints require CSRF/Origin validation; all public reads cached (ETag).

---

## 9. Non-Functional Requirements ⏳

### 9.1 Performance budgets

| Metric | Target |
|--------|--------|
| p95 page load (public pages) | < 2s (3G simulated) |
| TTFB (server-rendered public page) | < 200ms |
| Search query (p95) | < 300ms |
| Chat message round-trip (p95, delivered) | < 500ms |
| Map viewport re-query | < 400ms debounced, < 300ms response |
| Trending recompute job | < 60s for 100k businesses |
| Image serving | CDN-ready variants, WebP/AVIF where supported |
| Media pipeline | Async via job queue: image variants < 5s, video < 60s, virus scan < 30s |
| Currency conversion read | < 10ms (cached rates); hourly refresh job |

### 9.2 Availability & scale targets

- Uptime ≥ 99.5%; design for 10k registered businesses and 100k monthly actives in year one; horizontal scaling path documented (stateless API, sticky-free WS via Redis pub/sub).
- Graceful degradation: search degrades to a LIKE query if the search index is unavailable; map falls back to list; chat queues offline messages (§5.5.3); currency conversion falls back to last-known rates with a staleness banner.

### 9.3 Security

- OWASP Top-10 baseline: XSS (React escaping, CSP header), CSRF (double-submit cookie), SQLi (parameterized queries everywhere), SSRF (link previews fetched via strict allowlist, no credential forwarding, 2s timeout, size caps), auth (argon2id, JWT rotation, httpOnly+Secure+SameSite cookies, TOTP 2FA), rate limiting on auth + chat + engagement endpoints, uploads (magic bytes, size caps, ClamAV scan, random names, separate origin), verification documents encrypted at rest with access audit, audit logs for auth + moderation + document views.
- Secrets: env-based, never in repo; staging and prod DBs separate.
- Data at rest: full-DB encryption where provider allows; TLS everywhere.

### 9.4 SEO & shareability

- SSR/prerender for all public pages (§6.6); dynamic meta/OG/Twitter; Schema.org LocalBusiness, AggregateRating, Product, BreadcrumbList; XML sitemap with business + category pages; robots.txt; canonical URLs; 404s return 404 status with suggestions; meaningful slugs; Lighthouse ≥ 90 (performance/SEO) on public pages.

### 9.5 Internationalization & locale

- Multi-language UI from day one (react-i18next); English ships first, additional languages land as translations are completed (JSON files + export pipeline); date/number/currency formatting locale-aware (Intl); currency conversion per viewer locale; timezone-aware open-now (§5.3.4); RTL-ready class strategy; owner content stays in the owner's language (no auto-translation).

### 9.6 Accessibility

- WCAG 2.1 AA: semantic HTML, keyboard navigation, focus management in modals/trays, ARIA labels on icon buttons, color contrast ≥ 4.5:1, alt text on all images, reduced-motion respect for map pulse markers.

### 9.7 Observability

- Structured logs (JSON), request IDs; metrics: request latency/error rates, WS connections, job durations, queue depths; error tracking (Sentry-style); alerting on 5xx spikes, job failures, search index lag; admin health page (§6.5).

### 9.8 Testing requirements

- Unit: services and business rules (validation, scoring math, anti-gaming, variant generation, currency conversion).
- Integration: API contracts, DB migrations, auth + 2FA flows, chat round-trips (edit/delete/reaction/receipts), media pipeline, WS multi-instance fan-out.
- E2E: critical journeys (§3 J1–J5) via Playwright; visual regression on storefront builder preview; PWA install + push smoke test.
- Load: search + map viewport queries at 10× projected peak; WS concurrency (10k connections); media upload throughput.

---

## 10. Tech Stack & Architecture ⏳

### 10.1 Stack summary

| Layer | Choice | Notes |
|-------|--------|-------|
| Frontend | React 18 + TypeScript + Vite | SPA with client routing; public pages pre-rendered |
| UI | Tailwind CSS + headless components (Radix) | Theme system mirrors storefront theming |
| State | TanStack Query (server state) + Zustand (UI state) | Optimistic engagement actions, compare tray |
| Routing | TanStack Router (or React Router 7) | Lazy routes, SSR-compatible path map |
| Backend | Go 1.22+ (net/http or chi router) | Single binary, WS via gorilla/websocket or nhooyr |
| API | REST JSON + WebSocket | Versioned `/api/v1`; JSON:API-lite style errors |
| DB | PostgreSQL 16 | UUIDv7 PKs, JSONB, PostGIS, full-text search (tsvector) |
| Cache/queue | Redis | Sessions/rate limits, WS pub/sub, job queue (or native PG for jobs v1) |
| Search | PostgreSQL FTS (tsvector + pg_trgm) v1 | Meilisearch/OpenSearch migration path documented |
| Object storage | S3-compatible (Cloudflare R2) | Public-read bucket + CDN; MinIO for local dev |
| Email | Resend | Verification, reset, alerts, digests (transactional templates) |
| Push | Web Push (VAPID) via service worker | PWA installable; no app stores needed |
| Currency | Open Exchange Rates / exchangerate.host feed | Hourly sync job into `currency_rates` |
| Media pipeline | FFmpeg worker (job queue) + ClamAV | Image variants (WebP/AVIF), audio m4a, video mp4 + poster, virus scanning |
| Deployment | Frontend: **Vercel**; Backend/services: **Railway** | Railway: Go API + WS, Postgres, Redis, R2, workers; horizontal scale later |
| Observability | Prometheus + Grafana + Loki (Railway-managed or self-host) | Dashboards + alerting from day one |

### 10.2 Repository layout (monorepo)

```
allstuff/
  docs/              PRD, architecture, decisions (ADRs)
  apps/
    web/             React + TS frontend (Vite)
  services/
    api/             Go backend (REST + WS + jobs)
  infra/
    docker/          compose files, Dockerfiles
    postgres/        migrations (versioned SQL, e.g. goose/tern)
    observability/   prometheus/grafana config
  packages/          shared TS types (OpenAPI-generated), shared config
  scripts/           dev tooling, seed data
```

### 10.3 Backend structure (Go)

```
services/api/
  cmd/api/           main: server, ws, jobs
  internal/
    http/            routers, middleware (auth, rbac, ratelimit, logging)
    handler/         per-resource handlers
    service/         business logic (pure, testable)
    repo/            SQL queries (sqlc or pgx + hand-written SQL)
    domain/          types, errors, rules (§8 enforcement)
    ws/              hub, connection manager, pub/sub client
    jobs/            trending recompute, digests, currency sync, media transcode + scan, cleanup
    config/          env parsing
    migrate/         migrations entrypoint
```

- Service layer owns business rules; handlers are thin; repos use parameterized SQL via `pgx`; sqlc for typed queries.
- Domain errors map to HTTP statuses centrally (400/403/404/409/429/500 with consistent envelope).
- WS gateway: single hub per instance; Redis pub/sub fan-out for multi-instance; reconnect + resync via REST fallback.

### 10.4 Frontend structure

```
apps/web/src/
  app/           router, layout shells (public/user/owner/admin)
  features/      discovery, map, business, dashboard, chat, admin (colocated code)
  components/    shared UI kit (Radix + Tailwind primitives)
  api/           typed client (OpenAPI-generated) + WS client
  i18n/          en translations, locale config
  storefront/    business-page renderer shared with owner preview
```

- One renderer component for a business page used by: public page, owner live preview, "preview as guest".
- Compare tray is a global feature with cross-route persistence (localStorage + account sync).

### 10.5 Data flow highlights

- **Publish flow:** draft theme/layout → Publish → transaction copies draft to `published_snapshot` + sets `last_published_at` → invalidation event → CDN cache purge (or ETag bump).
- **Trending:** engagement events → append-only table → 10-min job aggregates → `trend_snapshots` → API reads snapshots (cached 10 min) → UI renders leaderboards/badges.
- **Chat:** REST for history + WS for live; messages acked (`client_msg_id`), outbox on client, at-least-once with dedupe.
- **Search:** writes update tsvector column (triggers); reads via tsquery + ranking; filters applied in SQL; viewport bounds for map queries.
- **Media:** upload → object storage → job queue → FFmpeg variants + ClamAV scan → status callbacks update the media row → clients notified (poll/WS).
- **Currency:** hourly job fetches rates → `currency_rates` → read-time conversion (Intl formatting); staleness handled per §11.
- **Migrations:** versioned SQL in repo, run at deploy (before app start); backward-compatible only.

### 10.6 Environments

- **dev:** docker compose, hot-reload frontend, seeded data, fake SMTP (Mailpit), MinIO local.
- **staging:** mirrors prod, weekly refresh from prod (sanitized), used for E2E + load tests.
- **prod:** Vercel (frontend) + Railway (API/WS/workers/Postgres/Redis/R2); backups daily + PITR; blue-green rollout via Railway deploys; secrets in env config; Vercel preview deployments per PR; staging on Railway mirrors prod schema with sanitized seed.

---

## 11. Edge Cases & Error Handling ⏳

### 11.1 Catalogued edge cases and their handling

| # | Edge case | Handling |
|---|-----------|----------|
| E1 | Guest tries to chat/review/like | Register prompt modal preserving intent (draft message, return URL); after auth, action resumes automatically |
| E2 | Business slug collision | Auto-suffix `-2`, `-3` at creation; slug immutable |
| E3 | Category deleted while businesses reference it | Reassignment is mandatory before delete (§5.8.3); UI blocks delete until moved |
| E4 | Business paused/closed while user has open chat | Thread stays readable; new messages show "Business is temporarily closed" notice; user can still send (owner may reply after reopening) |
| E5 | Owner suspended while chats open | Messages from owner blocked with notice; user can message (goes to queue) |
| E6 | User deletes account | 14-day grace (restorable); export first; after final deletion: name/anonymized, chats anonymized, engagement events kept (score integrity), reviews anonymized |
| E7 | Offline chat send | Client outbox; queued locally; flushed on reconnect; duplicate-send protection via client_msg_id |
| E8 | Concurrent publish vs draft edit | Single-writer per business via row lock (SELECT ... FOR UPDATE) on publish transaction; draft edit during publish → conflict error, user retries |
| E9 | Geolocation denied | Radius search requires explicit "Set location" fallback: manual city/address input; no silent failure |
| E10 | Map viewport too large (zoomed out) | Bounds-capped at city scale; query degrades to top-N by score with "Refine your area" hint |
| E11 | Search with no results | Empty state with suggestions (popular categories nearby, clear filters, "Add your business") |
| E12 | Trending anomaly (velocity spike) | Flagged for admin review; excluded from leaderboards while flagged; notification to admin (§8.6) |
| E13 | Banned-word false positive | Send blocked with generic hint; admin can add to allowlist; no automated account penalty |
| E14 | Email deliverability (reset/verify) | Resend with cooldown (60s); token expiry messaging; support contact fallback |
| E15 | Image upload fails mid-upload | Client resumable upload (presigned multipart, chunked); orphan cleanup job removes unreferenced media > 24h |
| E16 | User reports their own content | Blocked client+server (reporting own content returns 400) |
| E17 | Two admins moderate same report | Optimistic lock: second resolver sees "already resolved"; audit log records both |
| E18 | Business page 404 | 404 with "Did you mean?" (nearby same-category businesses) + search CTA |
| E19 | Rate limit exceeded | 429 with Retry-After; clear copy ("too many attempts, try again in Xs") |
| E20 | Timezone shift (DST) for open-now | Hours stored in business timezone; open-now computed with IANA tz db; DST edge annotated in UI as "local time" |
| E21 | Message edit while other side reacts/replies | Edits are non-destructive: reactions/replies reference the original; edit history preserves versions; no locking |
| E22 | Currency rate stale / provider down | Serve last-known rates with "rates may be outdated" banner; fall back to owner currency with note |
| E23 | 2FA lost (no recovery codes) | Email magic-link challenge (15 min) → allow re-enrollment; admin accounts require direct founder approval |
| E24 | Video upload exceeds cap | Client pre-check (duration/size); server rejects 413 with copy; resumable uploads for large files |
| E25 | Verification document rejected (expired license) | Owner notified with specific reason + re-upload flow; business stays in draft; 30-day deadline then auto-archive |
| E26 | Blocked user tries to reply / join a thread | Server rejects with "unavailable"; no details leaked to the blocked party |
| E27 | Deleted-for-everyone message still quoted/replied to | Tombstone shown; quote renders "(deleted message)"; reactions cascade-deleted |
| E28 | Two devices editing the same message | Last-write-wins on text; edit_history appends both; UI shows one "Edited" badge |
| E29 | Export during deletion grace | Export allowed until final deletion; snapshot frozen at request time |
| E30 | Co-owner invite expires | 7-day expiry; re-invite allowed; invite/reject trail audited |

### 11.2 Error contract

- Consistent envelope: `{ "error": { "code": "review_exists", "message": "...", "fields": {...} } }`; codes are stable and documented (frontend maps codes → i18n strings).
- HTTP mapping: 400 validation / 401 unauth / 403 forbidden (incl. suspended) / 404 missing / 409 conflict (unique violations, version conflicts) / 429 rate limit / 5xx server (masked, logged with request ID).
- Network failures on mutations: automatic retry only for idempotent ops (safe methods + client_msg_id chat); otherwise surfaced with "Retry" affordance.
- Client offline state: global banner + per-feature offline caching (search results page, chat outbox).

---

## 12. Milestones & Delivery Plan ⏳

### 12.1 Definition of Done (global)

- Feature passes its §5 spec + §8 rules; unit/integration tests green; E2E journey passes; Lighthouse ≥ 90 on affected public pages; i18n keys exist (English); no console errors; migration-safe (backward compatible); admin can operate it (documented).

### 12.2 Milestones

**M0 — Foundation (wk 1–2)**
Monorepo, CI/CD (Vercel + Railway), docker compose dev env, Postgres migrations toolchain, Go API skeleton (REST + WS), React skeleton + 4 route shells (public/user/owner/admin), auth (register/login/verify/reset, JWT + session registry, CSRF), base UI kit, i18n plumbing, observability wiring.

**M1 — Directory core (wk 3–5)**
Categories (admin CRUD + tree), business wizard (6 steps incl. verification documents), verification with levels, public business page, homepage (hero, category grid, featured), search (FTS + filters + sorts + autocomplete), category pages, slug system, SEO (meta/sitemap/Schema.org).

**M2 — Storefront (wk 6–8)**
Dashboard shell + business switcher + co-owner invites, storefront builder (preset + custom themes, sections, live preview, publish snapshots), products with variants/options/stock/currency, business settings + danger zone, owner analytics, owner notifications.

**M3 — Engagement (wk 9–10)**
Likes/recommends/collections/comments (threading + likes + mentions)/reviews (business + product + helpful votes), engagement_events pipeline, trending job, leaderboards (global/category/city), Booming/Rising badges + homepage strips, anti-gaming + anomaly flags.

**M4 — Map & compare (wk 11)**
Full-screen map (clusters, layers, viewport search, near-me, directions, share), homepage widget, business mini-maps, compare tray + comparison page, shareable compare URLs.

**M5 — Messaging (wk 12–14)**
Threads (direct + business), full message features (text/image/file/audio/video/link, reply, edit, delete me/everyone, forward, reactions, receipts, typing, search, gallery, pin, export), WS transport + offline outbox + Web Push, quick replies, blocks, moderation evidence, media pipeline (transcode + ClamAV), spam heuristics.

**M6 — Admin & platform (wk 15–16)**
Admin panel (verify with document review + level assignment, moderation queues incl. reactions/attachments + audit trail, categories, users, curation, config), document access audit, admin analytics + health.

**M7 — Security, quality & launch (wk 17–18)**
2FA + session hardening, E2E suites (Playwright J1–J5), load tests (search/map/WS/media), security audit (OWASP checklist + dependency audit), a11y pass (WCAG AA), backups/PITR verified, PWA + push verification, staging smoke + launch checklist, public launch.

### 12.3 Post-launch (separate products, per §4 out-of-scope)

Monetization decision, native app packaging, payments/orders/delivery, AI-assisted features.

---

## 13. Decisions Log ✅

Founder decisions (2026-08-14) — binding for all specs above:

| # | Decision | Impact |
|---|----------|--------|
| D1 | **Verification:** golden standard with documents — business registration/license + tax ID + optional owner identity; two badge levels (Verified / Fully Verified) | §5.4.1, §5.8.1, §8.2 |
| D2 | **Messaging is full chat** — user↔user, user↔business; text, media (image/file/audio/video/link), reply, edit, delete (me/everyone), forward, reactions, receipts, typing, search, pin, export | §5.5, §7.3, §8.5 |
| D3 | **Collections:** organized lists (default "Favorites"), private by default, publishable; collection saves feed the trend score | §5.6, §7.2 |
| D4 | **Products:** full variants/options/SKU/stock/currency + product reviews — no price-only simplification | §5.4.3, §7.1 |
| D5 | **Currency:** read-time conversion (hourly rate feed); owner sets business/product currency | §8.8, §10.1 |
| D6 | **Email:** Resend | §10.1 |
| D7 | **Deployment:** Vercel (frontend) + Railway (backend, Postgres, Redis, R2, workers) | §10.6 |
| D8 | **Golden standard directive:** no MVP-tier scope — every feature ships complete; specs revised accordingly across all sections | whole document |

*PRD status: complete and approved. All sections reflect the golden-standard build.*
