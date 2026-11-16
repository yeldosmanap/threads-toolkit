import './styles.css';

import { createApp } from 'vue';
import { createRouter, createWebHistory } from 'vue-router';

import App from './App.vue';
import { installAuthGuard, loadCurrentUser } from './auth';
import AuthView from './views/AuthView.vue';
import JobPostsView from './views/JobPostsView.vue';
import JobsView from './views/JobsView.vue';
import ProjectsView from './views/ProjectsView.vue';
import SchedulesView from './views/SchedulesView.vue';

const router = createRouter({
    history: createWebHistory(),
    routes: [
        { path: '/', name: 'jobs', component: JobsView, meta: { auth: true } },
        { path: '/jobs/:id(\\d+)', name: 'job', component: JobPostsView, meta: { auth: true }, props: true },
        { path: '/projects', name: 'projects', component: ProjectsView, meta: { auth: true } },
        { path: '/schedules', name: 'schedules', component: SchedulesView, meta: { auth: true } },
        { path: '/login', name: 'login', component: AuthView, props: { mode: 'login' } },
        { path: '/register', name: 'register', component: AuthView, props: { mode: 'register' } },
        { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
});

router.beforeEach(async (to) => {
    const user = await loadCurrentUser();
    if (to.meta.auth && !user) return { name: 'login' };
    if (!to.meta.auth && user) return { name: 'jobs' };
    return true;
});

installAuthGuard(() => {
    if (router.currentRoute.value.meta.auth) void router.push({ name: 'login' });
});

createApp(App).use(router).mount('#app');
