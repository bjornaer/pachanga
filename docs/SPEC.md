# Pachanga: Build Spec

Oct 12, 2026 · @Max

## 0. How to use this spec (instructions to the coding LLM)

Build the product milestone by milestone. When asked to build "up to milestone M\_n", implement every milestone from M0 to M\_n, and stop only when every Definition of Done (DoD) item for those milestones passes.

- **Source of truth.** This document defines behaviour. If something is ambiguous, pick the simplest option consistent with the spec, record it in `docs/DECISIONS.md`, and continue. Do not invent features from later milestones.
- **Forward compatibility.** Data models and APIs may contain fields needed by later milestones (e.g. `payment_status`, `tournament_id`) when this spec says so, but no UI or business logic for them until their milestone.
- **Milestone completion.** A milestone is complete only when: (1) all its DoD checkboxes pass, (2) all automated tests pass in CI, (3) migrations apply cleanly on an empty database and on the previous milestone's database, (4) the app builds for iOS and Android and the admin web builds, (5) `docs/MILESTONES.md` is updated with what was delivered and any deviations.
- **Definition of usable.** Every milestone from M1 onward must be deployable to a staging environment and usable end to end by a real user, not a demo with mocked data.
- **Seeds.** Provide a `make seed` command that creates realistic demo data in Madrid (users, teams, matches, courts) for manual QA.
- **Language of code.** Code, identifiers and comments in English. User-facing strings always via i18n keys (es-ES default, en).

## 1. Product overview

**Pachanga (Spanish for a casual kickabout**) is a mobile app (Spain first) where adult amateur footballers find matches, fill gaps in teams, challenge other teams, rate each other and find pitches. Code identifier: `pachanga` (bundle IDs, package names, repo).

**Core loops**

1. A player needs a game: searches nearby public matches or teams looking for players, joins, plays, confirms the score, rates teammates and opponents.
2. A team needs players or an opponent: the captain posts a "looking for players" or "looking for opponent" listing, or challenges a team directly from its profile.
3. Anyone needs a pitch: searches courts near them or by name, sees free public pitches, prices and availability where known, calls or books.

**Principles**

- **Trust by consensus.** Results, goals and assists only count toward stats and leaderboards once confirmed by a quorum of participants. Ratings only come from people who actually played together.
- **Know your crowd.** Every match shows its age range, gender mix, average rating and tags before you join.
- **Adults only.** 18+ accounts. No minors' data, ever.
- **Pay at the venue.** No money flows through the app in early milestones; the data model is ready for it later.
- **Football formats.** Futsal, 5-a-side (F5), 7-a-side (F7), 11-a-side (F11).

## 2. Tech stack and architecture

One Go API serves the React Native app and the admin web app; Postgres with PostGIS stores everything, including geo data.

| Layer | Choice | Notes |
| --- | --- | --- |
| Mobile | React Native + Expo (TypeScript), Expo Router | iOS and Android from one codebase; EAS builds |
| Admin web | React + Vite (TypeScript) | Separate app, same API, `super_admin` role only |
| API | Go 1.23+, `chi` router, `pgx` + `sqlc` | REST/JSON, OpenAPI 3 spec generated and committed |
| Realtime | WebSocket endpoint in the Go API | Captain chat, live updates of match roster |
| Background jobs | Postgres-backed queue (e.g. `river`) | Reminders, quorum timeouts, court sync, sanctions |
| Database | PostgreSQL 16 + PostGIS | `geography(Point,4326)` columns, GiST indexes for "within X km" queries |
| Files | S3-compatible object storage | Avatars, team crests, court photos; signed upload URLs |
| Push | Expo Push (FCM/APNs underneath) | Device tokens per user |
| Maps | `react-native-maps` with Google provider on both platforms | Required if any Google Places data is shown (see section 5) |
| Auth | Email + password, Sign in with Apple, Sign in with Google | JWT access token (15 min) + rotating refresh token |
| Infra | Docker Compose locally; one container image for API + worker | Staging and prod on DigitalOcean (App Platform or Droplets, Managed PostgreSQL with PostGIS, Spaces for files); GitHub Actions CI |

**Why PostGIS:** "matches/courts within 10 km sorted by distance" becomes one indexed SQL query (`ST_DWithin`, `ST_Distance`) instead of hand-rolled maths.

**Email delivery:** DigitalOcean blocks SMTP ports 25, 465 and 587 on all Droplets by default ([DigitalOcean docs](https://docs.digitalocean.com/support/why-is-smtp-blocked/)), so self-hosted SMTP is not possible there. Send through providers' HTTPS APIs (port 443) behind an `EmailSender` interface with ordered failover: **Brevo** primary (free plan 300 emails/day) and **Mailjet** fallback (free plan 6,000/month, 200/day), both EU-based ([SendLayer comparison, 2026](https://sendlayer.com/blog/best-free-smtp-servers/)). Configure SPF, DKIM and DMARC on the sending domain. Only transactional mail (verification, password reset, sanction notices); everything else goes by push. The worker tracks daily sends per provider and switches before a cap is hit; if both caps are reached, verification emails queue and the app shows "code on its way".

**Repository layout (monorepo)**

- `apps/mobile` (Expo), `apps/admin` (Vite)
- `services/api` (Go: `cmd/api`, `cmd/worker`, `internal/<domain>`)
- `db/migrations` (SQL, `golang-migrate`), `db/queries` (sqlc)
- `packages/api-client` (TypeScript client generated from OpenAPI), `packages/i18n`
- `docs/` (`DECISIONS.md`, `MILESTONES.md`, `openapi.yaml`)

**Backend conventions:** domain packages (`users`, `teams`, `matches`, `results`, `ratings`, `reports`, `courts`, `chat`, `notifications`, `admin`); handlers thin, services hold rules, all rule enforcement server-side; every mutation writes an `audit_log` row; times stored UTC, displayed in the venue's timezone (Europe/Madrid by default).

## 3. Domain model

All IDs are UUIDv7; every table has `created_at`, `updated_at`; soft-deletable entities have `deleted_at`. Enums are Postgres enums or check constraints.

| Entity | Key fields | Notes |
| --- | --- | --- |
| `user` | email, password\_hash?, auth\_providers, display\_name, avatar\_url, birth\_date, gender, preferred\_positions\[\], preferred\_foot, home\_location (geography), locale, status (`active`, `suspended`, `banned`, `deleted`), adult\_attested\_at, terms\_version\_accepted | `birth_date` must give age at least 18 at signup. `gender` enum: `male`, `female`, `non_binary`, `prefer_not_to_say` |
| `user_rating_summary` | user\_id, overall (1-99), per-category averages, ratings\_count, is\_visible | Materialised; `is_visible` false until ratings\_count is at least 5 |
| `team` | name, crest\_url, description, home\_area (geography), preferred\_formats\[\], status (`active`, `suspended`, `banned`), visibility |  |
| `team_member` | team\_id, user\_id, role (`captain`, `co_admin`, `member`), joined\_at | Exactly one captain per team (partial unique index) |
| `match` | title, description, format, scheduled\_start, duration\_min, court\_id?, custom\_location (geography + text)?, capacity, visibility (`public`, `private`), share\_token, tags\[\], gender\_policy (`open`, `men_only`, `women_only`), skill\_hint?, status (`open`, `full`, `in_progress`, `awaiting_result`, `confirmed`, `disputed`, `cancelled`), kind (`pickup`, `team_vs_team`), home\_team\_id?, away\_team\_id?, created\_by, tournament\_id (null, future), payment fields (null, future) | Either `court_id` or `custom_location` required |
| `match_admin` | match\_id, user\_id | Creator is implicit admin; creator can add co-admins |
| `match_participant` | match\_id, user\_id, side (`A`, `B`, `unassigned`), slot (`field`, `goalkeeper`), status (`joined`, `invited`, `waitlisted`, `left`, `removed`, `no_show`), joined\_via (`self`, `added_by_admin`, `share_link`, `team`), waitlist\_position? |  |
| `listing` | type (`team_needs_players`, `team_needs_opponent`, `player_needs_team`), team\_id?, user\_id?, match\_id?, format, area (geography), date\_window, positions\_needed\[\], spots\_needed, note, status (`open`, `filled`, `closed`, `expired`) | Public discovery board |
| `challenge` | from\_team\_id, to\_team\_id?, listing\_id?, proposed\_start, court\_id?, format, message, status (`pending`, `accepted`, `declined`, `countered`, `expired`, `cancelled`), resulting\_match\_id? |  |
| `match_result` | match\_id, version, score\_a, score\_b, submitted\_by, status (`pending_confirmation`, `confirmed`, `disputed`, `expired_unconfirmed`), confirmed\_at | One active version; edits create a new version and reset confirmations |
| `match_event` | result\_id, type (`goal`, `own_goal`, `assist`), scorer\_user\_id, assist\_user\_id?, side, minute? |  |
| `result_confirmation` | result\_id, user\_id, side, decision (`confirm`, `dispute`), dispute\_reason? |  |
| `rating` | match\_id, rater\_id, ratee\_id, categories {skill, teamwork, fair\_play, punctuality, fitness} each 1-99, overall (computed mean), created\_at | Unique (match\_id, rater\_id, ratee\_id); never shown individually |
| `report` | reporter\_id, target\_type (`user`, `team`), target\_id, match\_id, reason (`abuse`, discrimination, `aggression`, `no_show`, `cheating_stats`, `other`), details, status (`open`, `upheld`, `rejected`) | Unique (reporter, target, match). Stores computed \`severity\`, \`credibility\`, \`independence\`, \`cluster\_key\`, \`retaliation\_flag\`; reporter credibility kept on \`user.report\_credibility\`; user also stores \`conduct\_score\` |
| `sanction` | target\_type, target\_id, type (`warning`, `suspension`, `ban`), source (`auto`, `admin`), reason, starts\_at, ends\_at?, lifted\_by?, lifted\_at? |  |
| `court` (venue) | name, address, location (geography), sports\_surfaces, formats\[\], indoor, surface (`artificial_grass`, `natural_grass`, `concrete`, `parquet`, `other`), access (`public_free`, `reservation_required`, `members_only`, `unknown`), phone, website, booking\_url, booking\_provider (`none`, `phone`, `external_link`, `playtomic`, `native`), price\_info, opening\_hours, photos\[\], source (`osm`, `manual`, `owner`, `user_submitted`), source\_ref (e.g. OSM id, Google place\_id), status (`pending_review`, `active`, `removed`), claimed\_by\_user\_id? | One venue may hold several pitches |
| `court_pitch` | court\_id, name, format, price\_per\_hour\_cents?, external\_resource\_id? | For availability and booking |
| `court_tombstone` | source, source\_ref, removed\_by, removed\_at, reason | Sync never re-creates these (section 5) |
| `court_claim` | court\_id or proposed court data, user\_id, business details, status | Owner registration flow |
| `chat_thread` / `chat_message` | thread per match (captains and match admins only), sender, body, created\_at |  |
| `notification` / `device_token` | user\_id, type, payload, read\_at; token, platform |  |
| `audit_log` | actor\_id, action, entity, entity\_id, diff, at | Every admin and moderation action |
| `payment_*` (future) | Reserved names only: `payment_intent`, `payment_split` | No tables until the payments milestone; match has nullable `price_per_player_cents`, `payment_mode` (`at_venue` default) |

## 4. Functional requirements

Each area lists the rules the server must enforce. The milestone that delivers each area is in section 10.

### 4.1 Registration and profile

- Sign up with email + password, Apple or Google. Email verification (6-digit code, 15 min expiry, max 5 attempts) required before joining or creating anything; device ban bits checked at signup (see 6.4).
- Mandatory at signup: display name, birth date, gender (`male`, `female`, `non_binary`, `prefer_not_to_say`), home area (pick on map or allow location), accept Terms + Privacy Policy, and an **adult attestation checkbox**: "I confirm I am 18 or older and that this account is used only by me." Store `adult_attested_at` and `terms_version_accepted`.
- Reject signup if birth date gives an age under 18. Show a neutral message ("This app is for adults only"); do not store the rejected birth date.
- Optional: avatar, preferred positions (GK, DEF, MID, FWD), preferred foot, bio (280 chars).
- Public profile shows: player card (4.2), age **range** (5-year bucket, e.g. "30-34"), gender only if the user opts in to show it, match history, stats, teams, badges. Exact birth date is never exposed by the API to other users.
- Account deletion: anonymises the user (name becomes "Deleted player"), keeps aggregate results so other people's histories stay coherent, removes personal data within 30 days (GDPR).

### 4.2 Player card

- FC-style card component reused everywhere a user appears (rosters, search, chat headers, profile): avatar, name, overall rating (1-99) in a badge, main position, and the 5 category values on the full card.
- Before 5 ratings received: badge shows "NEW" instead of a number.
- Card tier colour by overall: 1-64 bronze, 65-74 silver, 75-99 gold (constants in config).
- Captain armband icon "C" overlays the card in team and match contexts where that user captains the team involved; co-admins get a smaller "A" marker.

### 4.3 Teams

- Any verified user can create a team and becomes its captain. A user may belong to any number of teams.
- Roles: exactly one `captain`, any number of `co_admin`, rest `member`. Captain and co-admins can: invite/remove members, accept join requests, edit team, post listings, send/accept challenges, pick rosters. Only the captain can promote/demote co-admins, transfer captaincy, or delete the team.
- If the captain leaves, they must transfer captaincy first; if the captain's account is deleted, the longest-serving co-admin (else member) becomes captain automatically.
- Join paths: invite (in-app or link), request to join (approved by captain/co-admin).
- Team profile: crest, name, description, area, formats, members list (captain marked), team rating (mean of members' visible overall ratings), record W/D/L from confirmed team-vs-team matches, recent matches, and a primary **Challenge** call-to-action button (4.6) shown to users who captain or co-admin at least one other team.
- Team status `suspended`/`banned` (section 6): hidden from search, cannot post listings, send or receive challenges, or be added to matches.

### 4.4 Matches

**Create:** title (max 80), description (max 1,000), format, date/time + duration, location (pick a court from the directory or drop a custom pin with a label), capacity (defaults: futsal 10, F5 10, F7 14, F11 22, editable), optional goalkeeper slots (0-2), visibility, gender policy, up to 3 tags, optional price-per-player text shown as "pay at venue".

**Tags (predefined, admin-editable list):** `casual`, `competitive`, `training`, `beginner_friendly`, `tournament`, `semi_pro`, `professional`, `veterans_35_plus`, `bibs_provided`, `ball_provided`.

**Visibility:**

- `public`: appears in search, map and suggestions.
- `private`: never in search, suggestions, public listings or other users' "nearby" views. Reachable only via share link (`/m/{share_token}`, 128-bit random, regenerable by admins) or by being added/invited by a match admin. Private matches still appear in participants' own histories.

**Joining:**

- Players join themselves (public or via link) or are added by a match admin. Being added creates status `invited` with a push notification; the user accepts or declines (prevents silently adding people to inflate stats).
- When full, new joiners go to a FIFO waitlist; when someone leaves, the first waitlisted player is promoted and notified.
- Leaving less than 2 hours before kick-off is allowed but recorded as a late withdrawal (shown to admins, counts at half weight toward no-show sanctions).
- Joining is blocked for suspended/banned users, for users blocked by the creator, and for overlapping matches (same user, overlapping time window).
- Gender policy `men_only`/`women_only` is shown prominently; a user whose declared gender does not match sees a confirmation modal before joining (advisory in M1; see section 11).

**Admins:** creator plus co-admins they appoint. Admins can edit details (participants notified on time/location change), add/remove players, assign sides and GK slots, cancel (everyone notified), submit results, mark no-shows.

**Match card and detail must show, computed live from joined participants:**

- Spots: "8/10 joined, 2 waitlisted".
- Age profile: average age as a 5-year bucket plus the decade spread, e.g. "Avg \~30-34 · ages 20s to 40s". Never individual ages.
- Gender mix: "Men only so far", "Women only so far", or "Mixed". Users with `prefer_not_to_say` are excluded from the computation; any `non_binary` participant makes it "Mixed".
- Average overall rating of participants with visible ratings, as a range band (e.g. "65-74").
- Tags, format, court (with access type and phone button), schedule, organiser card.

**Lifecycle (status machine, transitions by the worker):** `open` ⇄ `full` → `in_progress` (at start) → `awaiting_result` (at start + duration) → `confirmed` | `disputed` | (`awaiting_result` expires to unverified after 72 h). `cancelled` from any pre-start state.

### 4.5 Discovery and search

- **Home feed "For you"**: public matches and listings within the user's radius (default 10 km) in the next 7 days, ranked by: start time soonest, distance, rating band closeness to the user's own overall, matching preferred formats. Never shows private matches.
- **Search** (list + map toggle): free text on title/court name; filters for distance, date range, time of day, format, tags, spots available, gender mix, age bucket, rating band, court access type.
- **Teams search**: by name, area, format; badge "looking for players" or "looking for opponent" when the team has an open listing.
- **Players search**: by name only (no location search for individuals, for safety).

### 4.6 Listings and challenges

- **Listings board** (public, filterable by type, format, area, date): `team_needs_players` (positions + spots + date or linked match), `team_needs_opponent` (format, date window, area, optional court), `player_needs_team`.
- Responding to `team_needs_players` = a join request to the linked match or the team. Responding to `team_needs_opponent` = sending a challenge.
- **Challenge** (from team profile button or from a listing): sender picks their team (if they admin several), date/time, format, court (optional), message. Recipient captain/co-admins can accept, decline or counter-propose date/court. Expires at proposed start minus 2 h or after 7 days.
- On accept, the server creates a `team_vs_team` match with home/away teams, both teams' admins as match admins, visibility `private` by default (either side may make it public to recruit extras), and notifies both rosters. Each team's admins pick their side's roster from members; members can also self-join their own side.
- Listings auto-expire at their date window end; filled listings close automatically when linked match spots fill.

### 4.7 Results, quorum and confirmation

- After the match reaches `awaiting_result`, any match admin (in team-vs-team: either team's admin) submits: final score, and goal/assist events (scorer, optional assister, side). Sum of goals per side must equal the score (own goals allowed).
- Every participant with status `joined` (not no-show) gets a **push notification and, on next app open, a blocking-style modal** "Confirm the result of {match}" showing score, scorers and assists, with buttons Confirm / Dispute (dispute requires picking a reason: wrong score, wrong scorer/assist, didn't play, other + text). The modal can be dismissed but reappears on each app open until answered or expired. Reminder pushes at +12 h and +48 h.
- **Quorum rules (constants in config):**
  - Pickup match: `confirmed` when confirmations ≥ max(3, ceil(40% of eligible participants)) and disputes < 25% of eligible participants.
  - Team vs team: same threshold applied **on each side separately**, and at least one admin of each team must confirm.
  - If disputes reach 25% of eligible participants, the result becomes `disputed`; admins may submit a corrected version (new version resets all confirmations). After 2 disputed versions, it stays `disputed` permanently.
  - If quorum is not reached within 72 h: `expired_unconfirmed`. The match stays in histories labelled "Unverified".
- **Only `confirmed` results count** toward stats, records, team W/D/L, leaderboards and badges.
- The submitter's own confirmation is automatic and counts once.

### 4.8 Ratings

- After scheduled end, each joined participant (not marked no-show) can rate every other participant they played with, within 48 h. One rating per (match, rater, ratee). Push notification + "Rate your match" card on home.
- Categories, each 1-99 slider (default untouched = must move or tap "skip player"): **Skill**, **Teamwork**, **Fair play**, **Punctuality**, **Fitness**. Rating overall = mean of the 5.
- Ratings are anonymous; individuals never see who rated what. Only aggregates are shown.
- User overall = Bayesian average over ratings from the last 12 months: (C × 60 + Σ ratings) / (C + n) with C = 5, per category and overall. Recomputed by job on new rating.
- Anti-collusion: the same rater's ratings of the same ratee count at most 3 times per rolling 90 days; ratings from accounts under 7 days old are stored but weighted 0.5.
- A user who received at least 3 ratings in a match where they were marked no-show by an admin has those ratings discarded.

### 4.9 History, stats and leaderboards

- Profile history: all matches (newest first) with result, side, own goals/assists, verification label (Confirmed, Unverified, Disputed).
- Stats from confirmed matches only: matches played, W/D/L, goals, assists, goals per match, MVP-style "top rated in match" count.
- **Leaderboards** (per city and per Spain, this month / this season (Sep-Jun) / all time): top scorers, top assisters, most matches, highest overall (min 10 ratings). Eligibility filters against spoofing: only confirmed matches with at least 6 participants, at least 4 distinct confirmers, and at least 4 participants with accounts older than 14 days. A player scoring more than 10 goals in one match triggers an admin review flag; their goals in that match are excluded from leaderboards until reviewed.
- Badges (simple, server-computed): first match, 10/50/100 matches, hat-trick (confirmed), always-on-time (punctuality ≥ 85 with 20+ ratings).

### 4.10 Captain chat

- Each match has one chat thread whose members are the match admins (creator, co-admins) and, in team-vs-team matches, both teams' captains and co-admins. Purpose: coordinating logistics.
- Text only, max 1,000 chars, realtime via WebSocket, push on new message, history kept 90 days after the match. Messages can be reported (feeds the report system).

### 4.11 Notifications

Push + in-app inbox for: added/invited to match, waitlist promotion, match edited/cancelled, reminder 24 h and 2 h before kick-off, result to confirm, result confirmed/disputed, rate your match, challenge received/answered, join request, team invite, new chat message, report outcome, sanction applied. Per-type toggles in settings; transactional safety notices (sanctions) cannot be disabled.

## 5. Courts: data strategy and integrations

The court directory is built mainly from OpenStreetMap plus admin and owner entries; Google Places is only a live enrichment layer, and in-app booking through Playtomic requires a formal partnership.

### 5.1 Sources and their constraints

| Source | Use it for | Constraints the code must respect |
| --- | --- | --- |
| OpenStreetMap (Spain extract via Geofabrik or Overpass API) | Bulk import of pitches: `leisure=pitch` with `sport=soccer`; `access`/`fee` tags to detect free public pitches | Data is ODbL. Show "© OpenStreetMap contributors" wherever OSM-derived data is displayed. If you share data or any derivative database with others it must stay under the ODbL, and recipients of a produced work can request the underlying OSM-derived data ([OSMF licence FAQ](https://osmfoundation.org/wiki/Licence_and_Legal_FAQ), [attribution guideline](https://osmfoundation.org/wiki/Attribution)). Keep OSM-derived fields in a separable `court_osm_source` table keyed by OSM id. |
| Google Places API | Live enrichment at view time: phone, opening hours, photos, rating | Content must not be pre-fetched, cached or stored, except the `place_id`, which can be stored indefinitely ([Places policies](https://developers.google.com/maps/documentation/places/web-service/policies)). Places data shown on a map must be on a Google Map with attribution. So: store only `place_id`, fetch details on demand, never write them to our DB. |
| Admin manual entry | Courts missing from OSM, corrections | Field-level lock (see 5.2) |
| Owner registration (claim) | Venues add/claim their listing: phone, prices, hours, booking link, pitches | Pending until admin approves |
| User submission | "Suggest a court" with pin, name, access type, photo | Pending review; reporter credited |
| Playtomic | Deep link "Book on Playtomic" from day one; real availability/booking only after partnership | Its official third-party API is for venues already set up on Playtomic, with credentials issued after emailing partnersapi@playtomic.io ([docs](https://third-party.playtomic.io/)). The club API is described as read-only ([help centre](https://helpmanager.playtomic.com/hc/en-gb/articles/38836515997073)), and Playtomic is blocking unauthorised third-party automations in favour of its Playtomic Connect partner programme ([notice](https://helpmanager.playtomic.com/hc/en-gb/articles/47390286355089-Third-party-integrations-and-Playtomic-Connect)). Never use reverse-engineered endpoints. Build a `BookingProvider` interface so a Playtomic adapter can be added once approved. |

### 5.2 Sync rules (court import job)

1. Runs on admin demand ("Re-sync" button) and weekly by schedule. Idempotent upsert keyed by (`source`, `source_ref`).
2. **Tombstones:** before inserting, check `court_tombstone` for (`source`, `source_ref`). If present, skip silently. Admin removal always writes a tombstone; only an explicit admin "Restore" deletes it.
3. **Field locks:** any field edited by an admin or verified owner is marked locked (`court_field_lock`); sync never overwrites locked fields.
4. New records from sync go to `active` if access type and location are present, otherwise `pending_review`.
5. Records that disappeared upstream are flagged `stale` for admin review, never auto-deleted.
6. Each run writes a sync report: created, updated, skipped (tombstoned), skipped (locked fields), stale.

### 5.3 Court UI

- **Nearby:** map + list of courts within radius, sorted by distance; filters: access (free public, reservation, members), format, surface, indoor/outdoor, has online booking.
- **Search:** by name (Postgres `pg_trgm` fuzzy match) and by place (city, neighbourhood, address via a geocoder; use a self-hosted Nominatim or a paid geocoder, respecting the provider's usage policy).
- **Court page:** name, photos, address + "Directions" (opens Apple/Google Maps), access badge ("Free, no booking" highlighted), surface, formats, pitches, price ("from €X/h" when known), opening hours, phone button (`tel:` link), booking button by provider (`external_link` opens browser, `playtomic` opens deep link or in-app flow once partnered, `native` opens in-app booking), upcoming public matches at this court, "Create match here", "Report wrong info", attribution footer.
- **Availability:** shown only when a provider supplies it (`native` or a partner adapter). Otherwise the page says "Call to check availability".

### 5.4 Native booking (later milestone)

For venues that onboard directly: owner portal (web) to manage pitches, weekly opening slots, prices and blackout dates. Users pick a free slot in the app and send a **booking request**; the venue confirms or rejects; payment at the venue. Double-booking prevented by a Postgres exclusion constraint on (`court_pitch_id`, `tstzrange`). A confirmed booking can be attached to a match.

## 6. Trust, safety and sanctions

Sanctions are driven by a **conduct score** per user: independent, credible, severity-weighted reports that decay over time. Escalation is stepped, permanent bans need an admin or a repeat offence, and removal sticks through device ban bits, probation and evasion checks. All constants live in the `config` table.

### 6.1 Reporting

- Report a **user** from: a match roster (within 7 days after the match), their profile (must share at least one match in the last 30 days), or a chat message (the message is snapshotted as evidence). Report a **team** from a team-vs-team match against it.
- Reasons: `abuse` (insults, harassment), `discrimination`, `aggression` (violence, threats), `no_show`, `cheating_stats`, `other`. Free text up to 1,000 chars, optional.
- A report is **eligible** for the score only if the reporter was a `joined` participant of that match and not marked no-show, has finished probation (6.4), and, when the target is a team, played on the opposing side. Ineligible reports still reach the admin queue with weight 0.
- One report per (reporter, target, match). The target is never told who reported them.
- `no_show` and `cheating_stats` do not feed the conduct score; they have their own rules in 6.3.

### 6.2 Conduct score

```latex
S_u = m_u \cdot \sum_{r \in R_u} s_r \cdot c_r \cdot i_r \cdot 2^{-\Delta t_r / 180}
```

| Factor | Rule |
| --- | --- |
| `s` severity | `abuse` 1, `discrimination` 2, `aggression` 3; `other` 0.5 |
| `c` reporter credibility | Per reporter, starts 1.0, range 0.2 to 1.5. +0.1 per report upheld by an admin, -0.25 per report rejected. Opposing team captain or co-admin reports get ×1.25 (capped at 1.5) |
| `i` independence | Reporters are in the same **cluster** if they share a current team or played together in 5 or more matches in the last 180 days. For each target, all reports from one cluster within 90 days share a total weight of 1: `i = 1 / k` for `k` reports from that cluster |
| Decay | Half-life 180 days (`Δt` in days since the match) |
| Retaliation | Report filed within 72 h after the target reported the reporter, or after the reporter received a Fair play rating of 30 or less from the target: weight ×0.5 and flagged for admin |
| `m` corroboration | 1.25 if the target's Fair play average is below 40 with 10 or more ratings in the last 90 days; else 1.0 |
| Admin decisions | Upheld report: `c` set to at least 1.0 for that report and frozen. Rejected report: removed from the score; reporter credibility drops |

The score is recomputed by a worker job on every new report, admin decision and nightly (for decay). The full breakdown per report is stored for the admin console.

### 6.3 Escalation

| Trigger | Action |
| --- | --- |
| `S` reaches 3 | Private warning (push + in-app notice with the conduct guidelines) |
| `S` reaches 5 | Automatic 7-day suspension |
| `S` reaches 8 | Automatic 30-day suspension + admin review item |
| Second 30-day suspension within 12 months | Automatic permanent ban |
| **Fast lane:** `aggression` reports from 3 or more different clusters in the same match | Immediate suspension pending review; admin alert with a 48 h review target |
| Admin decision | Warning, suspension or ban at any time, with reason |
| No-shows: 2 in 60 days (admin-marked or eligible `no_show` reports; late withdrawals count 0.5) | Warning |
| No-shows: 3 in 60 days | 14-day suspension |
| No-shows: 5 in 180 days | 60-day suspension and +1 added to `S` |
| `cheating_stats` reports on a result from 2 or more participants | Result frozen from leaderboards pending admin review |

- **No double punishment:** each sanction records `S` at the time it was applied. The next automatic step requires `S` to grow by at least 3 from new reports after that sanction.
- **Teams:** a team conduct score uses the same formula on team-targeted reports. `S` 5 → 30-day team suspension; second suspension within 12 months → team ban; 2 or more members banned within 90 days → admin review flag.
- Suspended users cannot join, create, report or rate; suspended teams are hidden and cannot post listings or challenge. Banned users cannot log in beyond the ban screen and appear as "Removed player" in other people's histories.
- Every sanction is notified by push + email and shown full-screen at next app open with reason category, duration and an appeal form.

### 6.4 Making removal stick

- **Device ban bits:** on iOS, Apple DeviceCheck stores two bits per device per developer team that persist across reinstalls ([Apple](https://developer.apple.com/documentation/devicecheck/dcdevice)); on Android, Play Integrity device recall (beta) stores three bits per device that survive reinstall and factory reset, kept 3 years after last access ([Android Developers](https://developer.android.com/google/play/integrity/device-recall)). Set a "banned device" bit on ban and clear it on unban; every signup and login reads the bits server-side, and a flagged device sends new signups to `pending_review`. App Attest / Play Integrity verdicts also reject requests from modified apps or emulators in production. SMS phone verification is not used (no SMS dependency); it can be added later behind a `PhoneVerifier` interface if evasion becomes a problem.
- **Probation:** until a user has 3 confirmed matches they cannot create public matches and their reports carry weight 0 (still reviewed).
- **Evasion checks:** keep salted hashes of verified email and device ID, also after ban or deletion. A signup matching a banned or suspended user's hashes goes to `pending_review`, not active.

### 6.5 Blocking

Any user can block another: the blocked user cannot see the blocker's created matches, join them, or message them; they are not shown each other in suggestions. Blocking is silent.

### 6.6 Admin review

- Queue order: fast-lane cases, then users who crossed a threshold, then by severity and age.
- Each case shows the conduct score breakdown (every report with `s`, `c`, `i`, decay, retaliation flag, cluster), the match context, chat evidence, Fair play trend and sanction history.
- Actions: uphold or reject reports (score recomputed, reporter credibility updated), warn, suspend, ban, lift a sanction, reset a reporter's credibility. All written to `audit_log`.

## 7. Super admin web console

A separate web app (`apps/admin`) for users with role `super_admin`, protected by password + mandatory TOTP 2FA; every action writes to `audit_log` with a required reason field for destructive actions.

| Area | Capabilities |
| --- | --- |
| Dashboard | Counts: active users, matches this week, open reports, pending courts, pending claims, last sync result |
| Courts | Search/filter/map; create manually; edit (creates field locks); approve/reject pending courts and user submissions; approve/reject owner claims; **remove** (soft delete + tombstone); restore removed court (deletes tombstone); merge duplicates (keeps one, tombstones the other's source ref); trigger **Re-sync** per source with a report of created/updated/skipped/stale |
| Users | Search by name/email; view profile, matches, reports received/sent, sanctions history; **suspend** (with end date), **unsuspend**, **ban**, **unban**; force logout; anonymise on GDPR request |
| Teams | Search; view members and reports; suspend/unsuspend, ban/unban; transfer captaincy in edge cases |
| Reports queue | Ordered by severity and age; see context (match, chat excerpt if reported from chat); uphold/reject; apply sanction from the same screen |
| Results | Leaderboard review flags (more than 10 goals, `cheating_stats` reports); void a result from stats |
| Content config | Edit match tags list, rating constants, quorum constants, sanction thresholds (stored in a `config` table, read by the API with caching) |
| Audit log | Filterable, read-only |

Admin APIs live under `/admin/v1/*`, never exposed to the mobile client; role checked server-side on every request.

## 8. Non-functional requirements

**Privacy and GDPR (EU, Spain first)**

- Data minimisation: store home area as a point rounded to about 500 m; live device location is used on-device for "near me" queries and sent per request, never stored.
- Birth date and gender are personal data shown only as derived aggregates (age bucket, gender mix). Gender is optional on the public profile.
- In-app: download my data (JSON export), delete my account (both app stores require in-app deletion), consent records for terms version and push.
- Privacy Policy, Terms and the adult-only clause are versioned; changing the version forces re-acceptance on next login.
- Processors (hosting, email, push, maps) listed in the privacy policy; data hosted in the EU.

**Security**

- OWASP ASVS level 2 as the checklist. Argon2id password hashing; rotating refresh tokens with reuse detection; rate limits on auth, search, join, report, rating endpoints.
- Authorization in the service layer: every mutation checks role (participant, match admin, team captain/co-admin, super admin).
- Share tokens and invite links are unguessable and revocable. Private matches return 404 (not 403) to unauthorised users.
- Uploads via signed URLs, image type and size checked, EXIF stripped.

**Performance and reliability**

- p95 API latency under 300 ms for search within 10 km on a dataset of 50,000 courts and 100,000 matches.
- Worker jobs are idempotent and retried with backoff; time-based transitions (match status, quorum expiry, listing expiry) are driven by scheduled jobs, never by client clocks.
- Daily database backups, 30-day retention, tested restore.

**Internationalisation and accessibility**

- es-ES default, en; all strings in `packages/i18n`; dates in 24 h format, EUR with Spanish formatting.
- Minimum touch target 44 pt, dynamic font sizes, screen reader labels on player cards and buttons, colour never the only signal (tier badges also have a text label).

**Observability**

- Structured JSON logs, OpenTelemetry traces, error tracking (Sentry or equivalent) on API, mobile and admin.

**Testing (required in every milestone)**

- Table-driven Go unit tests for every rule in sections 4.7, 4.8, 6.2, 6.3 (quorum, rating maths, sanctions), with boundary cases.
- API integration tests against a real Postgres + PostGIS (testcontainers).
- Mobile end-to-end flows with Maestro for each milestone's critical paths; admin end-to-end with Playwright.
- CI blocks merges on failing tests, lint (`golangci-lint`, ESLint, TypeScript strict) and OpenAPI drift.

## 9. API outline

REST under `/v1`, JSON, cursor pagination (`?cursor=&limit=`), errors as RFC 9457 problem details with a stable `code`. The full contract lives in `docs/openapi.yaml`; this is the minimum surface.

| Area | Endpoints |
| --- | --- |
| Auth | `POST /auth/signup`, `/auth/login`, `/auth/oauth/{apple,google}`, `/auth/refresh`, `/auth/logout`, `/auth/verify-email`, `/auth/password-reset` |
| Me | `GET/PATCH /me`, `DELETE /me`, `GET /me/export`, `POST /me/devices`, `GET /me/notifications`, `PATCH /me/notification-settings`, `GET /me/pending-actions` (results to confirm, matches to rate) , `POST /me/blocks/{userId}` |
| Users | `GET /users/{id}` (public profile + card), `GET /users/{id}/matches`, `GET /users/{id}/stats`, `GET /users?q=` |
| Teams | `POST /teams`, `GET/PATCH/DELETE /teams/{id}`, `GET /teams?q=&near=&format=`, `POST /teams/{id}/invites`, `POST /teams/{id}/join-requests`, `PATCH /teams/{id}/members/{userId}` (role), `DELETE /teams/{id}/members/{userId}`, `POST /teams/{id}/transfer-captaincy` |
| Matches | `POST /matches`, `GET/PATCH /matches/{id}`, `POST /matches/{id}/cancel`, `GET /matches?near=&radius=&from=&to=&format=&tags=&gender_mix=&age_bucket=&rating_band=&has_spots=`, `GET /matches/feed`, `GET /m/{shareToken}`, `POST /matches/{id}/join`, `POST /matches/{id}/leave`, `POST /matches/{id}/participants` (admin add), `PATCH /matches/{id}/participants/{userId}` (side, slot, no-show), \`POST /matches/{id}/invites/{inviteId}/accept |
| Results | `POST /matches/{id}/results`, `GET /matches/{id}/results/current`, `POST /results/{id}/confirm`, `POST /results/{id}/dispute` |
| Ratings | `GET /matches/{id}/ratable`, `POST /matches/{id}/ratings` (batch) |
| Listings | `POST /listings`, `GET /listings?type=&near=&format=&from=&to=`, `PATCH /listings/{id}`, `POST /listings/{id}/respond` |
| Challenges | `POST /challenges`, `GET /teams/{id}/challenges`, \`POST /challenges/{id}/accept |
| Leaderboards | \`GET /leaderboards?scope=city:{id} |
| Courts | `GET /courts?near=&radius=&access=&format=&surface=&q=`, `GET /courts/{id}`, `GET /courts/{id}/live-details` (Google enrichment, not stored), `POST /courts/suggestions`, `POST /courts/{id}/claims`, `POST /courts/{id}/report-info`, `GET /courts/{id}/availability?date=` (provider-backed only), `POST /courts/{id}/booking-requests` (native booking milestone) |
| Reports | `POST /reports` |
| Chat | `GET /matches/{id}/chat/messages`, `POST /matches/{id}/chat/messages`, WebSocket `GET /ws` (subscribe to match chat and roster events) |
| Admin | `/admin/v1/courts/*` (CRUD, approve, remove, restore, merge, sync), `/admin/v1/sync-runs`, `/admin/v1/users/*` (suspend, unsuspend, ban, unban, anonymise), `/admin/v1/teams/*`, `/admin/v1/reports/*`, `/admin/v1/results/*/void`, `/admin/v1/config`, `/admin/v1/audit-log` |

## 10. Milestones and Definition of Done

M1 is the launchable MVP: a stranger can sign up, find or create a match, find a pitch, play, and be protected by reporting and admin tools. Each later milestone adds one loop. Build in order; each DoD below is the stop condition for that milestone, on top of the general rules in section 0.

### M0. Foundations (not user-facing)

Scope: monorepo, CI, Docker Compose, Postgres + PostGIS migrations, Go API skeleton with health check, auth (email/password, Apple, Google), JWT/refresh, OpenAPI generation, generated TS client, Expo app shell with navigation and i18n, admin web shell with TOTP login, staging deployment, seed command.

- [ ] `docker compose up` starts API, worker, Postgres/PostGIS, object storage locally; `make seed` populates Madrid demo data
- [ ] Sign up, verify email, log in, refresh, log out work from the mobile app against staging
- [ ] Super admin can log in to the admin web with TOTP
- [ ] CI runs lint, unit, integration tests and OpenAPI drift check; all green
- [ ] iOS and Android development builds install and open

### M1. MVP: play a match, find a pitch, stay safe

Scope: sections 4.1, 4.2 (card without numbers, "NEW" badge), 4.4 (all), 4.5, 4.11 (subset: match invites, waitlist, edits, cancellations, reminders), 5.1 (OSM import + admin manual entry only), 5.2, 5.3 (without Google enrichment and booking providers other than `phone` and `external_link`), 6.1 to 6.6 for users (no-show ladder in M2, team scores in M3), 7 (Dashboard, Courts, Users, Reports queue, Content config for tags, Audit log), 8 (privacy, deletion, export).

- [ ] Under-18 birth date is rejected; adult attestation and terms version are stored; a device with the banned bit set (DeviceCheck or Play device recall) sends new signups to review
- [ ] A user creates a public match at a court and it appears in another user's feed, search and map within 10 km
- [ ] A private match never appears in search, feed or map for non-participants (automated test), and is joinable through its share link
- [ ] Admin-added players receive an invite and appear only after accepting
- [ ] Capacity, waitlist promotion and overlap blocking work (automated tests)
- [ ] Match card shows spots, age bucket and spread, gender mix label, tags, court access type; values verified by tests on fixtures
- [ ] Status transitions open → full → in\_progress → awaiting\_result happen via worker on time
- [ ] OSM import for Spain completes; nearby courts and name/place search return results in Madrid; phone button dials; OSM attribution visible
- [ ] Admin can create, edit (locks fields), remove (tombstone) and restore a court; re-sync does not re-create a removed court and does not overwrite locked fields (automated test)
- [ ] Conduct score matches 6.2 on fixtures (table-driven tests for severity, credibility, cluster independence, decay, retaliation); warning, 7-day and 30-day suspensions, repeat-offence ban and fast lane trigger as in 6.3; admin can lift; all in audit log
- [ ] Admin can suspend and unsuspend a user; a suspended user cannot join or create matches
- [ ] Block, data export and account deletion work end to end
- [ ] Maestro flows pass: signup, create match, join match, join via link, report user

### M2. Results, ratings, history

Scope: 4.2 (numbers and tiers), 4.7 (pickup matches), 4.8, 4.9 (history and personal stats, no leaderboards), 6.3 no-show ladder, related notifications.

- [ ] Admin submits score + scorers/assists; sums validated against score
- [ ] Every eligible participant gets a push and an in-app modal that reappears until answered or expired
- [ ] Quorum, dispute, re-submission and 72 h expiry behave exactly as 4.7 (table-driven tests on every threshold boundary)
- [ ] Only confirmed results change stats; unverified and disputed matches are labelled in history
- [ ] Ratings: 48 h window, one per pair per match, anonymous, Bayesian average with C = 5, "NEW" until 5 ratings, collusion cap (tests)
- [ ] Player card shows overall, categories and tier colour everywhere a user appears
- [ ] No-show marking and the warning/suspension ladder work (tests)

### M3. Teams, listings, challenges, captain chat

Scope: 4.3, 4.6, team-vs-team rules in 4.7, 4.10, team conduct scores in 6.3, admin Teams area, related notifications.

- [ ] Create team; invite and join-request flows; exactly one captain enforced by DB; co-admin permissions enforced server-side
- [ ] Captain "C" and co-admin "A" markers show on team page and on match rosters involving that team
- [ ] Listings board with all three types; auto-expiry and auto-close on fill
- [ ] Challenge button on team profile; accept creates a team-vs-team match with both rosters; counter-proposal and expiry work
- [ ] Team-vs-team quorum requires each side plus one admin per team (tests); team W/D/L updates on confirmation
- [ ] Captain chat is realtime, limited to admins/captains, reportable, push on new message
- [ ] Team suspension and ban thresholds work; suspended teams vanish from search and cannot challenge

### M4. Leaderboards and badges

Scope: leaderboards and badges in 4.9, `cheating_stats` handling, admin Results area.

- [ ] City and country leaderboards for goals, assists, matches, rating, per month/season/all time
- [ ] Eligibility filters applied (6 participants, 4 confirmers, 4 accounts older than 14 days); tests prove a self-made match with fake goals never reaches a leaderboard
- [ ] More than 10 goals in one match raises a review flag; admin can clear or void
- [ ] Badges awarded by job and shown on profile

### M5. Court enrichment and owners

Scope: Google Places live details, owner claims and owner-edited prices/hours/booking link, user court suggestions, Playtomic deep link, `BookingProvider` interface with `phone`, `external_link`, `playtomic_link` adapters.

- [ ] Court page shows live Google details with required attribution; DB contains only `place_id` from Google (test asserts no other Google fields persisted)
- [ ] Owners can claim a court; admin approves; owner edits lock fields
- [ ] Users can suggest a court; admin approves into the directory
- [ ] Booking button routes correctly per provider; "Call to check availability" shown when no availability source exists

### M6. Native booking requests

Scope: 5.4, owner portal (web, owner role), availability on court page, attach booking to match.

- [ ] Owner configures pitches, weekly slots, prices, blackout dates
- [ ] User sees free slots and sends a request; owner confirms or rejects; both notified
- [ ] Double booking impossible (exclusion constraint test)
- [ ] Confirmed booking can be linked to a match and shows on the match page

### M7 (future). Payments

Not to be built until requested. Foundations already present: nullable `price_per_player_cents` and `payment_mode` on match. Expected scope: split court fee among participants via a PSP with Strong Customer Authentication (EU), refunds on cancellation, venue payouts.

### M8 (future). Tournaments

Not to be built until requested. Foundations already present: nullable `tournament_id` on match, `tournament` tag. Expected scope: tournament entity, team registration, group stage and knockout brackets, fixtures auto-generated as team-vs-team matches, standings computed from confirmed results.

## 11. Open decisions

The coding LLM should implement the default shown and leave these configurable; the product owner decides before launch.

- [ ] **Email free-tier ceiling.** Brevo + Mailjet free plans give about 500 transactional emails/day combined; Brevo adds its branding on free emails. Move to a paid tier (or Amazon SES) before launch marketing pushes signups past that.
- [ ] **Gender policy enforcement.** Default: `men_only`/`women_only` are advisory with a confirmation modal. Alternative: hard enforcement by declared gender (needs a rule for `non_binary` and `prefer_not_to_say`).
- [ ] **Playtomic partnership.** Apply to the partner programme before M5 if in-app booking for Playtomic venues is wanted; until then only deep links.
- [ ] **Legal review before launch:** Terms and Privacy Policy (Spanish law, GDPR), adult self-attestation without ID checks, and the ODbL share-alike posture for the OSM-derived court layer.
- [ ] **Tuning constants:** rating prior (60) and weight (C = 5), quorum percentages, leaderboard eligibility, card tier thresholds, conduct score weights and escalation thresholds (6.2, 6.3). All in the `config` table.
- [ ] **Name clearance and brand: check "Pachanga" in the App Store, Google Play and the EUIPO/OEPM trademark registers (classes 9 and 41). As a common Spanish word for the very activity, a plain word mark may be refused as descriptive; plan a distinctive logo (figurative mark) or a store name such as "Pachanga: fútbol cerca de ti".**

## Sources

- [OpenStreetMap Foundation: Licence and Legal FAQ](https://osmfoundation.org/wiki/Licence_and_Legal_FAQ)
- [OpenStreetMap Foundation: Attribution Guideline](https://osmfoundation.org/wiki/Attribution)
- [Google Maps Platform: Policies and attributions for Places API](https://developers.google.com/maps/documentation/places/web-service/policies)
- [Playtomic Third Party API: Getting Started](https://third-party.playtomic.io/)
- [Playtomic Manager help: Playtomic API complete guide](https://helpmanager.playtomic.com/hc/en-gb/articles/38836515997073)
- [Playtomic Manager help: Third-party integrations and Playtomic Connect](https://helpmanager.playtomic.com/hc/en-gb/articles/47390286355089-Third-party-integrations-and-Playtomic-Connect)

* [DigitalOcean: Why is SMTP blocked?](https://docs.digitalocean.com/support/why-is-smtp-blocked/)
* [SendLayer: Best free SMTP servers compared (2026)](https://sendlayer.com/blog/best-free-smtp-servers/)
* [Apple: DeviceCheck DCDevice](https://developer.apple.com/documentation/devicecheck/dcdevice)
* [Android Developers: Play Integrity device recall (beta)](https://developer.android.com/google/play/integrity/device-recall)
