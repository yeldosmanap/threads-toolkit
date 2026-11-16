<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { ApiError, api, type Limits, type Schedule, type ScheduleInput, type SearchFilter } from '../api';
import ScheduleCard from '../components/ScheduleCard.vue';
import { browserTimezone, FREQUENCIES } from '../schedules';

const schedules = ref<Schedule[]>([]);
const limits = ref<Limits | null>(null);
const loading = ref(true);
const loadError = ref('');

const keyword = ref('');
const filter = ref<SearchFilter>('recent');
const maxItems = ref(100);
const frequency = ref('h12');
const dailyTime = ref('09:00');
const formError = ref('');
const busy = ref(false);
const timezone = browserTimezone();

const frequencies = computed(() =>
    FREQUENCIES.filter((f) => !f.hours || !limits.value || f.hours >= limits.value.minScheduleHours),
);
const atLimit = computed(() => !!limits.value && schedules.value.length >= limits.value.schedulesLimit);
const isDaily = computed(() => frequency.value === 'daily');

async function refresh(): Promise<void> {
    try {
        const [list, lim] = await Promise.all([api.listSchedules(), api.limits()]);
        schedules.value = list;
        if (!limits.value) maxItems.value = lim.defaultMaxItems;
        limits.value = lim;
        loadError.value = '';
    } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) {
            loadError.value = e instanceof ApiError ? e.message : 'Не удалось загрузить расписания';
        }
    } finally {
        loading.value = false;
    }
}

async function submit(): Promise<void> {
    formError.value = '';
    busy.value = true;
    const preset = FREQUENCIES.find((f) => f.value === frequency.value);
    const input: ScheduleInput = {
        keyword: keyword.value.trim(),
        filter: filter.value,
        maxItems: Number(maxItems.value),
        kind: isDaily.value ? 'daily' : 'every_hours',
        everyHours: preset?.hours ?? 0,
        dailyTime: isDaily.value ? dailyTime.value : '',
        timezone,
        enabled: true,
    };
    try {
        const created = await api.createSchedule(input);
        schedules.value = [created, ...schedules.value];
        keyword.value = '';
    } catch (e) {
        formError.value = e instanceof ApiError ? e.message : 'Не удалось создать расписание';
    } finally {
        busy.value = false;
    }
}

function onChanged(s: Schedule): void {
    schedules.value = schedules.value.map((x) => (x.id === s.id ? s : x));
}

function onDeleted(id: number): void {
    schedules.value = schedules.value.filter((x) => x.id !== id);
}

// Keep "next run" / last status fresh: the backend scheduler acts every ~30 s
let timer: ReturnType<typeof setInterval> | undefined;
onMounted(async () => {
    await refresh();
    timer = setInterval(() => {
        if (document.visibilityState === 'visible') void refresh();
    }, 30000);
});
onBeforeUnmount(() => clearInterval(timer));

// If the minimum interval hides the selected preset, fall back to the first allowed one
watch(frequencies, (list) => {
    if (!list.some((f) => f.value === frequency.value)) frequency.value = list[0]?.value ?? 'daily';
});
</script>

<template>
    <h1 class="page-title">Регулярные поиски</h1>
    <p class="muted intro">
        Поиск будет запускаться автоматически по расписанию. Каждый запуск появится в «Поисках», а новые посты —
        те, которых не было в прошлых запусках, — будут отмечены.
    </p>

    <form class="card form" @submit.prevent="submit">
        <div class="row">
            <div class="grow">
                <label for="s-keyword">Ключевое слово</label>
                <input id="s-keyword" v-model="keyword" type="text" placeholder="например, билайн" maxlength="100" required autocomplete="off" />
            </div>
            <div class="count">
                <label for="s-max">Постов</label>
                <input id="s-max" v-model.number="maxItems" type="number" min="1" :max="limits?.maxItemsLimit ?? 500" required />
            </div>
        </div>

        <div class="row wrap">
            <div class="grow">
                <label for="s-freq">Как часто</label>
                <select id="s-freq" v-model="frequency" class="select">
                    <option v-for="f in frequencies" :key="f.value" :value="f.value">{{ f.label }}</option>
                </select>
            </div>
            <div v-if="isDaily" class="time">
                <label for="s-time">Время</label>
                <input id="s-time" v-model="dailyTime" type="time" required class="select" />
            </div>
            <div>
                <label>Сортировка</label>
                <div class="segmented" role="group" aria-label="Сортировка">
                    <button type="button" :class="{ active: filter === 'recent' }" @click="filter = 'recent'">Свежие</button>
                    <button type="button" :class="{ active: filter === 'top' }" @click="filter = 'top'">Популярные</button>
                </div>
            </div>
        </div>

        <p class="muted note">
            <template v-if="isDaily">Время указано по вашему часовому поясу ({{ timezone }}).</template>
            <template v-else>Первый запуск — сразу после создания.</template>
            Запуски расходуют общий суточный лимит поисков.
        </p>

        <p v-if="formError" class="error-box" role="alert">{{ formError }}</p>

        <div class="submit-row">
            <span v-if="limits" class="muted quota">
                Расписаний: {{ schedules.length }} из {{ limits.schedulesLimit }}
            </span>
            <button class="btn" type="submit" :disabled="busy || atLimit || !keyword.trim()">
                {{ busy ? 'Сохраняем…' : 'Создать расписание' }}
            </button>
        </div>
    </form>

    <h2 class="section-title">Мои расписания</h2>
    <p v-if="loadError" class="error-box">{{ loadError }}</p>
    <p v-else-if="loading" class="muted">Загрузка…</p>
    <div v-else-if="schedules.length === 0" class="card empty">
        <p>Пока нет регулярных поисков.</p>
        <p class="muted">Например: «билайн» каждый день в 09:00 — утром у вас будут свежие упоминания за сутки.</p>
    </div>
    <div v-else class="list">
        <ScheduleCard v-for="s in schedules" :key="s.id" :schedule="s" @changed="onChanged" @deleted="onDeleted" />
    </div>
</template>

<style scoped>
.page-title {
    margin: 0 0 6px;
    font-size: 24px;
}

.intro {
    margin: 0 0 16px;
    font-size: 14px;
}

.form {
    padding: 18px;
}

.row {
    display: flex;
    gap: 12px;
}

.row + .row {
    margin-top: 14px;
}

.wrap {
    flex-wrap: wrap;
    align-items: flex-end;
}

.grow {
    flex: 1;
    min-width: 0;
}

.wrap .grow {
    min-width: 200px;
}

.count {
    width: 110px;
}

.time {
    width: 120px;
}

.select {
    width: 100%;
    min-height: 40px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
}

.note {
    margin: 12px 0 0;
    font-size: 13px;
}

.error-box {
    margin: 12px 0 0;
}

.submit-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
}

.quota {
    font-size: 13px;
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

@media (max-width: 520px) {
    .submit-row .btn {
        width: 100%;
    }

    /* Only the frequency row stacks; keyword + count stay side by side */
    .wrap .grow {
        min-width: 100%;
    }
}
</style>
