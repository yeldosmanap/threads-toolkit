<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { ApiError, api, type Job, type Post, type PostSort, type Sentiment, type Summary } from '../api';
import PostCard from '../components/PostCard.vue';
import SentimentBar from '../components/SentimentBar.vue';
import { ASPECTS, aspectLabel, CATEGORY } from '../labels';

const props = defineProps<{ id: string }>();
const route = useRoute();
const router = useRouter();
const jobId = computed(() => Number(props.id));

const PAGE_SIZE = 50;
const SORTS: { value: PostSort; label: string }[] = [
    { value: 'newest', label: 'Сначала свежие' },
    { value: 'oldest', label: 'Сначала старые' },
    { value: 'likes', label: 'Больше лайков' },
    { value: 'replies', label: 'Больше ответов' },
    { value: 'reposts', label: 'Больше репостов' },
];

// Filters live in the URL so reload/back/share keep them
const onlyNew = ref(route.query.new === '1');
const search = ref(typeof route.query.q === 'string' ? route.query.q : '');
const sort = ref<PostSort>(SORTS.some((s) => s.value === route.query.sort) ? (route.query.sort as PostSort) : 'newest');
const page = ref(Math.max(1, Number(route.query.page) || 1));
const sentiment = ref<Sentiment | ''>(['negative', 'neutral', 'positive'].includes(String(route.query.s)) ? (route.query.s as Sentiment) : '');
const aspect = ref(ASPECTS.some((a) => a.value === route.query.a) ? String(route.query.a) : '');
const view = ref<'kept' | 'removed'>(route.query.view === 'removed' ? 'removed' : 'kept');
const summary = ref<Summary | null>(null);

const job = ref<Job | null>(null);
const posts = ref<Post[]>([]);
const total = ref(0);
const loading = ref(true);
const error = ref('');
const appliedSearch = ref(search.value.trim());

const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)));
const hasFilters = computed(() => onlyNew.value || appliedSearch.value !== '' || !!sentiment.value || !!aspect.value);
// Project runs with a finished (or partly finished) analysis
const analyzed = computed(() => job.value?.analysisStatus === 'done' || job.value?.analysisStatus === 'failed');
const removedTotal = computed(() => Object.values(summary.value?.excluded ?? {}).reduce((a, b) => a + b, 0));
const topAspects = computed(() =>
    Object.entries(summary.value?.aspects ?? {})
        .map(([key, c]) => ({ key, negative: c.negative ?? 0, total: (c.negative ?? 0) + (c.neutral ?? 0) + (c.positive ?? 0) }))
        .sort((x, y) => y.negative - x.negative || y.total - x.total)
        .slice(0, 8),
);
const maxAspect = computed(() => Math.max(1, ...topAspects.value.map((a) => a.total)));
const hasComments = computed(() => Object.values(summary.value?.comments ?? {}).some((n) => n > 0));
// On the first run of a search every post is "new" - hide the flag and the filter
const showNew = computed(() => !!job.value && !job.value.isFirstRun);

let requestSeq = 0;
async function loadPosts(): Promise<void> {
    const seq = ++requestSeq;
    loading.value = true;
    error.value = '';
    try {
        const res = await api.listPosts(jobId.value, {
            onlyNew: onlyNew.value,
            sentiment: sentiment.value,
            aspect: aspect.value,
            view: analyzed.value ? view.value : 'all',
            q: appliedSearch.value,
            sort: sort.value,
            page: page.value,
            pageSize: PAGE_SIZE,
        });
        if (seq !== requestSeq) return; // a newer request is in flight
        posts.value = res.posts;
        total.value = res.total;
    } catch (e) {
        if (seq === requestSeq) error.value = e instanceof ApiError ? e.message : 'Не удалось загрузить посты';
    } finally {
        if (seq === requestSeq) loading.value = false;
    }
}

function syncUrl(): void {
    const query: Record<string, string> = {};
    if (onlyNew.value) query.new = '1';
    if (appliedSearch.value) query.q = appliedSearch.value;
    if (sort.value !== 'newest') query.sort = sort.value;
    if (page.value > 1) query.page = String(page.value);
    if (sentiment.value) query.s = sentiment.value;
    if (aspect.value) query.a = aspect.value;
    if (view.value === 'removed') query.view = 'removed';
    void router.replace({ query });
}

function reload(): void {
    syncUrl();
    void loadPosts();
}

// Changing a filter goes back to page 1; the page watcher then does the single reload
watch([onlyNew, sort, appliedSearch, sentiment, aspect, view], () => {
    if (page.value !== 1) page.value = 1;
    else reload();
});
// Label filters apply to posts in the analytics only
watch(view, (v) => {
    if (v === 'removed') {
        sentiment.value = '';
        aspect.value = '';
    }
});
watch(page, () => {
    reload();
    window.scrollTo({ top: 0, behavior: 'smooth' });
});

// Debounced text search
let searchTimer: ReturnType<typeof setTimeout> | undefined;
watch(search, (value) => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
        appliedSearch.value = value.trim();
    }, 350);
});

onMounted(async () => {
    try {
        job.value = await api.getJob(jobId.value);
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Поиск не найден';
        loading.value = false;
        return;
    }
    if (analyzed.value) {
        api.summary(jobId.value)
            .then((sum) => (summary.value = sum))
            .catch(() => undefined);
    }
    if (job.value.isFirstRun && onlyNew.value) {
        onlyNew.value = false; // the watcher reloads
        return;
    }
    await loadPosts();
});

onBeforeUnmount(() => clearTimeout(searchTimer));

function showRemoved(): void {
    sentiment.value = '';
    aspect.value = '';
    view.value = 'removed';
}

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' });
</script>

<template>
    <RouterLink to="/" class="back">← Мои поиски</RouterLink>

    <template v-if="job">
        <div class="title-row">
            <div>
                <h1>{{ job.keyword }}</h1>
                <p class="muted sub">
                    {{ job.filter === 'recent' ? 'Свежие' : 'Популярные' }} · {{ dateFmt.format(new Date(job.createdAt)) }}
                    <template v-if="job.postsCount !== null">
                        · {{ job.postsCount }} постов<template v-if="showNew">, из них {{ job.newPostsCount }} новых</template>
                    </template>
                </p>
            </div>
            <a v-if="job.hasFile" class="btn" :href="api.downloadUrl(job.id)" download>Скачать .xlsx</a>
        </div>

        <div v-if="job.status !== 'done'" class="card notice">
            <template v-if="job.status === 'queued' || job.status === 'running'">
                Поиск ещё выполняется — посты появятся здесь, когда он закончится.
            </template>
            <template v-else-if="job.status === 'failed'">Поиск завершился с ошибкой: {{ job.error }}</template>
            <template v-else>Поиск был отменён.</template>
        </div>

        <template v-else>
            <section v-if="analyzed && summary" class="card analytics">
                <div class="an-head">
                    <h2>Тональность</h2>
                    <span class="muted">{{ summary.kept }} постов в аналитике<template v-if="removedTotal"> · {{ removedTotal }} отсеяно</template></span>
                </div>
                <SentimentBar :counts="summary.sentiment" selectable :selected="sentiment" @select="(s) => { view = 'kept'; sentiment = s; }" />

                <div v-if="topAspects.length" class="aspects">
                    <h3>О чём пишут <span class="muted">(сортировка по негативу)</span></h3>
                    <button
                        v-for="asp in topAspects"
                        :key="asp.key"
                        type="button"
                        :class="['asp-row', { active: aspect === asp.key }]"
                        @click="view = 'kept'; aspect = aspect === asp.key ? '' : asp.key"
                    >
                        <span class="asp-name">{{ aspectLabel(asp.key) }}</span>
                        <span class="asp-bar">
                            <span class="asp-total" :style="{ width: `${(asp.total / maxAspect) * 100}%` }">
                                <span class="asp-neg" :style="{ width: asp.total ? `${(asp.negative / asp.total) * 100}%` : '0' }" />
                            </span>
                        </span>
                        <span class="asp-num"><strong>{{ asp.negative }}</strong> / {{ asp.total }}</span>
                    </button>
                </div>

                <div v-if="hasComments" class="an-row">
                    <h3>Комментарии к негативным постам</h3>
                    <SentimentBar :counts="summary.comments" compact />
                </div>

                <p v-if="removedTotal" class="an-removed muted">
                    Отсеяно:
                    <template v-for="(n, key, i) in summary.excluded" :key="key">{{ i ? ', ' : '' }}{{ CATEGORY[key] ?? key }} — {{ n }}</template>.
                    <button class="link-btn" type="button" @click="showRemoved">Показать отсеянные</button>
                </p>
            </section>

            <div class="toolbar card">
                <div v-if="analyzed" class="segmented" role="group" aria-label="Какие посты показывать">
                    <button type="button" :class="{ active: view === 'kept' }" @click="view = 'kept'">В аналитике</button>
                    <button type="button" :class="{ active: view === 'removed' }" @click="view = 'removed'">
                        Отсеянные<template v-if="removedTotal"> ({{ removedTotal }})</template>
                    </button>
                </div>
                <select v-if="analyzed && view === 'kept'" v-model="sentiment" class="sort" aria-label="Тональность">
                    <option value="">Любая тональность</option>
                    <option value="negative">Негатив</option>
                    <option value="neutral">Нейтрал</option>
                    <option value="positive">Позитив</option>
                </select>
                <select v-if="analyzed && view === 'kept'" v-model="aspect" class="sort" aria-label="Аспект">
                    <option value="">Все темы</option>
                    <option v-for="asp in ASPECTS" :key="asp.value" :value="asp.value">{{ asp.label }}</option>
                </select>
                <div v-if="showNew" class="segmented" role="group" aria-label="Какие посты показывать">
                    <button type="button" :class="{ active: !onlyNew }" @click="onlyNew = false">Все</button>
                    <button type="button" :class="{ active: onlyNew }" @click="onlyNew = true">
                        Только новые
                    </button>
                </div>
                <input
                    v-model="search"
                    class="search"
                    type="text"
                    placeholder="Поиск по тексту или автору"
                    maxlength="100"
                    aria-label="Поиск по тексту или автору"
                />
                <select v-model="sort" class="sort" aria-label="Сортировка">
                    <option v-for="s in SORTS" :key="s.value" :value="s.value">{{ s.label }}</option>
                </select>
            </div>

            <p v-if="!showNew" class="hint muted">
                <template v-if="job.projectId">Это первый запуск проекта «{{ job.keyword }}».</template>
                <template v-else>Это первый поиск по слову «{{ job.keyword }}».</template>
                Запустите его ещё раз позже — новые посты будут отмечены и их можно будет отфильтровать.
            </p>
            <p class="count muted">
                <template v-if="!loading || posts.length">
                    {{ hasFilters ? `Найдено: ${total}` : `Всего: ${total}` }}
                    <template v-if="pages > 1"> · страница {{ page }} из {{ pages }}</template>
                </template>
            </p>
        </template>
    </template>

    <p v-if="error" class="error-box">{{ error }}</p>

    <template v-if="job?.status === 'done'">
        <div v-if="!loading && posts.length === 0 && !error" class="card notice">
            <template v-if="hasFilters">По этим условиям постов нет.</template>
            <template v-else>В этом поиске нет постов.</template>
        </div>

        <div class="list" :class="{ dim: loading }">
            <PostCard v-for="p in posts" :key="p.id" :post="p" :highlight="appliedSearch" :show-new="showNew" :job-id="jobId" />
        </div>

        <nav v-if="pages > 1" class="pager" aria-label="Страницы">
            <button class="btn secondary small" :disabled="page <= 1 || loading" @click="page--">← Назад</button>
            <span class="muted">{{ page }} / {{ pages }}</span>
            <button class="btn secondary small" :disabled="page >= pages || loading" @click="page++">Вперёд →</button>
        </nav>
    </template>
</template>

<style scoped>
.back {
    display: inline-block;
    margin-bottom: 12px;
    font-size: 14px;
    text-decoration: none;
}

.title-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 16px;
}

h1 {
    margin: 0;
    font-size: 24px;
    overflow-wrap: anywhere;
}

.sub {
    margin: 4px 0 0;
    font-size: 14px;
}

.notice {
    padding: 16px;
}

.analytics {
    margin-bottom: 12px;
    padding: 16px;
}

.an-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-bottom: 10px;
}

.analytics h2 {
    margin: 0;
    font-size: 17px;
}

.analytics h3 {
    margin: 0 0 8px;
    font-size: 14px;
}

.an-head .muted {
    font-size: 13px;
}

.aspects,
.an-row {
    margin-top: 18px;
}

.asp-row {
    display: grid;
    grid-template-columns: 170px 1fr 70px;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 4px 6px;
    border: 0;
    border-radius: 6px;
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    text-align: left;
    cursor: pointer;
}

.asp-row:hover,
.asp-row.active {
    background: var(--surface-2);
}

.asp-bar {
    height: 8px;
}

.asp-total {
    display: block;
    height: 100%;
    overflow: hidden;
    border-radius: 999px;
    background: #94a3b8;
    opacity: 0.85;
}

.asp-neg {
    display: block;
    height: 100%;
    background: var(--danger);
}

.asp-num {
    text-align: right;
}

.an-removed {
    margin: 16px 0 0;
    font-size: 13px;
}

.toolbar {
    position: sticky;
    top: 8px;
    z-index: 1;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    padding: 10px;
}

.search {
    flex: 1;
    min-width: 180px;
}

.sort {
    min-height: 40px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
}

.hint {
    margin: 12px 0 0;
    font-size: 14px;
}

.count {
    min-height: 1.5em;
    margin: 12px 0 8px;
    font-size: 14px;
}

.list {
    display: grid;
    gap: 10px;
    transition: opacity 0.15s;
}

.list.dim {
    opacity: 0.55;
}

.pager {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 16px;
    margin-top: 20px;
}

.error-box {
    margin: 12px 0;
}

@media (max-width: 520px) {
    .asp-row {
        grid-template-columns: 1fr 60px;
    }

    .asp-bar {
        grid-column: 1 / -1;
        grid-row: 2;
    }

    .toolbar {
        position: static;
    }

    .search,
    .sort {
        width: 100%;
    }
}
</style>
