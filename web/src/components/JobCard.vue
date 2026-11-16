<script setup lang="ts">
import { computed, ref } from 'vue';

import { ApiError, api, type Job } from '../api';

const props = defineProps<{ job: Job }>();
const emit = defineEmits<{ changed: [job: Job] }>();

const canceling = ref(false);
const cancelError = ref('');

const statusLabel = computed(() => {
    const j = props.job;
    switch (j.status) {
        case 'queued':
            return j.queuePosition ? `В очереди · №${j.queuePosition}` : 'В очереди';
        case 'running':
            return 'Ищем посты…';
        case 'done':
            return 'Готово';
        case 'failed':
            return 'Ошибка';
        default:
            return 'Отменён';
    }
});

const progress = computed(() => Math.min(100, Math.round((props.job.postsSeen / props.job.maxItems) * 100)));

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
const created = computed(() => dateFmt.format(new Date(props.job.createdAt)));

const duration = computed(() => {
    const { startedAt, finishedAt } = props.job;
    if (!startedAt || !finishedAt) return '';
    const secs = Math.max(1, Math.round((Date.parse(finishedAt) - Date.parse(startedAt)) / 1000));
    return secs < 60 ? `${secs} с` : `${Math.floor(secs / 60)} мин ${secs % 60} с`;
});

async function cancel(): Promise<void> {
    canceling.value = true;
    cancelError.value = '';
    try {
        emit('changed', await api.cancelJob(props.job.id));
    } catch (e) {
        cancelError.value = e instanceof ApiError ? e.message : 'Не удалось отменить';
    } finally {
        canceling.value = false;
    }
}
</script>

<template>
    <article class="card job">
        <div class="head">
            <div class="title">
                <h3>{{ job.keyword }}</h3>
                <span class="meta muted">
                    {{ job.filter === 'recent' ? 'Свежие' : 'Популярные' }} · до {{ job.maxItems }} постов ·
                    {{ created }}
                </span>
            </div>
            <span class="badge" :class="job.status">{{ statusLabel }}</span>
        </div>

        <div v-if="job.status === 'running'" class="progress">
            <div class="bar"><div class="fill" :style="{ width: `${progress}%` }" /></div>
            <span class="muted">Найдено {{ job.postsSeen }} из {{ job.maxItems }}</span>
        </div>

        <div v-else-if="job.status === 'done'" class="result">
            <span>
                <strong>{{ job.postsCount }}</strong> постов
                <template v-if="job.newPostsCount !== null">
                    · <strong>{{ job.newPostsCount }}</strong> новых
                </template>
                <span v-if="duration" class="muted"> · {{ duration }}</span>
            </span>
            <a v-if="job.hasFile" class="btn small" :href="api.downloadUrl(job.id)" download>Скачать .xlsx</a>
        </div>

        <p v-else-if="job.status === 'failed'" class="error-box">{{ job.error || 'Неизвестная ошибка' }}</p>

        <div v-if="job.status === 'queued'" class="result">
            <span class="muted">Поиск начнётся автоматически, страницу можно закрыть</span>
            <button class="btn secondary small" :disabled="canceling" @click="cancel">Отменить</button>
        </div>
        <p v-if="cancelError" class="error-box">{{ cancelError }}</p>
    </article>
</template>

<style scoped>
.job {
    padding: 14px 16px;
}

.head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
}

.title {
    min-width: 0;
}

h3 {
    margin: 0;
    overflow-wrap: anywhere;
    font-size: 16px;
}

.meta {
    font-size: 13px;
}

.badge {
    flex-shrink: 0;
    padding: 3px 10px;
    border-radius: 999px;
    font-size: 13px;
    font-weight: 600;
    background: var(--surface-2);
    color: var(--muted);
}

.badge.queued {
    background: var(--warn-soft);
    color: var(--warn);
}

.badge.running {
    background: var(--accent-soft);
    color: var(--accent);
}

.badge.done {
    background: var(--ok-soft);
    color: var(--ok);
}

.badge.failed {
    background: var(--danger-soft);
    color: var(--danger);
}

.progress {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 12px;
    font-size: 13px;
}

.bar {
    flex: 1;
    height: 6px;
    border-radius: 999px;
    background: var(--surface-2);
    overflow: hidden;
}

.fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.4s ease;
}

.result {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px 12px;
    margin-top: 12px;
    font-size: 14px;
}

.error-box {
    margin: 12px 0 0;
    overflow-wrap: anywhere;
}
</style>
