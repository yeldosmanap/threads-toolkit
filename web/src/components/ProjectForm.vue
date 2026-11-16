<script setup lang="ts">
import { ref } from 'vue';

import { ApiError, api, type Project, type ProjectInput } from '../api';

const props = defineProps<{ project?: Project | null }>();
const emit = defineEmits<{ saved: [project: Project]; cancel: [] }>();

const split = (v: string): string[] =>
    v
        .split(/[,\n]/)
        .map((s) => s.trim())
        .filter(Boolean);

const p = props.project;
const name = ref(p?.name ?? '');
const market = ref(p?.market ?? 'Казахстан');
const aliases = ref(p?.aliases.join(', ') ?? '');
const official = ref(p?.officialAccounts.map((a) => `@${a}`).join(', ') ?? '');
const competitors = ref(p?.competitors.join(', ') ?? '');
const notes = ref(p?.notes ?? '');
const error = ref('');
const busy = ref(false);

async function submit(): Promise<void> {
    error.value = '';
    busy.value = true;
    const input: ProjectInput = {
        name: name.value.trim(),
        market: market.value.trim(),
        aliases: split(aliases.value),
        officialAccounts: split(official.value).map((a) => a.replace(/^@/, '')),
        competitors: split(competitors.value),
        notes: notes.value.trim(),
    };
    try {
        emit('saved', p ? await api.updateProject(p.id, input) : await api.createProject(input));
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Не удалось сохранить проект';
    } finally {
        busy.value = false;
    }
}
</script>

<template>
    <form class="card form" @submit.prevent="submit">
        <h2>{{ p ? 'Изменить проект' : 'Новый проект' }}</h2>
        <div class="row">
            <div class="grow">
                <label for="pf-name">Компания</label>
                <input id="pf-name" v-model="name" type="text" placeholder="например, Beeline Казахстан" maxlength="100" required />
            </div>
            <div class="market">
                <label for="pf-market">Страна</label>
                <input id="pf-market" v-model="market" type="text" maxlength="60" />
            </div>
        </div>

        <div class="field">
            <label for="pf-aliases">Как упоминают компанию — каждый вариант ищется отдельно</label>
            <textarea id="pf-aliases" v-model="aliases" rows="2" placeholder="@beeline_kz, beeline, билайн, билаин" required />
            <p class="hint muted">Через запятую или с новой строки, до 10 вариантов. Регистр не важен для анализа, но Threads ищет по-разному.</p>
        </div>

        <div class="row wrap">
            <div class="grow">
                <label for="pf-official">Официальные аккаунты компании</label>
                <input id="pf-official" v-model="official" type="text" placeholder="@beeline_kz" />
                <p class="hint muted">Их посты не попадут в аналитику.</p>
            </div>
            <div class="grow">
                <label for="pf-competitors">Конкуренты</label>
                <input id="pf-competitors" v-model="competitors" type="text" placeholder="Activ, Kcell, Tele2, Altel" />
                <p class="hint muted">Помогает понять сравнения «у X лучше, чем у Y».</p>
            </div>
        </div>

        <div class="field">
            <label for="pf-notes">Продукты и заметки для анализа</label>
            <textarea
                id="pf-notes"
                v-model="notes"
                rows="3"
                maxlength="2000"
                placeholder="Starlink Mobile — спутниковая связь Beeline. izi — суббренд Beeline."
            />
            <p class="hint muted">Всё, что поможет понять, что пост именно о компании: продукты, суббренды, рекламные кампании.</p>
        </div>

        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <div class="actions">
            <button class="btn" type="submit" :disabled="busy">{{ busy ? 'Сохраняем…' : 'Сохранить' }}</button>
            <button class="btn secondary" type="button" :disabled="busy" @click="emit('cancel')">Отмена</button>
        </div>
    </form>
</template>

<style scoped>
.form {
    padding: 18px;
}

h2 {
    margin: 0 0 14px;
    font-size: 17px;
}

.row {
    display: flex;
    gap: 12px;
}

.wrap {
    flex-wrap: wrap;
}

.row,
.field {
    margin-bottom: 14px;
}

.grow {
    flex: 1;
    min-width: 0;
}

.wrap .grow {
    min-width: 220px;
}

.market {
    width: 160px;
}

textarea {
    width: 100%;
    padding: 9px 12px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    resize: vertical;
}

textarea:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
}

.hint {
    margin: 4px 0 0;
    font-size: 12px;
}

.error-box {
    margin: 0 0 14px;
}

.actions {
    display: flex;
    gap: 8px;
}

@media (max-width: 520px) {
    .row:not(.wrap) {
        flex-direction: column;
    }

    .market {
        width: 100%;
    }

    .wrap .grow {
        min-width: 100%;
    }
}
</style>
