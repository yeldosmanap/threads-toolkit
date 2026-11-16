<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';

import { ApiError, api, type Job, type Limits } from '../api';
import JobCard from '../components/JobCard.vue';
import SearchForm from '../components/SearchForm.vue';

const jobs = ref<Job[]>([]);
const limits = ref<Limits | null>(null);
const loading = ref(true);
const loadError = ref('');

const hasActive = computed(() => jobs.value.some((j) => j.status === 'queued' || j.status === 'running'));

async function refresh(): Promise<void> {
    try {
        const [list, lim] = await Promise.all([api.listJobs(), api.limits()]);
        jobs.value = list;
        limits.value = lim;
        loadError.value = '';
    } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) {
            loadError.value = e instanceof ApiError ? e.message : 'Не удалось загрузить поиски';
        }
    } finally {
        loading.value = false;
    }
}

// Poll fast while something is queued/running, slowly otherwise; pause in background tabs
let timer: ReturnType<typeof setTimeout> | undefined;
function schedule(): void {
    clearTimeout(timer);
    timer = setTimeout(
        async () => {
            if (document.visibilityState === 'visible') await refresh();
            schedule();
        },
        hasActive.value ? 3000 : 30000,
    );
}

function onVisible(): void {
    if (document.visibilityState === 'visible') {
        void refresh().then(schedule);
    }
}

function onCreated(job: Job): void {
    jobs.value = [job, ...jobs.value];
    void refresh().then(schedule);
}

function onChanged(job: Job): void {
    jobs.value = jobs.value.map((j) => (j.id === job.id ? job : j));
    void refresh();
}

onMounted(async () => {
    await refresh();
    schedule();
    document.addEventListener('visibilitychange', onVisible);
});

onBeforeUnmount(() => {
    clearTimeout(timer);
    document.removeEventListener('visibilitychange', onVisible);
});
</script>

<template>
    <h1 class="page-title">Поиск постов в Threads</h1>
    <SearchForm :limits="limits" @created="onCreated" />

    <h2 class="section-title">Мои поиски</h2>
    <p v-if="loadError" class="error-box">{{ loadError }}</p>
    <p v-else-if="loading" class="muted">Загрузка…</p>
    <div v-else-if="jobs.length === 0" class="card empty">
        <p>Здесь появятся ваши поиски.</p>
        <p class="muted">
            Введите ключевое слово выше — поиск встанет в очередь и выполнится на сервере, даже если закрыть
            страницу. Готовый результат можно скачать в Excel.
        </p>
    </div>
    <div v-else class="list">
        <JobCard v-for="job in jobs" :key="job.id" :job="job" @changed="onChanged" />
    </div>
</template>

<style scoped>
.page-title {
    margin: 0 0 16px;
    font-size: 24px;
}

.section-title {
    margin: 32px 0 12px;
    font-size: 17px;
}

.list {
    display: grid;
    gap: 10px;
}

.empty {
    padding: 20px;
}

.empty p {
    margin: 0 0 6px;
}
</style>
