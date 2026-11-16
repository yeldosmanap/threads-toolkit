<script setup lang="ts">
import { useRouter } from 'vue-router';

import { currentUser, logout } from './auth';

const router = useRouter();

async function onLogout(): Promise<void> {
    await logout();
    await router.push({ name: 'login' });
}
</script>

<template>
    <header class="topbar">
        <div class="container topbar-inner">
            <RouterLink to="/" class="brand">
                <img src="/favicon.svg" alt="" width="24" height="24" />
                Threads Toolkit
            </RouterLink>
            <div v-if="currentUser" class="user">
                <span class="muted email">{{ currentUser.email }}</span>
                <button class="btn secondary small" @click="onLogout">Выйти</button>
            </div>
        </div>
    </header>
    <main class="container main">
        <RouterView />
    </main>
</template>

<style scoped>
.topbar {
    border-bottom: 1px solid var(--border);
    background: var(--surface);
}

.topbar-inner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 56px;
}

.brand {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--text);
    font-weight: 700;
    text-decoration: none;
}

.user {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
}

.email {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 14px;
}

.main {
    padding-top: 24px;
    padding-bottom: 48px;
}

@media (max-width: 520px) {
    .email {
        display: none;
    }
}
</style>
