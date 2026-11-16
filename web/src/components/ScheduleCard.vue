<script setup lang="ts">
import { computed, ref } from 'vue';

import { ApiError, api, type Schedule } from '../api';
import { confirmDialog } from '../confirm';
import { MENTION_FORMS, plural } from '../labels';
import { describeSchedule, relativeFromNow, toInput } from '../schedules';

const props = defineProps<{ schedule: Schedule }>();
const emit = defineEmits<{ changed: [schedule: Schedule]; deleted: [id: number] }>();

const busy = ref(false);
const title = computed(() => props.schedule.projectName ?? props.schedule.keyword);
const error = ref('');

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

const next = computed(() => {
    const at = props.schedule.nextRunAt;
    // A due schedule is picked up by the backend within ~30 s
    if (Date.parse(at) <= Date.now()) return 'в ближайшую минуту';
    return `${relativeFromNow(at)} · ${dateFmt.format(new Date(at))}`;
});

const lastStatus = computed(() => {
    switch (props.schedule.lastJobStatus) {
        case 'queued':
            return 'в очереди';
        case 'running':
            return 'выполняется';
        case 'done':
            return 'готово';
        case 'failed':
            return 'ошибка';
        case 'canceled':
            return 'отменён';
        default:
            return '';
    }
});

async function toggle(): Promise<void> {
    busy.value = true;
    error.value = '';
    try {
        const input = toInput(props.schedule);
        input.enabled = !props.schedule.enabled;
        emit('changed', await api.updateSchedule(props.schedule.id, input));
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Не удалось изменить расписание';
    } finally {
        busy.value = false;
    }
}

async function remove(): Promise<void> {
    const answer = await confirmDialog({
        title: `Удалить регулярный поиск «${title.value}»?`,
        message: 'Новые запуски по этому расписанию больше не будут создаваться. Уже собранные результаты останутся в «Поисках».',
        confirmText: 'Удалить',
        danger: true,
    });
    if (answer !== 'confirm') return;
    busy.value = true;
    error.value = '';
    try {
        await api.deleteSchedule(props.schedule.id);
        emit('deleted', props.schedule.id);
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Не удалось удалить';
        busy.value = false;
    }
}
</script>

<template>
    <article class="card sched" :class="{ paused: !schedule.enabled }">
        <div class="head">
            <div class="title">
                <h3>
                    {{ title }}
                    <span v-if="schedule.projectId" class="tag">проект · {{ plural(schedule.aliases?.length ?? 0, MENTION_FORMS) }}</span>
                </h3>
                <span class="meta muted">
                    {{ describeSchedule(schedule) }} · {{ schedule.filter === 'recent' ? 'Свежие' : 'Популярные' }} · до
                    {{ schedule.maxItems }} постов
                </span>
            </div>
            <span class="badge" :class="{ on: schedule.enabled }">{{ schedule.enabled ? 'Активен' : 'На паузе' }}</span>
        </div>

        <dl class="facts">
            <div v-if="schedule.enabled">
                <dt>Следующий запуск</dt>
                <dd>{{ next }}</dd>
            </div>
            <div v-if="schedule.lastRunAt">
                <dt>Последний запуск</dt>
                <dd>
                    {{ dateFmt.format(new Date(schedule.lastRunAt)) }}
                    <template v-if="schedule.lastJobId && !schedule.lastError">
                        ·
                        <RouterLink
                            v-if="schedule.lastJobStatus === 'done'"
                            :to="{ name: 'job', params: { id: schedule.lastJobId } }"
                        >
                            {{ lastStatus }} — посмотреть посты
                        </RouterLink>
                        <span v-else>{{ lastStatus }}</span>
                    </template>
                </dd>
            </div>
        </dl>
        <p v-if="schedule.lastError" class="warn">{{ schedule.lastError }}</p>

        <div class="actions">
            <button class="btn secondary small" :disabled="busy" @click="toggle">
                {{ schedule.enabled ? 'Поставить на паузу' : 'Возобновить' }}
            </button>
            <button class="btn secondary small danger" :disabled="busy" @click="remove">Удалить</button>
        </div>
        <p v-if="error" class="error-box">{{ error }}</p>
    </article>
</template>

<style scoped>
.sched {
    padding: 14px 16px;
}

.sched.paused {
    opacity: 0.75;
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

.tag {
    margin-left: 6px;
    padding: 1px 7px;
    border-radius: 999px;
    background: var(--accent-soft);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
    vertical-align: middle;
}

.meta {
    font-size: 13px;
}

.badge {
    flex-shrink: 0;
    padding: 3px 10px;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 13px;
    font-weight: 600;
}

.badge.on {
    background: var(--ok-soft);
    color: var(--ok);
}

.facts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 32px;
    margin: 12px 0 0;
    font-size: 14px;
}

dt {
    color: var(--muted);
    font-size: 12px;
}

dd {
    margin: 0;
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
    margin-top: 12px;
}

.danger {
    color: var(--danger);
}

.error-box {
    margin: 10px 0 0;
}
</style>
