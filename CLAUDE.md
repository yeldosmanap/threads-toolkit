# CLAUDE.md

Apify Actor (TypeScript, Crawlee + Playwright) that scrapes Threads (threads.com, Meta). General Apify conventions live in `AGENTS.md`; this file covers what is specific to this repo.

## Commands

```bash
npm run start:dev      # run locally via tsx (reads storage/key_value_stores/default/INPUT.json)
apify run              # same, through Apify CLI
npm run build          # tsc -> dist/ (Docker runs dist/main.js)
npm run lint           # eslint (@apify config + prettier)
npm test               # node:test via tsx: unit + fixture-based parser tests (launches headless Chromium)
node --import tsx --test test/utils.test.ts   # single test file
npm run smoke:live     # LIVE scraping of real Threads pages (network, slow, can flake)
npm run smoke:auth     # needs THREADS_STORAGE_STATE_PATH or THREADS_STORAGE_STATE_JSON
```

Before finishing a change, run `npm run build`, `npm run lint` and `npm test`. Run smoke tests only when the change touches scraping/parsing behaviour against the live site, and tell the user that they hit the real network.

## Architecture

- `src/main.ts` — entry. Reads input; if any of `keywords`/`usernames`/`tags`/`postUrls` is an array → **batch mode** (expands into single tasks, runs them with a simple worker pool, `concurrency` default 2, failures are logged and don't fail the run). Otherwise validates `action` and dispatches to one action.
- `src/actions/{search,hashtag,profile,post}.ts` — one `PlaywrightCrawler` per action, each a single page (`maxRequestsPerCrawl: 1`). Each file repeats the same scaffolding: proxy from `Actor.createProxyConfiguration`, `preNavigationHooks` (block images/fonts/media, request delay, exponential backoff on retry), optional cookie/localStorage injection from `storageState` in `postPageCreateHooks` (Threads origins only; sessionStorage is ignored). A change to this scaffolding usually has to be made in all four files.
  - Every crawler **must** use its own queue from `openIsolatedQueue()` and be run via `runSingleRequestCrawl()` (`src/utils/crawler-helpers.ts`). With the shared default queue, batch tasks after the first hit `maxRequestsPerCrawl: 1` without running and parallel tasks steal each other's requests. `runSingleRequestCrawl` drops the queue and throws if the request failed, which is how batch mode counts failures.
  - Search/hashtag posts are pushed with `source` + `keyword`/`tag` so batch output can be split by query.
  - `search`/`hashtag` → `https://www.threads.com/search?...` (hashtag is a search with `#tag`), scroll, extract posts.
  - `profile` → `/@username`; pushes a profile item (`type: 'profile'`) and, if `includePosts`, posts with `source: 'profile_posts'`. `location`/`joinedDate` are only fetched via the "About this profile" dialog when authenticated, otherwise `null`.
  - `post` → normalizes URL (`threads.net` → `threads.com`), pushes the main post, then replies with `source: 'reply'`, `parentId`.
- `src/utils/parser.ts` — **all DOM parsing** (posts, single post, profile, About dialog, relative time, `1.2K`/`3M` stat numbers). Pages are parsed with `page.evaluate`/locators; post containers are found by walking up from `a[href*="/post/"]` links. Some fields (display names, profile fields) are recovered from embedded JSON payloads in `<script type="application/json">`.
- `src/utils/selectors.ts` — central selector map. Selectors are multilingual (English + Chinese UI text, e.g. `讚`, `留言`, `大頭貼照`); keep both languages when editing.
- `src/utils/page-helpers.ts` — error detection (login wall, rate limit, explicit "No results", error page; multilingual patterns matched against visible `innerText` only — inline `<script>` JSON contains words like "blocked"), `scrollForPosts`, `validatePost` (requires content ≥3 chars, author, timestamp — invalid posts are skipped, not pushed), `validateProfile` (only username required; marks `partial` + `missingFields`), `DEFAULT_RATE_LIMIT_CONFIG`, `RateLimitError`.
- `src/types.ts` — input/output types. Output shape: `ThreadsPost` (`id, url, author, content, timestamp, stats{likes,replies,reposts,shares}, images, videos, links`) and `ProfileData`.

Search URLs: Threads opens the **Top** tab by default; **Recent** is `&filter=recent` (there is no `filter=top`).

Scrolling (`scrollForPosts`) returns the posts it parsed along the way — Threads virtualizes the feed and unloads posts that scrolled out of view, so parsing once at the end loses most of them. Already-parsed IDs are skipped via `extractPostsFromPage(page, max, skipIds)`. Logged-out, the feed is in an inner scroll container; logged-in, the document scrolls and the only scrollable div is the sidebar — so the scroller must be a scrollable element that contains post links, else `window`. Timing comes from the `scrollDelay` / `maxEmptyScrolls` / `maxScrollTime` inputs; `requestHandlerTimeoutFor()` keeps the handler timeout above `maxScrollTime`. Logged-out users get a fixed ~15–20 posts followed by a "Log in for more" banner; scrolling stops early when it is detected. Logged-in pace is roughly 6 posts per ~14 s scroll step.

Video URLs are captured from network responses in a pre-navigation listener and handed to the page via `window.__threadsVideoRequests` before parsing.

## Schemas and inputs

- `.actor/input_schema.json` drives the Apify Console form. Note that some inputs accepted by the code are **not** in the schema: batch fields (`keywords`, `usernames`, `tags`, `postUrls`, `concurrency`) and `rateLimitConfig` (forwarded to every batch task). `outputFormat` exists in types but is unused. Keep the schema, `src/types.ts`, `.actor/dataset_schema.json` and both READMEs (`README.md`, `README.zh-TW.md`) in sync when adding or renaming fields.
- Default action is `profile` with `username: "zuck"` — Apify's automated Actor test runs with defaults, so defaults must produce a successful run without login.

## Testing notes

- `test/parser.integration.test.ts` loads inline HTML fixtures into real Chromium (`page.setContent`) and runs the parser. When fixing a parser bug, add or extend a fixture here instead of relying only on live smoke.
- `scripts/run-action-smoke.mjs` calls actions directly, stubbing `Dataset.pushData` and disabling proxy, and prints a JSON summary; `smoke-live.mjs` asserts on it (e.g. `@zuck` profile, a fixed post ID). These assertions depend on live data and may need updating if the content changes.

## Gotchas

- Playwright is pinned to `1.59.1` and must match the Docker base image tag `apify/actor-node-playwright-chrome:22-1.59.1` (the Dockerfile checks this). Upgrade both together; ask before changing the Dockerfile or installing packages.
- ESM with `NodeNext`: relative imports in `src/` must use the `.js` extension.
- Style: Prettier, 4 spaces, single quotes, print width 120.
- Threads changes its DOM often; prefer fixes in `selectors.ts`/`parser.ts` with fallbacks over action-specific hacks.
- `.actor/actor.json` → `meta.generatedBy` was deliberately set by the owner (commit `0a615b2`); do not overwrite it despite the instruction in `AGENTS.md`.
- Never commit `storageState` / cookies or anything under `storage/`.

## Web service (backend/, web/, src/worker.ts)

A multi-user web app around the scraper. `docker compose up --build` starts everything; site at http://localhost:3000.

```
web (Vue 3 + TS, nginx) --/api--> backend (Go) --> Postgres (users, sessions, jobs = queue, posts)
                                     |   ^                         ^
                                     v   | /internal (Bearer WORKER_TOKEN)
                              S3 (private bucket, xlsx)    worker (src/worker.ts, this repo's scraper image)
```

- **Worker** (`src/worker.ts`, `npm run worker:dev`) polls `POST /internal/jobs/claim`, runs `searchAction` with `ActionHooks` (collects posts in memory instead of the Apify Dataset), heartbeats `/progress`, then sends all posts to `/complete` (or `/fail`). It runs ONE job at a time on purpose — every job uses the same Threads session (`THREADS_STORAGE_STATE_PATH`, mounted from `./.auth`). Actions must stay platform-agnostic: never call `Actor.*`/`Dataset.*` directly in `src/actions`, go through `resolveHooks()`.
- **Backend** (`backend/`, Go 1.23, chi + pgx + goose + excelize + aws-sdk-go-v2). Only the backend touches the DB and S3. All SQL lives in `internal/store`; migrations are embedded in `internal/db/migrations` and applied on start.
  - Queue = `jobs` table: claim uses `FOR UPDATE SKIP LOCKED` with fair ordering (user served least recently first). A maintenance loop requeues `running` jobs without heartbeat for `JOB_STALE_AFTER`, failing them after `JOB_MAX_ATTEMPTS`.
  - "New posts": `job_posts.is_new` = no earlier job of the same user with the same `keyword_norm` (lower-cased, whitespace-collapsed) found the post. Posts are global rows; stats keep latest values.
  - On `/complete` the backend builds the xlsx (sheets «Все посты» + «Новые») and uploads it to S3 under an ASCII-only key `users/{uid}/jobs/{jid}/posts.xlsx`. Downloads are streamed through `GET /api/jobs/{id}/download` after an ownership check — the bucket is never exposed and no presigned URLs are used (SeaweedFS rejected AWS SDK v2 presigned signatures).
  - Auth: email + bcrypt password, opaque session token in an HttpOnly SameSite=Lax cookie (only its SHA-256 is stored). Every non-GET `/api` request must be `Content-Type: application/json` (CSRF guard). Login/register are rate-limited per IP. Per-user quotas: `QUOTA_ACTIVE_JOBS`, `QUOTA_DAILY_JOBS`, `MAX_ITEMS_LIMIT`.
  - API error shape: `{"error": {"code": "...", "message": "<Russian, user-facing>"}}`.
  - Posts viewer: `GET /api/jobs/{id}/posts?onlyNew&q&sort&page&pageSize` (`internal/store/posts.go`). Sort keys are whitelisted in `postSorts` (never interpolate user input into ORDER BY); search is ILIKE with `escapeLike` so `%`/`_` are literal.
  - `isFirstRun` on jobs (no earlier run of the same search saved posts): every post would be "new", so the UI hides new-markers/filter and the xlsx omits the «Новые» sheet and flag column.
  - Recurring searches (`schedules` table, `internal/schedule`, `store/schedules.go`): presets only — every 6/12/24/168 h or daily at HH:MM in the user's IANA timezone (tz database is embedded via `time/tzdata`; distroless has none). The `scheduler` loop in `cmd/server` calls `RunDueSchedules` every 30 s: due schedules (`FOR UPDATE SKIP LOCKED`) become normal jobs with `schedule_id`, using the same quotas; an occurrence is skipped (reason in `last_error`) if the previous run is still active or a quota is full. `Next()` is computed from now — no catch-up after downtime. Interval schedules run immediately on creation; resuming keeps the planned slot unless it already passed. Limits: `QUOTA_SCHEDULES`, `MIN_SCHEDULE_HOURS`.
  - **Projects + analysis** (`projects`, `post_labels`; `internal/analysis`, `internal/pipeline`): a project = company + aliases + official accounts + competitors + notes. A project run is a job with `keywords` (the worker searches each alias, dedupes) and `keyword_norm = project:<id>`. With `ANTHROPIC_API_KEY` set, `/complete` only marks the job scraped (`analysis_status = pending`); `pipeline.Analyzer` (one goroutine) classifies posts with Claude (`claude-opus-5-5`, effort `low`, structured outputs, server-side `fallbacks: "default"`), stores labels per (project, post) keyed by `analysis.PromptVersion` (a later run re-bills only new posts; editing the prompt or project relabels), then builds the xlsx (sheets «Все посты», «Негативные», «Новые» + a separate «Удаленные_посты» file for official/ad/spam/offtopic/other-country posts). Without a key project runs behave like plain searches.
  - Classifier rules agreed with the client live in `internal/analysis/prompt.go` (sentiment is toward the company; leaving = negative, switching to = positive, all-operators complaint = neutral, bare tag / demand for reply = negative, employer posts = offtopic). Measure every prompt change with `go run ./cmd/evalsentiment -files <labeled.xlsx,...>` (paid API calls; results cached per model/effort/prompt version in `eval-out/`, which holds post texts and is git-ignored). Baseline on 558 hand-labeled Beeline posts: 93.2% accuracy, macro-F1 ≈0.81 (Opus 5.5, low); Haiku 5.5 was notably worse.
  - **Context & comments**: the worker opens post pages (`postAction`) for comments / thread parts / bare tags (`src/worker-context.ts`, max `CONTEXT_PER_JOB`) and sends the parent post or the other thread parts as `context`, which the classifier sees in a `<context>` block. After a project run is analyzed, the analyzer queues a system job (`kind = 'comments'`, `parent_job_id`, `post_urls`) for up to `COMMENTS_PER_RUN` negative posts with replies; comments are saved with `posts.parent_id` and labeled like posts. System jobs are excluded from quotas and job lists (`kind = 'search'` filters). Comments are sent with their parent post as `context`. When a comments job's analysis finishes, the analyzer rebuilds the parent run's xlsx (`refreshParentFiles`): main file gets a «Комментарии» sheet (with «К посту» link), the removed file gets «Удаленные_комментарии».
  - **Stop early** (`POST /api/jobs/{id}/stop`, `jobs.stop_requested`): the progress heartbeat answers `{"stop": true}`; the worker then breaks out of scrolling (`ActionHooks.shouldStop` → `scrollForPosts`), skips the remaining aliases and the context fetch, and completes normally with what it has, so analysis/exports/comments run as usual. On a run that already finished scraping, stop applies to its comments job (running → stop, queued → canceled).
  - Project progress: the worker reports `keywordIndex` (1-based) + `aliasSeen`; then `posts_seen` is set directly (it may drop slightly after de-duplication), otherwise it only grows.
  - Schedules can run a project (`schedules.project_id`, ON DELETE CASCADE): `RunDueSchedules` inserts a project job using the project's *current* name/aliases; `keyword_norm` = `project:<id>`, so scheduled and manual runs share new-post tracking.
- **Web** (`web/`): Vue 3 + vue-router, no UI library, plain CSS with light/dark tokens in `src/styles.css`. `npm run dev` proxies `/api` to `127.0.0.1:8080` (backend port published by compose). Russian UI. Jobs list polls every 3 s while a job is active, 30 s otherwise. `/jobs/:id` shows a job's posts; filters live in the URL query (`new`, `q`, `sort`, `page`). `/projects` manages projects and starts runs. For analyzed runs the job page shows the sentiment bar, aspects (sorted by negative count, clickable filters), comment sentiment, a «В аналитике / Отсеянные» switch; post cards show the label, aspects, the model's reason and expandable comments. Russian names for labels live in `src/labels.ts` (and `internal/export/labels.go` for xlsx). Post text is rendered as text nodes only (search highlighting splits strings, no `v-html`); Threads images are not hotlinked (counts only).
- **Local S3** is SeaweedFS (`chrislusf/seaweedfs`) — official MinIO images are no longer published. Any S3 works via `S3_*` env.
- Compose Postgres is published on **127.0.0.1:55432** (5432 is often taken by a local Postgres).

```bash
cd backend && go test ./...                      # unit tests
cd backend && TEST_DATABASE_URL='postgres://threads:threads@127.0.0.1:55432/threads_test?sslmode=disable' go test ./...  # + DB integration (wipes threads_test)
cd web && npm run build                          # vue-tsc typecheck + build
```
