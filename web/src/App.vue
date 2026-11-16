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
                <span class="brand-text">Threads Toolkit</span>
            </RouterLink>
            <nav v-if="currentUser" class="nav">
                <RouterLink to="/" exact-active-class="active">Поиски</RouterLink>
                <RouterLink to="/schedules" active-class="active">Регулярные</RouterLink>
            </nav>
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

.nav {
    display: flex;
    gap: 4px;
    margin-right: auto;
    margin-left: 16px;
}

.nav a {
    padding: 6px 10px;
    border-radius: 7px;
    color: var(--muted);
    font-size: 14px;
    font-weight: 600;
    text-decoration: none;
}

.nav a:hover {
    background: var(--surface-2);
}

.nav a.active {
    background: var(--accent-soft);
    color: var(--accent);
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
    .email,
    .brand-text {
        display: none;
    }

    .nav {
        margin-left: 4px;
    }
}

@media (max-width: 360px) {
    .topbar-inner {
        gap: 6px;
    }

    .nav a {
        padding: 6px 7px;
    }
}
</style>
