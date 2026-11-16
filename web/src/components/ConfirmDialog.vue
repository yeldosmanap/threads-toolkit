<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue';

import { answerConfirm, pendingConfirm } from '../confirm';

const cancelBtn = ref<HTMLButtonElement | null>(null);
let restoreFocus: HTMLElement | null = null;

// Focus starts on «Отмена»: an accidental Enter/Space never confirms a destructive action
watch(pendingConfirm, async (p, prev) => {
    if (p && !prev) {
        restoreFocus = document.activeElement as HTMLElement | null;
        await nextTick();
        cancelBtn.value?.focus();
    } else if (!p && prev) {
        restoreFocus?.focus?.();
        restoreFocus = null;
    }
});

function onKey(e: KeyboardEvent): void {
    if (pendingConfirm.value && e.key === 'Escape') {
        e.preventDefault();
        answerConfirm('cancel');
    }
}
window.addEventListener('keydown', onKey);
onBeforeUnmount(() => window.removeEventListener('keydown', onKey));
</script>

<template>
    <Teleport to="body">
        <div v-if="pendingConfirm" class="backdrop" @mousedown.self="answerConfirm('cancel')">
            <div class="dialog card" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" aria-describedby="confirm-message">
                <h2 id="confirm-title">{{ pendingConfirm.title }}</h2>
                <p v-if="pendingConfirm.message" id="confirm-message" class="message">{{ pendingConfirm.message }}</p>
                <div class="actions">
                    <button ref="cancelBtn" class="btn secondary" type="button" @click="answerConfirm('cancel')">
                        {{ pendingConfirm.cancelText ?? 'Отмена' }}
                    </button>
                    <button v-if="pendingConfirm.secondaryText" class="btn secondary" type="button" @click="answerConfirm('secondary')">
                        {{ pendingConfirm.secondaryText }}
                    </button>
                    <button :class="['btn', { danger: pendingConfirm.danger }]" type="button" @click="answerConfirm('confirm')">
                        {{ pendingConfirm.confirmText }}
                    </button>
                </div>
            </div>
        </div>
    </Teleport>
</template>

<style scoped>
.backdrop {
    position: fixed;
    inset: 0;
    z-index: 100;
    display: grid;
    place-items: center;
    padding: 16px;
    background: rgb(15 17 21 / 55%);
}

.dialog {
    width: 100%;
    max-width: 440px;
    padding: 20px;
    box-shadow: 0 10px 40px rgb(0 0 0 / 25%);
}

h2 {
    margin: 0 0 8px;
    font-size: 18px;
    overflow-wrap: anywhere;
}

.message {
    margin: 0;
    color: var(--muted);
    font-size: 14px;
    white-space: pre-line;
}

.actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 20px;
}

.btn.danger {
    background: var(--danger);
}

.btn.danger:hover {
    filter: brightness(0.92);
}

@media (max-width: 520px) {
    .actions {
        flex-direction: column-reverse;
    }

    .actions .btn {
        width: 100%;
    }
}
</style>
