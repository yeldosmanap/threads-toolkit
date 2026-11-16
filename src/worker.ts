/**
 * Queue worker - runs search jobs for the web service backend.
 *
 * Loop: claim a job from the backend -> run the search action -> send posts back.
 * Talks only to the backend's internal API (the backend owns the database and S3).
 * Runs ONE job at a time on purpose: all jobs share a single Threads session,
 * and parallel scraping on one account gets it blocked quickly.
 *
 * Env:
 *   BACKEND_URL                  e.g. http://backend:8080 (required)
 *   WORKER_TOKEN                 shared secret for /internal/* (required)
 *   WORKER_ID                    name shown in backend logs (default: hostname)
 *   POLL_INTERVAL_MS             idle wait between claims (default: 5000)
 *   THREADS_STORAGE_STATE_PATH   Playwright storageState.json of a logged-in Threads account (optional)
 *   PROXY_URLS                   comma-separated proxy URLs (optional)
 */

import { readFileSync } from 'node:fs';
import { hostname } from 'node:os';

import { Log, ProxyConfiguration } from 'crawlee';

import { postAction, searchAction } from './actions/index.js';
import type { ActionHooks, FilterType, PostInput, SearchInput, ThreadsPost } from './types.js';
import { sleep } from './utils/index.js';
import { buildContext, needsContext, pickComments, type PostPageItem } from './worker-context.js';

interface ClaimedJob {
    id: number;
    keyword: string;
    /** Project runs: search each alias in turn */
    keywords?: string[] | null;
    /** "comments": collect comments under postUrls (maxItems = comments per post) */
    kind?: 'search' | 'comments';
    postUrls?: string[] | null;
    filter: FilterType;
    maxItems: number;
    scrollDelay?: number;
    maxEmptyScrolls?: number;
    maxScrollTime?: number;
}

const log = new Log({ prefix: 'Worker' });

const BACKEND_URL = (process.env.BACKEND_URL ?? '').replace(/\/$/, '');
const WORKER_TOKEN = process.env.WORKER_TOKEN ?? '';
const WORKER_ID = process.env.WORKER_ID ?? hostname();
const POLL_INTERVAL_MS = Number(process.env.POLL_INTERVAL_MS ?? 5000);
const HEARTBEAT_MS = 20_000;
const PROGRESS_MIN_INTERVAL_MS = 3_000;

if (!BACKEND_URL || !WORKER_TOKEN) {
    log.error('BACKEND_URL and WORKER_TOKEN are required');
    process.exit(1);
}

let stopping = false;
function onStopSignal(signal: NodeJS.Signals): void {
    if (stopping) process.exit(1);
    stopping = true;
    log.info(`${signal} received - finishing current job, then exiting`);
}
process.on('SIGTERM', onStopSignal);
process.on('SIGINT', onStopSignal);

async function api(path: string, body?: unknown): Promise<Response> {
    return fetch(`${BACKEND_URL}/internal${path}`, {
        method: 'POST',
        headers: {
            Authorization: `Bearer ${WORKER_TOKEN}`,
            'Content-Type': 'application/json',
            'X-Worker-Id': WORKER_ID,
        },
        body: JSON.stringify(body ?? {}),
    });
}

async function apiOk(path: string, body?: unknown): Promise<void> {
    const res = await api(path, body);
    if (!res.ok) {
        throw new Error(`Backend ${path} -> ${res.status} ${await res.text().catch(() => '')}`);
    }
}

function loadStorageState(): Record<string, unknown> | undefined {
    const path = process.env.THREADS_STORAGE_STATE_PATH;
    if (!path) return undefined;
    try {
        // Read on every job so a refreshed session file is picked up without a restart
        return JSON.parse(readFileSync(path, 'utf8'));
    } catch (err) {
        log.warning('Could not read Threads session file, running without login', { path, error: String(err) });
        return undefined;
    }
}

const proxyUrls = (process.env.PROXY_URLS ?? '')
    .split(',')
    .map((u) => u.trim())
    .filter(Boolean);

/** Post pages opened for context per search job - each is one more request on the Threads account */
const CONTEXT_PER_JOB = Number(process.env.CONTEXT_PER_JOB ?? 15);

/** seen = unique posts so far; keywordIndex (1-based) / aliasSeen describe the alias being searched */
type Progress = {
    set(seen: number, keywordIndex?: number, aliasSeen?: number): void;
    stop(): void;
    /** The user asked (via the backend) to finish early with what has been collected */
    stopRequested(): boolean;
};

function startProgress(jobId: number): Progress {
    let lastSent = 0;
    let seen = 0;
    let keywordIndex: number | undefined;
    let aliasSeen: number | undefined;
    let stopRequested = false;
    const send = async (): Promise<void> => {
        lastSent = Date.now();
        try {
            const res = await api(`/jobs/${jobId}/progress`, { postsSeen: seen, keywordIndex, aliasSeen });
            if (!res.ok) throw new Error(`${res.status} ${await res.text().catch(() => '')}`);
            const body = (await res.json().catch(() => ({}))) as { stop?: boolean };
            if (body.stop && !stopRequested) {
                stopRequested = true;
                log.info('Stop requested by the user - finishing with what was collected', { id: jobId });
            }
        } catch (err) {
            log.warning('Progress update failed', { id: jobId, error: String(err) });
        }
    };
    // Heartbeat even when no new posts arrive (page loads, retries), so the backend
    // doesn't treat the job as abandoned - and so a stop request is noticed quickly
    const heartbeat = setInterval(() => {
        send().catch(() => undefined);
    }, HEARTBEAT_MS);
    return {
        set(n: number, index?: number, alias?: number) {
            seen = n;
            if (index !== undefined) keywordIndex = index;
            if (alias !== undefined) aliasSeen = alias;
            if (Date.now() - lastSent >= PROGRESS_MIN_INTERVAL_MS) send().catch(() => undefined);
        },
        stop: () => clearInterval(heartbeat),
        stopRequested: () => stopRequested,
    };
}

function hooks(push: (item: object) => void, onProgress?: (seen: number) => void, shouldStop?: () => boolean): ActionHooks {
    return {
        shouldStop,
        pushItem: async (item) => {
            push(item);
        },
        createProxyConfiguration: async () => (proxyUrls.length > 0 ? new ProxyConfiguration({ proxyUrls }) : undefined),
        onProgress,
    };
}

function sessionInput(): Pick<SearchInput, 'useCookies' | 'storageState'> {
    const storageState = loadStorageState();
    return { useCookies: Boolean(storageState), storageState };
}

/** Open a post page and return everything postAction found on it (main post + posts around it). */
async function openPostPage(
    url: string,
    maxReplies: number,
    scroll: Partial<PostInput>,
    jobLog: Log,
    shouldStop?: () => boolean,
): Promise<PostPageItem[]> {
    const items: PostPageItem[] = [];
    await postAction(
        { action: 'post', postUrl: url, maxItems: maxReplies, ...sessionInput(), ...scroll },
        jobLog,
        hooks((item) => items.push(item as PostPageItem), undefined, shouldStop),
    );
    return items;
}

async function runSearch(job: ClaimedJob, progress: Progress, jobLog: Log): Promise<ThreadsPost[]> {
    const keywords = job.keywords && job.keywords.length > 0 ? job.keywords : [job.keyword];
    const posts = new Map<string, ThreadsPost>();

    for (const [i, keyword] of keywords.entries()) {
        if (stopping) break;
        if (progress.stopRequested()) {
            jobLog.info('Stopped early: skipping the remaining aliases', { skipped: keywords.slice(i) });
            break;
        }
        const before = posts.size;
        const index = keywords.length > 1 ? i + 1 : undefined;
        const input: SearchInput = {
            action: 'search',
            keyword,
            filter: job.filter,
            maxItems: job.maxItems,
            ...sessionInput(),
            scrollDelay: job.scrollDelay,
            maxEmptyScrolls: job.maxEmptyScrolls,
            maxScrollTime: job.maxScrollTime,
        };
        await searchAction(
            input,
            jobLog,
            hooks(
                (item) => {
                    const p = item as ThreadsPost;
                    if (!posts.has(p.id)) posts.set(p.id, p);
                },
                // Scrolling parses a bit more than maxItems; only maxItems per alias are kept
                (seen) => {
                    const kept = Math.min(seen, job.maxItems);
                    progress.set(before + kept, index, kept);
                },
                progress.stopRequested,
            ),
        );
        jobLog.info('Keyword done', { keyword, found: posts.size - before, total: posts.size });
        // Exact count after de-duplication against earlier aliases
        progress.set(posts.size, index, posts.size - before);
    }

    // Comments and parts of multi-part posts make sense only with what they refer to.
    // Skipped when the user asked to finish early - they want the result now.
    const candidates = progress.stopRequested() ? [] : [...posts.values()].filter(needsContext).slice(0, CONTEXT_PER_JOB);
    for (const post of candidates) {
        if (stopping || progress.stopRequested()) break;
        try {
            const page = await openPostPage(post.url, 8, { scrollDelay: 1000, maxEmptyScrolls: 1, maxScrollTime: 10 }, jobLog);
            const context = buildContext(post, page);
            if (context) (post as ThreadsPost & { context?: string }).context = context;
        } catch (err) {
            // Deleted or unavailable posts are common - label them without context
            jobLog.warning('Context fetch failed', { post: post.id, error: String(err) });
        }
    }
    if (candidates.length > 0) jobLog.info('Context collected', { candidates: candidates.length });
    return [...posts.values()];
}

async function runComments(job: ClaimedJob, progress: Progress, jobLog: Log): Promise<ThreadsPost[]> {
    const out: ThreadsPost[] = [];
    for (const url of job.postUrls ?? []) {
        if (stopping || progress.stopRequested()) break;
        try {
            const page = await openPostPage(
                url,
                job.maxItems,
                { scrollDelay: 2500, maxEmptyScrolls: 3, maxScrollTime: 60 },
                jobLog,
                progress.stopRequested,
            );
            const { main, comments } = pickComments(page);
            if (!main) continue;
            // The post a comment answers is what makes "😂" or "у меня тоже" understandable
            const context = `@${main.author.username}: ${main.content.trim()}`.slice(0, 3000);
            for (const c of comments) out.push({ ...c, parentId: main.id, context } as ThreadsPost);
            progress.set(out.length);
        } catch (err) {
            jobLog.warning('Comments fetch failed', { url, error: String(err) });
        }
    }
    return out;
}

async function runJob(job: ClaimedJob): Promise<void> {
    log.info('Job started', { id: job.id, kind: job.kind ?? 'search', keyword: job.keyword, keywords: job.keywords, maxItems: job.maxItems });
    const progress = startProgress(job.id);
    const jobLog = log.child({ prefix: `Job ${job.id}` });

    try {
        const posts = job.kind === 'comments' ? await runComments(job, progress, jobLog) : await runSearch(job, progress, jobLog);
        progress.stop();
        await apiOk(`/jobs/${job.id}/complete`, { posts });
        log.info('Job done', { id: job.id, posts: posts.length });
    } catch (err) {
        progress.stop();
        const message = err instanceof Error ? err.message : String(err);
        log.error('Job failed', { id: job.id, error: message });
        await apiOk(`/jobs/${job.id}/fail`, { error: message.slice(0, 2000) }).catch((e) =>
            log.error('Could not report failure to backend', { id: job.id, error: String(e) }),
        );
    }
}

log.info('Worker started', { backend: BACKEND_URL, id: WORKER_ID, proxies: proxyUrls.length });

while (!stopping) {
    let job: ClaimedJob | undefined;
    try {
        const res = await api('/jobs/claim');
        if (res.status === 200) {
            job = ((await res.json()) as { job: ClaimedJob }).job;
        } else if (res.status !== 204) {
            log.warning('Claim failed', { status: res.status, body: await res.text().catch(() => '') });
        }
    } catch (err) {
        log.warning('Backend unreachable', { error: String(err) });
    }

    if (job) {
        await runJob(job);
    } else {
        await sleep(POLL_INTERVAL_MS);
    }
}

log.info('Worker stopped');
