<script setup lang="ts">
import { computed } from 'vue';

import type { Sentiment } from '../api';
import { SENTIMENTS } from '../labels';

const props = withDefaults(
    defineProps<{ counts: Partial<Record<Sentiment, number>>; compact?: boolean; selectable?: boolean; selected?: Sentiment | '' }>(),
    { compact: false, selectable: false, selected: '' },
);
const emit = defineEmits<{ select: [sentiment: Sentiment | ''] }>();

const total = computed(() => SENTIMENTS.reduce((n, s) => n + (props.counts[s.value] ?? 0), 0));
const parts = computed(() =>
    SENTIMENTS.map((s) => {
        const n = props.counts[s.value] ?? 0;
        return { ...s, n, pct: total.value ? Math.round((n / total.value) * 100) : 0 };
    }),
);

function toggle(s: Sentiment): void {
    if (props.selectable) emit('select', props.selected === s ? '' : s);
}
</script>

<template>
    <div class="sentiment" :class="{ compact }">
        <div class="bar" role="img" :aria-label="parts.map((p) => `${p.label}: ${p.n}`).join(', ')">
            <span v-for="p in parts" :key="p.value" :class="['seg', p.value]" :style="{ width: `${p.pct}%` }" />
            <span v-if="!total" class="seg empty" style="width: 100%" />
        </div>
        <div class="legend">
            <component
                :is="selectable ? 'button' : 'span'"
                v-for="p in parts"
                :key="p.value"
                :type="selectable ? 'button' : undefined"
                :class="['item', p.value, { active: selected === p.value, dim: selected && selected !== p.value }]"
                @click="toggle(p.value)"
            >
                <i :class="['dot', p.value]" />
                {{ p.label }} <strong>{{ p.n }}</strong>
                <span v-if="!compact && total" class="muted">{{ p.pct }}%</span>
            </component>
        </div>
    </div>
</template>

<style scoped>
.bar {
    display: flex;
    height: 10px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--surface-2);
}

.compact .bar {
    height: 6px;
}

.seg {
    height: 100%;
    transition: width 0.3s ease;
}

.negative {
    --c: var(--danger);
}

.neutral {
    --c: #94a3b8;
}

.positive {
    --c: var(--ok);
}

.seg {
    background: var(--c);
}

.seg.empty {
    background: transparent;
}

.legend {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 14px;
    margin-top: 8px;
    font-size: 14px;
}

.compact .legend {
    margin-top: 6px;
    font-size: 13px;
}

.item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
}

button.item {
    padding: 3px 8px;
    border-radius: 999px;
    cursor: pointer;
}

button.item:hover,
button.item.active {
    background: var(--surface-2);
}

button.item.active {
    box-shadow: inset 0 0 0 1px var(--c);
}

.item.dim {
    opacity: 0.55;
}

.dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--c);
}
</style>
