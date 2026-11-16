import { ref } from 'vue';

import { api, setUnauthorizedHandler, type User } from './api';

/** Current user; null = logged out, undefined = not checked yet */
export const currentUser = ref<User | null | undefined>(undefined);

export async function loadCurrentUser(): Promise<User | null> {
    if (currentUser.value !== undefined) return currentUser.value;
    try {
        currentUser.value = await api.me();
    } catch {
        currentUser.value = null;
    }
    return currentUser.value;
}

export async function login(email: string, password: string): Promise<void> {
    currentUser.value = await api.login(email, password);
}

export async function register(email: string, password: string): Promise<void> {
    currentUser.value = await api.register(email, password);
}

export async function logout(): Promise<void> {
    await api.logout().catch(() => undefined);
    currentUser.value = null;
}

export function installAuthGuard(onLoggedOut: () => void): void {
    setUnauthorizedHandler(() => {
        currentUser.value = null;
        onLoggedOut();
    });
}
