<script setup lang="ts">
import { computed, ref, watch } from 'vue';

import { ApiError, api, type Job, type Limits, type SearchFilter } from '../api';

const props = defineProps<{ limits: Limits | null }>();
const emit = defineEmits<{ created: [job: Job] }>();

const keyword = ref('');
const filter = ref<SearchFilter>('recent');
const maxItems = ref(100);
const error = ref('');
const busy = ref(false);

watch(
    () => props.limits,
    (l, prev) => {
        if (l && !prev) maxItems.value = l.defaultMaxItems;
    },
    { immediate: true },
);

const quotaReached = computed(() => {
    const l = props.limits;
    return !!l && (l.activeJobs >= l.activeJobsLimit || l.dailyJobs >= l.dailyJobsLimit);
});

async function submit(): Promise<void> {
    error.value = '';
    busy.value = true;
    try {
        const job = await api.createJob(keyword.value.trim(), filter.value, Number(maxItems.value));
        keyword.value = '';
        emit('created', job);
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Не удалось создать поиск';
    } finally {
        busy.value = false;
    }
}
</script>

<template>
    <form class="card form" @submit.prevent="submit">
        <div class="row">
            <div class="grow">
                <label for="keyword">Ключевое слово</label>
                <input
                    id="keyword"
                    v-model="keyword"
                    type="text"
                    placeholder="например, билайн"
                    maxlength="100"
                    required
                    autocomplete="off"
                />
            </div>
            <div class="count">
                <label for="maxItems">Постов</label>
                <input
                    id="maxItems"
                    v-model.number="maxItems"
                    type="number"
                    min="1"
                    :max="limits?.maxItemsLimit ?? 500"
                    required
                />
            </div>
        </div>

        <div class="row bottom">
            <div>
                <label>Сортировка</label>
                <div class="segmented" role="group" aria-label="Сортировка">
                    <button type="button" :class="{ active: filter === 'recent' }" @click="filter = 'recent'">
                        Свежие
                    </button>
                    <button type="button" :class="{ active: filter === 'top' }" @click="filter = 'top'">
                        Популярные
                    </button>
                </div>
            </div>
            <button class="btn" type="submit" :disabled="busy || quotaReached || !keyword.trim()">
                {{ busy ? 'Отправляем…' : 'Найти посты' }}
            </button>
        </div>

        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <p v-if="limits" class="quota muted">
            Поисков за сутки: {{ limits.dailyJobs }} из {{ limits.dailyJobsLimit }} · Одновременно:
            {{ limits.activeJobs }} из {{ limits.activeJobsLimit }} · До {{ limits.maxItemsLimit }} постов за поиск
        </p>
    </form>
</template>

<style scoped>
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

.bottom {
    align-items: flex-end;
    justify-content: space-between;
    flex-wrap: wrap;
}

.grow {
    flex: 1;
    min-width: 0;
}

.count {
    width: 110px;
}

.error-box {
    margin: 14px 0 0;
}

.quota {
    margin: 12px 0 0;
    font-size: 13px;
}

@media (max-width: 520px) {
    .bottom .btn {
        width: 100%;
    }
}
</style>
