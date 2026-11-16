<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';

import { ApiError, api, type Job, type Summary } from '../api';
import { confirmDialog } from '../confirm';
import SentimentBar from './SentimentBar.vue';

const props = defineProps<{ job: Job }>();
const emit = defineEmits<{ changed: [job: Job]; deleted: [id: number] }>();

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

// Project runs search their aliases one after another: progress covers all of them
const aliasCount = computed(() => props.job.keywords?.length ?? 0);
const currentAlias = computed(() => {
    const i = props.job.progressKeywordIndex;
    return aliasCount.value > 1 && i ? { index: i, keyword: props.job.keywords?.[i - 1] ?? '' } : null;
});
const progress = computed(() => {
    const j = props.job;
    if (currentAlias.value) {
        const done = currentAlias.value.index - 1 + Math.min(1, (j.progressAliasSeen ?? 0) / j.maxItems);
        return Math.min(100, Math.round((done / aliasCount.value) * 100));
    }
    return Math.min(100, Math.round((j.postsSeen / (j.maxItems * Math.max(1, aliasCount.value))) * 100));
});

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
const created = computed(() => dateFmt.format(new Date(props.job.createdAt)));

const duration = computed(() => {
    const { startedAt, finishedAt } = props.job;
    if (!startedAt || !finishedAt) return '';
    const secs = Math.max(1, Math.round((Date.parse(finishedAt) - Date.parse(startedAt)) / 1000));
    return secs < 60 ? `${secs} с` : `${Math.floor(secs / 60)} мин ${secs % 60} с`;
});

const isProject = computed(() => props.job.projectId !== null || (props.job.keywords?.length ?? 0) > 0);
const analyzing = computed(
    () => props.job.status === 'done' && (props.job.analysisStatus === 'pending' || props.job.analysisStatus === 'running'),
);
const analysisPct = computed(() =>
    props.job.postsCount ? Math.min(100, Math.round((props.job.analyzedCount / props.job.postsCount) * 100)) : 0,
);
const commentsText = computed(() => {
    switch (props.job.commentsState) {
        case 'queued':
        case 'running':
            return 'собираем комментарии к негативным постам…';
        case 'analyzing':
            return 'анализируем комментарии…';
        case 'done':
            return 'комментарии к негативным постам собраны';
        case 'failed':
            return 'не удалось собрать комментарии';
        case 'canceled':
            return 'сбор комментариев отменён';
        default:
            return '';
    }
});

// Sentiment summary for analyzed project runs
const summary = ref<Summary | null>(null);
async function loadSummary(): Promise<void> {
    if (props.job.analysisStatus !== 'done' && props.job.analysisStatus !== 'failed') return;
    try {
        summary.value = await api.summary(props.job.id);
    } catch {
        summary.value = null;
    }
}
onMounted(loadSummary);
watch(() => [props.job.analysisStatus, props.job.commentsState], loadSummary);
const removedCount = computed(() => Object.values(summary.value?.excluded ?? {}).reduce((a, b) => a + b, 0));

// Finish early: the worker stops and sends what it has; analysis and files follow as usual
const stopping = ref(false);
const collectingComments = computed(() => props.job.commentsState === 'queued' || props.job.commentsState === 'running');
async function stop(what: 'search' | 'comments'): Promise<void> {
    const answer = await confirmDialog(
        what === 'search'
            ? {
                  title: isProject.value ? 'Остановить и проанализировать?' : 'Остановить поиск?',
                  message: isProject.value
                      ? 'Всё, что уже собрано, будет проанализировано и сохранено в файл. Оставшиеся упоминания будут пропущены.'
                      : 'Найденные посты будут сохранены в файл.',
                  confirmText: 'Остановить',
              }
            : {
                  title: 'Остановить сбор комментариев?',
                  message: 'Уже собранные комментарии будут проанализированы и добавлены в файл.',
                  confirmText: 'Остановить',
              },
    );
    if (answer !== 'confirm') return;
    stopping.value = true;
    cancelError.value = '';
    try {
        emit('changed', await api.stopJob(props.job.id));
    } catch (e) {
        cancelError.value = e instanceof ApiError ? e.message : 'Не удалось остановить';
    } finally {
        stopping.value = false;
    }
}

const deleting = ref(false);
async function remove(): Promise<void> {
    const running = props.job.status === 'running';
    const answer = await confirmDialog({
        title: `Удалить поиск «${props.job.keyword}»?`,
        message: running
            ? 'Он сейчас выполняется: поиск будет остановлен, собранные посты не сохранятся.\nЕсли нужно сохранить собранное — используйте «Остановить».'
            : 'Поиск будет удалён из истории вместе с файлами и комментариями. Это нельзя отменить.',
        confirmText: 'Удалить',
        danger: true,
    });
    if (answer !== 'confirm') return;
    deleting.value = true;
    cancelError.value = '';
    try {
        await api.deleteJob(props.job.id);
        emit('deleted', props.job.id);
    } catch (e) {
        cancelError.value = e instanceof ApiError ? e.message : 'Не удалось удалить';
        deleting.value = false;
    }
}

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
                    <span v-if="isProject" class="tag">проект</span>
                    <RouterLink v-if="job.scheduleId" to="/schedules" class="tag">по расписанию</RouterLink>
                    {{ job.filter === 'recent' ? 'Свежие' : 'Популярные' }} · до {{ job.maxItems }} постов<template v-if="job.keywords?.length"> на каждое из {{ job.keywords.length }} упоминаний</template> ·
                    {{ created }}
                </span>
            </div>
            <span class="head-right">
                <span class="badge" :class="analyzing ? 'running' : job.status">{{ analyzing ? 'Анализ тональности…' : statusLabel }}</span>
                <button
                    class="icon-btn"
                    type="button"
                    :disabled="deleting"
                    title="Удалить поиск"
                    aria-label="Удалить поиск"
                    @click="remove"
                >
                    ✕
                </button>
            </span>
        </div>

        <div v-if="job.status === 'running'" class="progress">
            <div class="bar"><div class="fill" :style="{ width: `${progress}%` }" /></div>
            <span v-if="currentAlias" class="muted">
                Упоминание {{ currentAlias.index }} из {{ aliasCount }} · «{{ currentAlias.keyword }}» · собрано {{ job.postsSeen }}
            </span>
            <span v-else class="muted">Найдено {{ job.postsSeen }}</span>
        </div>
        <div v-if="job.status === 'running'" class="result">
            <span v-if="job.stopRequested" class="muted">Останавливаем — сохраняем то, что уже собрано…</span>
            <span v-else class="muted">Можно не ждать конца — собранное будет {{ isProject ? 'проанализировано' : 'сохранено' }}</span>
            <button v-if="!job.stopRequested" class="btn secondary small" :disabled="stopping" @click="stop('search')">
                {{ isProject ? 'Остановить и проанализировать' : 'Остановить и сохранить' }}
            </button>
        </div>

        <div v-else-if="analyzing" class="progress">
            <div class="bar"><div class="fill" :style="{ width: `${analysisPct}%` }" /></div>
            <span class="muted">Размечено {{ job.analyzedCount }} из {{ job.postsCount }}</span>
        </div>

        <template v-else-if="job.status === 'done'">
            <div v-if="summary && summary.analyzed > 0" class="summary">
                <SentimentBar :counts="summary.sentiment" compact />
            </div>
            <div class="result">
                <span>
                    <strong>{{ summary ? summary.kept : job.postsCount }}</strong> постов
                    <template v-if="removedCount"> · <span class="muted">{{ removedCount }} отсеяно</span></template>
                    <template v-if="job.isFirstRun"> · <span class="muted">первый запуск</span></template>
                    <template v-else-if="job.newPostsCount !== null"> · <strong>{{ job.newPostsCount }}</strong> новых</template>
                    <span v-if="duration" class="muted"> · {{ duration }}</span>
                </span>
                <span class="actions">
                    <RouterLink class="btn secondary small" :to="{ name: 'job', params: { id: job.id } }">
                        {{ summary ? 'Аналитика и посты' : 'Посмотреть посты' }}
                    </RouterLink>
                    <a v-if="job.hasFile" class="btn small" :href="api.downloadUrl(job.id)" download>Скачать .xlsx</a>
                </span>
            </div>
            <p v-if="job.stopRequested" class="note muted">⏹ Поиск остановлен досрочно — в результатах то, что успели собрать</p>
            <p v-if="commentsText" class="note muted">
                💬 {{ commentsText }}
                <button v-if="collectingComments" class="link-btn" type="button" :disabled="stopping" @click="stop('comments')">
                    остановить
                </button>
            </p>
            <p v-if="job.analysisStatus === 'failed'" class="warn">{{ job.analysisError || 'Анализ завершился с ошибкой' }}</p>
            <p v-if="job.hasRemovedFile" class="note">
                <a :href="api.downloadUrl(job.id, true)" download>Скачать «Удаленные_посты» (.xlsx)</a>
            </p>
        </template>

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

.tag {
    margin-right: 6px;
    padding: 1px 7px;
    border-radius: 999px;
    background: var(--accent-soft);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
    text-decoration: none;
}

.head-right {
    display: flex;
    flex-shrink: 0;
    align-items: center;
    gap: 6px;
}

.icon-btn {
    width: 28px;
    height: 28px;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--muted);
    font-size: 13px;
    cursor: pointer;
}

.icon-btn:hover {
    border-color: var(--border);
    background: var(--danger-soft);
    color: var(--danger);
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

.summary {
    margin-top: 12px;
}

.note {
    margin: 8px 0 0;
    font-size: 13px;
}

.warn {
    margin: 10px 0 0;
    padding: 8px 10px;
    border-radius: 8px;
    background: var(--warn-soft);
    color: var(--warn);
    font-size: 13px;
}

.actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
}

.error-box {
    margin: 12px 0 0;
    overflow-wrap: anywhere;
}
</style>
