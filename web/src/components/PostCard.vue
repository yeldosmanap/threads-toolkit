<script setup lang="ts">
import { computed, ref } from 'vue';

import type { Post } from '../api';

const props = withDefaults(defineProps<{ post: Post; highlight?: string; showNew?: boolean }>(), { showNew: true });

const expanded = ref(false);
const isLong = computed(() => props.post.content.length > 420 || props.post.content.split('\n').length > 7);

const dateFmt = new Intl.DateTimeFormat('ru-RU', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
});
const published = computed(() => (props.post.publishedAt ? dateFmt.format(new Date(props.post.publishedAt)) : ''));

const numberFmt = new Intl.NumberFormat('ru-RU', { notation: 'compact' });
const fmt = (n: number): string => numberFmt.format(n);

/** Split text into plain/matched parts for highlighting - rendered as text nodes, never as HTML */
const parts = computed(() => {
    const text = props.post.content;
    const needle = props.highlight?.trim().toLowerCase();
    if (!needle) return [{ text, match: false }];
    const out: { text: string; match: boolean }[] = [];
    const lower = text.toLowerCase();
    let i = 0;
    while (i < text.length) {
        const at = lower.indexOf(needle, i);
        if (at < 0) {
            out.push({ text: text.slice(i), match: false });
            break;
        }
        if (at > i) out.push({ text: text.slice(i, at), match: false });
        out.push({ text: text.slice(at, at + needle.length), match: true });
        i = at + needle.length;
    }
    return out;
});
</script>

<template>
    <article class="card post" :class="{ fresh: showNew && post.isNew }">
        <header class="head">
            <div class="author">
                <a :href="post.author.url || `https://www.threads.com/@${post.author.username}`" target="_blank" rel="noopener noreferrer">
                    @{{ post.author.username }}
                </a>
                <span v-if="post.author.verified" class="verified" title="Верифицирован" aria-label="Верифицирован">✓</span>
                <span v-if="post.author.name && post.author.name !== post.author.username" class="muted name">
                    {{ post.author.name }}
                </span>
            </div>
            <span v-if="showNew && post.isNew" class="new-badge">Новый</span>
        </header>

        <p class="content" :class="{ clamped: isLong && !expanded }">
            <template v-for="(part, i) in parts" :key="i">
                <mark v-if="part.match">{{ part.text }}</mark>
                <template v-else>{{ part.text }}</template>
            </template>
        </p>
        <button v-if="isLong" class="link-btn" type="button" @click="expanded = !expanded">
            {{ expanded ? 'Свернуть' : 'Показать полностью' }}
        </button>

        <footer class="foot">
            <span class="stats muted">
                <span title="Лайки">♥ {{ fmt(post.likes) }}</span>
                <span title="Ответы">💬 {{ fmt(post.replies) }}</span>
                <span title="Репосты">↻ {{ fmt(post.reposts) }}</span>
                <span v-if="post.shares" title="Поделились">↗ {{ fmt(post.shares) }}</span>
                <span v-if="post.imagesCount" title="Фото">🖼 {{ post.imagesCount }}</span>
                <span v-if="post.videosCount" title="Видео">▶ {{ post.videosCount }}</span>
            </span>
            <span class="right">
                <time v-if="post.publishedAt" class="muted" :datetime="post.publishedAt">{{ published }}</time>
                <a :href="post.url" target="_blank" rel="noopener noreferrer">Открыть в Threads ↗</a>
            </span>
        </footer>
    </article>
</template>

<style scoped>
.post {
    padding: 14px 16px;
}

.post.fresh {
    border-left: 3px solid var(--accent);
}

.head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
}

.author {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 8px;
    min-width: 0;
}

.author a {
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
    overflow-wrap: anywhere;
}

.author a:hover {
    text-decoration: underline;
}

.verified {
    color: var(--accent);
    font-weight: 700;
}

.name {
    font-size: 14px;
}

.new-badge {
    flex-shrink: 0;
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--accent-soft);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
}

.content {
    margin: 8px 0 0;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
}

.content.clamped {
    display: -webkit-box;
    -webkit-line-clamp: 7;
    -webkit-box-orient: vertical;
    overflow: hidden;
}

mark {
    border-radius: 3px;
    background: #fde68a;
    color: #111;
}

.link-btn {
    margin-top: 4px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
    font: inherit;
    font-size: 14px;
    cursor: pointer;
}

.foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px 16px;
    margin-top: 10px;
    font-size: 13px;
}

.stats {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 14px;
}

.right {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
}

.right a {
    text-decoration: none;
}
</style>
