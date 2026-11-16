<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { ApiError, api, type Job, type Post, type PostSort } from '../api';
import PostCard from '../components/PostCard.vue';

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

const job = ref<Job | null>(null);
const posts = ref<Post[]>([]);
const total = ref(0);
const loading = ref(true);
const error = ref('');
const appliedSearch = ref(search.value.trim());

const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)));
const hasFilters = computed(() => onlyNew.value || appliedSearch.value !== '');
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
    void router.replace({ query });
}

function reload(): void {
    syncUrl();
    void loadPosts();
}

// Changing a filter goes back to page 1; the page watcher then does the single reload
watch([onlyNew, sort, appliedSearch], () => {
    if (page.value !== 1) page.value = 1;
    else reload();
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
    if (job.value.isFirstRun && onlyNew.value) {
        onlyNew.value = false; // the watcher reloads
        return;
    }
    await loadPosts();
});

onBeforeUnmount(() => clearTimeout(searchTimer));

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
            <div class="toolbar card">
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
                Это первый поиск по слову «{{ job.keyword }}». Запустите его ещё раз позже — новые посты будут
                отмечены и их можно будет отфильтровать.
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
            <PostCard v-for="p in posts" :key="p.id" :post="p" :highlight="appliedSearch" :show-new="showNew" />
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
    .toolbar {
        position: static;
    }

    .search,
    .sort {
        width: 100%;
    }
}
</style>
