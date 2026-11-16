<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';

import { ApiError } from '../api';
import { login, register } from '../auth';

const props = defineProps<{ mode: 'login' | 'register' }>();
const router = useRouter();

const email = ref('');
const password = ref('');
const error = ref('');
const busy = ref(false);

const isLogin = computed(() => props.mode === 'login');

async function submit(): Promise<void> {
    error.value = '';
    busy.value = true;
    try {
        if (isLogin.value) await login(email.value.trim(), password.value);
        else await register(email.value.trim(), password.value);
        await router.push({ name: 'jobs' });
    } catch (e) {
        error.value = e instanceof ApiError ? e.message : 'Что-то пошло не так';
    } finally {
        busy.value = false;
    }
}
</script>

<template>
    <section class="auth card">
        <h1>{{ isLogin ? 'Вход' : 'Регистрация' }}</h1>
        <p class="muted lead">Поиск постов в Threads по ключевым словам с выгрузкой в Excel.</p>

        <form @submit.prevent="submit">
            <div class="field">
                <label for="email">Email</label>
                <input id="email" v-model="email" type="email" autocomplete="email" required />
            </div>
            <div class="field">
                <label for="password">Пароль</label>
                <input
                    id="password"
                    v-model="password"
                    type="password"
                    :autocomplete="isLogin ? 'current-password' : 'new-password'"
                    :minlength="isLogin ? undefined : 8"
                    required
                />
                <p v-if="!isLogin" class="hint muted">Минимум 8 символов</p>
            </div>

            <p v-if="error" class="error-box" role="alert">{{ error }}</p>

            <button class="btn full" type="submit" :disabled="busy">
                {{ busy ? 'Подождите…' : isLogin ? 'Войти' : 'Создать аккаунт' }}
            </button>
        </form>

        <p class="switch muted">
            <template v-if="isLogin">
                Нет аккаунта? <RouterLink to="/register">Зарегистрироваться</RouterLink>
            </template>
            <template v-else>Уже есть аккаунт? <RouterLink to="/login">Войти</RouterLink></template>
        </p>
    </section>
</template>

<style scoped>
.auth {
    max-width: 400px;
    margin: 32px auto 0;
    padding: 28px 24px;
}

h1 {
    margin: 0 0 4px;
    font-size: 22px;
}

.lead {
    margin: 0 0 20px;
    font-size: 14px;
}

.field {
    margin-bottom: 14px;
}

.hint {
    margin: 4px 0 0;
    font-size: 12px;
}

.error-box {
    margin: 0 0 14px;
}

.full {
    width: 100%;
}

.switch {
    margin: 16px 0 0;
    text-align: center;
    font-size: 14px;
}
</style>
