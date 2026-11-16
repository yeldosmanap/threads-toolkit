import { shallowRef } from 'vue';

/**
 * Promise-based confirmation dialog rendered by <ConfirmDialog /> in App.vue.
 *
 *   if ((await confirmDialog({ title: 'Удалить?', confirmText: 'Удалить', danger: true })) !== 'confirm') return;
 *
 * Resolves to 'confirm', 'secondary' (only when secondaryText is set) or 'cancel'
 * (Отмена button, Esc, click outside).
 */
export interface ConfirmOptions {
    title: string;
    message?: string;
    confirmText: string;
    /** Optional third choice between cancel and confirm, e.g. «Удалить только проект» */
    secondaryText?: string;
    cancelText?: string;
    /** Red confirm button for destructive actions */
    danger?: boolean;
}

export type ConfirmResult = 'confirm' | 'secondary' | 'cancel';

interface Pending extends ConfirmOptions {
    resolve: (result: ConfirmResult) => void;
}

export const pendingConfirm = shallowRef<Pending | null>(null);

export function confirmDialog(options: ConfirmOptions): Promise<ConfirmResult> {
    // A newer dialog replaces an unanswered one (that one counts as canceled)
    pendingConfirm.value?.resolve('cancel');
    return new Promise((resolve) => {
        pendingConfirm.value = { ...options, resolve };
    });
}

export function answerConfirm(result: ConfirmResult): void {
    const p = pendingConfirm.value;
    if (!p) return;
    pendingConfirm.value = null;
    p.resolve(result);
}
