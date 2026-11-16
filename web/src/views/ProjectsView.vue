<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { ApiError, api, type Project, type SearchFilter } from '../api';
import ProjectForm from '../components/ProjectForm.vue';
import { confirmDialog } from '../confirm';
import { plural } from '../labels';

const router = useRouter();
const projects = ref<Project[]>([]);
const analysisEnabled = ref(true);
const loading = ref(true);
const error = ref('');

// null = form closed, 'new' = create, Project = edit
const editing = ref<Project | 'new' | null>(null);

// Run options per project card
const runFor = ref<number | null>(null);
const runFilter = ref<SearchFilter>('recent');
const runMax = ref(100);
const runBusy = ref(false);
const runError = ref('');

async function load(): Promise<void> {
    try {
        const res = await api.listProjects();
        projects.value = res.projects;
        analysisEnabled.value = res.analysisEnabled;
        if (res.projects.length === 0 && editing.value === null) editing.value = 'new';
    } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) error.value = e instanceof ApiError ? e.message : 'Не удалось загрузить проекты';
    } finally {
        loading.value = false;
    }
}

function onSaved(p: Project): void {
    const i = projects.value.findIndex((x) => x.id === p.id);
    if (i >= 0) projects.value[i] = p;
    else projects.value.unshift(p);
    editing.value = null;
}

async function remove(p: Project): Promise<void> {
    // Its searches: delete them too (running ones are stopped) or keep them in the history
    const runs = plural(p.runsCount, ['запуск', 'запуска', 'запусков']);
    const answer = await confirmDialog(
        p.runsCount > 0
            ? {
                  title: `Удалить проект «${p.name}»?`,
                  message:
                      `Будут удалены его расписания и разметка тональности. У проекта ${runs} в «Поисках»:\n` +
                      '— «Удалить вместе с запусками» удалит и их (идущий будет остановлен);\n' +
                      '— «Только проект» оставит их в истории.',
                  confirmText: 'Удалить вместе с запусками',
                  secondaryText: 'Только проект',
                  danger: true,
              }
            : {
                  title: `Удалить проект «${p.name}»?`,
                  message: 'Будут удалены его расписания и разметка тональности. Это нельзя отменить.',
                  confirmText: 'Удалить',
                  danger: true,
              },
    );
    if (answer === 'cancel') return;
    const withRuns = answer === 'confirm' && p.runsCount > 0;
    try {
        await api.deleteProject(p.id, withRuns);
        projects.value = projects.value.filter((x) => x.id !== p.id);
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Не удалось удалить проект';
    }
}

function openRun(p: Project): void {
    runFor.value = runFor.value === p.id ? null : p.id;
    runError.value = '';
}

async function run(p: Project): Promise<void> {
    runBusy.value = true;
    runError.value = '';
    try {
        await api.runProject(p.id, runFilter.value, Number(runMax.value));
        await router.push({ name: 'jobs' });
    } catch (e) {
        runError.value = e instanceof ApiError ? e.message : 'Не удалось запустить';
    } finally {
        runBusy.value = false;
    }
}

onMounted(load);
</script>

<template>
    <div class="title-row">
        <div>
            <h1 class="page-title">Проекты</h1>
            <p class="muted intro">
                Проект — это компания и все варианты, как её упоминают. Запуск ищет посты по каждому варианту, убирает
                дубли, отсеивает рекламу, спам и посты не по теме, определяет тональность и о чём жалуются.
            </p>
        </div>
        <button v-if="editing === null" class="btn" @click="editing = 'new'">Новый проект</button>
    </div>

    <p v-if="!analysisEnabled" class="warn-box">
        Анализ тональности отключён на сервере (не задан ANTHROPIC_API_KEY): проекты будут искать посты, но без разметки.
    </p>

    <ProjectForm
        v-if="editing !== null"
        :key="editing === 'new' ? 'new' : editing.id"
        :project="editing === 'new' ? null : editing"
        @saved="onSaved"
        @cancel="editing = null"
    />

    <p v-if="error" class="error-box">{{ error }}</p>
    <p v-else-if="loading" class="muted">Загрузка…</p>

    <div v-if="projects.length" class="list">
        <article v-for="p in projects" :key="p.id" class="card project">
            <div class="head">
                <div class="title">
                    <h3>{{ p.name }}</h3>
                    <span class="muted meta">{{ p.market }}</span>
                </div>
                <RouterLink v-if="p.lastJobId && p.lastJobStatus === 'done'" class="last" :to="{ name: 'job', params: { id: p.lastJobId } }">
                    Последний запуск →
                </RouterLink>
            </div>
            <div class="chips">
                <span v-for="a in p.aliases" :key="a" class="chip">{{ a }}</span>
            </div>
            <p v-if="p.notes" class="muted notes">{{ p.notes }}</p>

            <div class="actions">
                <button class="btn small" @click="openRun(p)">Запустить</button>
                <RouterLink class="btn secondary small" :to="{ name: 'schedules', query: { project: p.id } }">Мониторинг</RouterLink>
                <button class="btn secondary small" @click="editing = p">Изменить</button>
                <button class="btn secondary small danger" @click="remove(p)">Удалить</button>
            </div>

            <form v-if="runFor === p.id" class="run" @submit.prevent="run(p)">
                <div class="segmented" role="group" aria-label="Сортировка">
                    <button type="button" :class="{ active: runFilter === 'recent' }" @click="runFilter = 'recent'">Свежие</button>
                    <button type="button" :class="{ active: runFilter === 'top' }" @click="runFilter = 'top'">Популярные</button>
                </div>
                <label class="inline">
                    до
                    <input v-model.number="runMax" type="number" min="1" max="500" class="num" aria-label="Постов на каждое упоминание" />
                    постов на каждое из {{ p.aliases.length }} упоминаний
                </label>
                <button class="btn small" type="submit" :disabled="runBusy">{{ runBusy ? 'Ставим в очередь…' : 'Поехали' }}</button>
                <p v-if="runError" class="error-box full">{{ runError }}</p>
            </form>
        </article>
    </div>
</template>

<style scoped>
.title-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 16px;
}

.page-title {
    margin: 0 0 6px;
    font-size: 24px;
}

.intro {
    margin: 0;
    max-width: 640px;
    font-size: 14px;
}

.warn-box {
    padding: 10px 12px;
    border-radius: 8px;
    background: var(--warn-soft);
    color: var(--warn);
    font-size: 14px;
}

.list {
    display: grid;
    gap: 10px;
    margin-top: 16px;
}

.project {
    padding: 14px 16px;
}

.head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 4px 12px;
}

.title {
    min-width: 0;
    overflow-wrap: anywhere;
}

h3 {
    display: inline;
    margin: 0 8px 0 0;
    font-size: 16px;
}

.meta {
    font-size: 13px;
}

.last {
    flex-shrink: 0;
    font-size: 14px;
    text-decoration: none;
}

.chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
}

.chip {
    padding: 2px 9px;
    border-radius: 999px;
    background: var(--surface-2);
    font-size: 13px;
}

.notes {
    margin: 10px 0 0;
    font-size: 13px;
    white-space: pre-wrap;
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

.run {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
}

.inline {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    color: var(--text);
    font-size: 14px;
    font-weight: 400;
}

.num {
    width: 80px;
    min-height: 32px;
}

.full {
    flex-basis: 100%;
    margin: 0;
}

@media (max-width: 520px) {
    .title-row {
        flex-direction: column;
    }

    .inline {
        flex-wrap: wrap;
    }
}
</style>
