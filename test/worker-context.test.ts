import assert from 'node:assert/strict';
import test from 'node:test';

import type { ThreadsPost } from '../src/types.js';
import { buildContext, needsContext, pickComments } from '../src/worker-context.js';

function post(id: string, author: string, content: string, timestamp: string): ThreadsPost {
    return {
        id,
        url: `https://www.threads.com/@${author}/post/${id}`,
        author: { username: author, displayName: author, profileUrl: `https://www.threads.com/@${author}` },
        content,
        timestamp,
        stats: { likes: 0, replies: 0, reposts: 0, shares: 0 },
    };
}

void test('needsContext detects comments, thread parts and bare tags', () => {
    assert.equal(needsContext({ content: 'Replying to @zhannat_kappar\n\nНу это отсылка к цитате' }), true);
    assert.equal(needsContext({ content: '@beeline_kz 2/2' }), true);
    assert.equal(needsContext({ content: '@beeline_kz 1 / 4' }), true);
    assert.equal(needsContext({ content: '@beeline_kz !!!!!!!' }), true);
    assert.equal(needsContext({ content: '@beeline_kz жду ваших комментариев.' }), true);
    assert.equal(needsContext({ content: 'Поддержка работает 24/7, а толку ноль' }), false);
    assert.equal(
        needsContext({ content: '@beeline_kz что за проблемы с мобильным интернетом? услуги полностью оплачены, уже пятый день' }),
        false,
    );
    assert.equal(needsContext({ content: 'Билайн опять не ловит в подъезде' }), false);
});

void test('buildContext takes the same author thread parts and the replied-to post, oldest first', () => {
    const target = post('p1', 'mira', '@beeline_kz 1/4', '2026-11-02T10:00:00Z');
    const page = [
        target,
        post('p3', 'mira', 'Вместо содействия пришлось столкнуться с грубостью', '2026-11-02T10:00:02Z'),
        post('p2', 'mira', 'Хочу выразить недовольство работой менеджера центра Beeline', '2026-11-02T10:00:01Z'),
        post('x', 'stranger', 'не относится', '2026-11-02T11:00:00Z'),
    ];
    assert.equal(
        buildContext(target, page),
        '@mira: Хочу выразить недовольство работой менеджера центра Beeline\n---\n@mira: Вместо содействия пришлось столкнуться с грубостью',
    );

    const reply = post('r', 'aza', 'Replying to @zhannat_kappar\n\nНу это отсылка к цитате астронавта', '2026-11-15T10:00:00Z');
    const replyPage = [post('root', 'zhannat_kappar', 'В ютубе постоянно реклама Билайн', '2026-11-15T09:00:00Z'), reply];
    assert.equal(buildContext(reply, replyPage), '@zhannat_kappar: В ютубе постоянно реклама Билайн');
    assert.equal(buildContext(reply, [reply]), '');
});

void test('pickComments keeps posts below the main post only', () => {
    const items = [
        { ...post('main', 'a', 'Билайн не работает', '2026-11-16T10:00:00Z') },
        { ...post('older', 'root', 'родительский пост', '2026-11-16T09:00:00Z'), source: 'reply', parentId: 'main' },
        { ...post('c1', 'b', 'у меня тоже', '2026-11-16T10:05:00Z'), source: 'reply', parentId: 'main' },
        { ...post('c1', 'b', 'у меня тоже', '2026-11-16T10:05:00Z'), source: 'reply', parentId: 'main' },
        { ...post('c2', 'c', 'а у меня норм', '2026-11-16T10:06:00Z'), source: 'reply', parentId: 'main' },
    ];
    const { main, comments } = pickComments(items);
    assert.equal(main?.id, 'main');
    assert.deepEqual(
        comments.map((c) => c.id),
        ['c1', 'c2'],
    );
    assert.equal('source' in comments[0], false);
});
