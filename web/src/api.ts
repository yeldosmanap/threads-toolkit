// Thin client for the backend JSON API (same origin: nginx / vite proxy /api to the backend).

export type JobStatus = 'queued' | 'running' | 'done' | 'failed' | 'canceled';
export type SearchFilter = 'recent' | 'top';

export interface User {
    id: number;
    email: string;
    isAdmin: boolean;
}

export interface Job {
    id: number;
    keyword: string;
    filter: SearchFilter;
    maxItems: number;
    status: JobStatus;
    postsSeen: number;
    postsCount: number | null;
    newPostsCount: number | null;
    error: string | null;
    hasFile: boolean;
    queuePosition: number | null;
    createdAt: string;
    startedAt: string | null;
    finishedAt: string | null;
}

export interface Limits {
    maxItemsLimit: number;
    defaultMaxItems: number;
    activeJobs: number;
    activeJobsLimit: number;
    dailyJobs: number;
    dailyJobsLimit: number;
}

export class ApiError extends Error {
    constructor(
        readonly status: number,
        readonly code: string,
        message: string,
    ) {
        super(message);
    }
}

/** Called on 401 so the app can drop the user and show the login page */
let onUnauthorized: () => void = () => undefined;
export function setUnauthorizedHandler(fn: () => void): void {
    onUnauthorized = fn;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    let res: Response;
    try {
        res = await fetch(`/api${path}`, {
            method,
            credentials: 'same-origin',
            // The backend requires JSON on every write (CSRF protection), even without a body
            headers: method === 'GET' ? {} : { 'Content-Type': 'application/json' },
            body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
        });
    } catch {
        throw new ApiError(0, 'network', 'Сервер недоступен. Проверьте подключение.');
    }

    if (res.status === 204) return undefined as T;

    const data = await res.json().catch(() => null);
    if (!res.ok) {
        const err = data?.error;
        if (res.status === 401 && path !== '/auth/login') onUnauthorized();
        throw new ApiError(res.status, err?.code ?? 'http_error', err?.message ?? `Ошибка ${res.status}`);
    }
    return data as T;
}

export const api = {
    me: async () => (await request<{ user: User }>('GET', '/auth/me')).user,
    login: async (email: string, password: string) =>
        (await request<{ user: User }>('POST', '/auth/login', { email, password })).user,
    register: async (email: string, password: string) =>
        (await request<{ user: User }>('POST', '/auth/register', { email, password })).user,
    logout: async () => request<void>('POST', '/auth/logout'),

    limits: async () => request<Limits>('GET', '/limits'),
    listJobs: async () => (await request<{ jobs: Job[] }>('GET', '/jobs')).jobs,
    createJob: async (keyword: string, filter: SearchFilter, maxItems: number) =>
        (await request<{ job: Job }>('POST', '/jobs', { keyword, filter, maxItems })).job,
    cancelJob: async (id: number) => (await request<{ job: Job }>('POST', `/jobs/${id}/cancel`)).job,
    downloadUrl: (id: number) => `/api/jobs/${id}/download`,
};
