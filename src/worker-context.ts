/**
 * Pure helpers for the queue worker: which posts need surrounding context, how to build it
 * from the posts on a post page, and which posts on a post page are its comments.
 */

import type { ThreadsPost } from './types.js';

/** Item pushed by postAction: the main post, or a post found below/above it (source: 'reply') */
export type PostPageItem = ThreadsPost & { source?: string; parentId?: string };

const REPLY_PREFIX = /^\s*Replying to @([\w.]+)/i;
// "1/4", "2 / 2" - part N of a multi-part post (N <= M <= 20, so "24/7" doesn't match)
const CHAIN_MARKER = /(?:^|\s)(\d{1,2})\s?\/\s?(\d{1,2})(?=\s|$|[.!?,)])/;
const MAX_CONTEXT_CHARS = 3000;

function isChainPart(text: string): boolean {
    const m = text.match(CHAIN_MARKER);
    if (!m) return false;
    const [n, total] = [Number(m[1]), Number(m[2])];
    return n >= 1 && total >= 2 && n <= total && total <= 20;
}

/**
 * A post needs context when it is a comment under someone else's post, a part of a
 * multi-part post, or so short that it is just a tag ("@beeline_kz !!!").
 */
export function needsContext(post: Pick<ThreadsPost, 'content'>): boolean {
    const text = post.content.trim();
    if (REPLY_PREFIX.test(text) || isChainPart(text)) return true;
    const withoutMentions = text.replace(/@[\w.]+/g, '').trim();
    return /@[\w.]+/.test(text) && withoutMentions.length <= 25;
}

function byTime(a: ThreadsPost, b: ThreadsPost): number {
    return (Date.parse(a.timestamp) || 0) - (Date.parse(b.timestamp) || 0);
}

/**
 * Context for a post = the other parts of the same author's thread and the post it replies to,
 * oldest first, as "@author: text" blocks. Empty if nothing relevant was on the page.
 */
export function buildContext(target: ThreadsPost, pagePosts: ThreadsPost[]): string {
    const author = target.author.username.toLowerCase();
    const replyTo = target.content.match(REPLY_PREFIX)?.[1]?.toLowerCase();
    const related = pagePosts
        .filter((p) => p.id !== target.id)
        .filter((p) => {
            const a = p.author.username.toLowerCase();
            return a === author || (replyTo !== undefined && a === replyTo);
        })
        .sort(byTime)
        .slice(0, 6);
    let text = related.map((p) => `@${p.author.username}: ${p.content.trim()}`).join('\n---\n');
    if (text.length > MAX_CONTEXT_CHARS) text = `${text.slice(0, MAX_CONTEXT_CHARS)}…`;
    return text;
}

/**
 * Comments of a post from the items postAction pushed for its page: everything below the
 * main post (not the main post itself and not the posts it replies to, which are older).
 */
export function pickComments(items: PostPageItem[]): { main?: ThreadsPost; comments: ThreadsPost[] } {
    const main = items.find((i) => i.source !== 'reply');
    if (!main) return { comments: [] };
    const mainTime = Date.parse(main.timestamp) || 0;
    const seen = new Set<string>([main.id]);
    const comments: ThreadsPost[] = [];
    for (const item of items) {
        if (item.source !== 'reply' || seen.has(item.id)) continue;
        if ((Date.parse(item.timestamp) || 0) < mainTime) continue;
        seen.add(item.id);
        const { source: _source, parentId: _parentId, ...post } = item;
        comments.push(post);
    }
    return { main, comments };
}
