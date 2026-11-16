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

import { searchAction } from './actions/index.js';
import type { FilterType, SearchInput, ThreadsPost } from './types.js';
import { sleep } from './utils/index.js';

interface ClaimedJob {
    id: number;
    keyword: string;
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

async function runJob(job: ClaimedJob): Promise<void> {
    log.info('Job started', { id: job.id, keyword: job.keyword, filter: job.filter, maxItems: job.maxItems });

    const posts: ThreadsPost[] = [];
    let lastProgressSent = 0;
    let postsSeen = 0;

    const sendProgress = async (): Promise<void> => {
        lastProgressSent = Date.now();
        await apiOk(`/jobs/${job.id}/progress`, { postsSeen }).catch((err) =>
            log.warning('Progress update failed', { id: job.id, error: String(err) }),
        );
    };
    // Heartbeat even when no new posts arrive (page load, retries), so the backend
    // doesn't treat the job as abandoned
    const heartbeat = setInterval(() => {
        sendProgress().catch(() => undefined);
    }, HEARTBEAT_MS);

    const storageState = loadStorageState();
    const input: SearchInput = {
        action: 'search',
        keyword: job.keyword,
        filter: job.filter,
        maxItems: job.maxItems,
        useCookies: Boolean(storageState),
        storageState,
        scrollDelay: job.scrollDelay,
        maxEmptyScrolls: job.maxEmptyScrolls,
        maxScrollTime: job.maxScrollTime,
    };

    try {
        await searchAction(input, log.child({ prefix: `Job ${job.id}` }), {
            pushItem: async (item) => {
                posts.push(item as ThreadsPost);
            },
            createProxyConfiguration: async () =>
                proxyUrls.length > 0 ? new ProxyConfiguration({ proxyUrls }) : undefined,
            onProgress: (seen) => {
                postsSeen = seen;
                if (Date.now() - lastProgressSent >= PROGRESS_MIN_INTERVAL_MS) {
                    sendProgress().catch(() => undefined);
                }
            },
        });
        clearInterval(heartbeat);
        await apiOk(`/jobs/${job.id}/complete`, { posts });
        log.info('Job done', { id: job.id, posts: posts.length });
    } catch (err) {
        clearInterval(heartbeat);
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
