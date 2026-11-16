/**
 * Crawler run helpers - shared across all actions
 *
 * Every action runs its own single-request PlaywrightCrawler. In batch mode several
 * of them run in one process, so each one must get its own request queue: sharing the
 * default queue makes later crawlers see the earlier handled request, hit
 * `maxRequestsPerCrawl: 1` before starting, and lets parallel crawlers steal each
 * other's requests.
 */

import { Actor } from 'apify';
import type { PlaywrightCrawler } from 'crawlee';
import { Dataset, RequestQueue } from 'crawlee';

import type { ActionHooks } from '../types.js';

/**
 * Fill in platform defaults for hooks the caller did not provide.
 * Defaults are resolved at call time, so tests/smoke runners can still stub
 * `Dataset.pushData` / `Actor.createProxyConfiguration`.
 */
export function resolveHooks(hooks: ActionHooks = {}): Required<ActionHooks> {
    return {
        pushItem: hooks.pushItem ?? (async (item) => Dataset.pushData(item as Record<string, unknown>)),
        createProxyConfiguration:
            hooks.createProxyConfiguration ?? (async (config) => Actor.createProxyConfiguration(config)),
        onProgress: hooks.onProgress ?? (() => undefined),
    };
}

/**
 * Open a uniquely named request queue for a single crawler run.
 * Always pair with `runSingleRequestCrawl`, which drops it afterwards.
 */
export async function openIsolatedQueue(label: string): Promise<RequestQueue> {
    const suffix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
    return RequestQueue.open(`threads-${label}-${suffix}`);
}

/**
 * Run a crawler over a single URL using its isolated queue, then drop the queue.
 * Throws if the request failed after all retries, so callers (and batch mode)
 * see the failure instead of a silent empty result.
 */
export async function runSingleRequestCrawl(
    crawler: PlaywrightCrawler,
    requestQueue: RequestQueue,
    url: string,
    what: string,
): Promise<void> {
    try {
        const { requestId } = await requestQueue.addRequest({ url });
        const stats = await crawler.run();

        if (stats.requestsFailed > 0) {
            const request = await requestQueue.getRequest(requestId);
            const messages = request?.errorMessages ?? [];
            const lastError = messages.length > 0 ? messages[messages.length - 1].split('\n')[0] : undefined;
            throw new Error(`${what} failed: ${lastError ?? 'request failed after all retries'}`);
        }
    } finally {
        await requestQueue.drop().catch(() => undefined);
    }
}
