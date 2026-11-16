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
    /** No earlier run of this search - every post counts as "new", so the flag is meaningless */
    isFirstRun: boolean;
    /** Set when the job was started by a recurring search */
    scheduleId: number | null;
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
    schedulesLimit: number;
    minScheduleHours: number;
}

export type ScheduleKind = 'every_hours' | 'daily';

export interface ScheduleInput {
    keyword: string;
    filter: SearchFilter;
    maxItems: number;
    kind: ScheduleKind;
    everyHours: number;
    dailyTime: string;
    timezone: string;
    enabled: boolean;
}

export interface Schedule extends ScheduleInput {
    id: number;
    nextRunAt: string;
    lastRunAt: string | null;
    lastError: string | null;
    lastJobId: number | null;
    lastJobStatus: JobStatus | null;
    createdAt: string;
}

export type PostSort = 'newest' | 'oldest' | 'likes' | 'replies' | 'reposts';

export interface Post {
    id: string;
    url: string;
    author: { username: string; name: string; verified: boolean; url: string };
    content: string;
    publishedAt: string | null;
    likes: number;
    replies: number;
    reposts: number;
    shares: number;
    imagesCount: number;
    videosCount: number;
    links: string[];
    quotedPostUrl: string;
    isNew: boolean;
}

export interface PostsPage {
    posts: Post[];
    total: number;
    page: number;
    pageSize: number;
}

export interface PostsParams {
    onlyNew?: boolean;
    q?: string;
    sort?: PostSort;
    page?: number;
    pageSize?: number;
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
    getJob: async (id: number) => (await request<{ job: Job }>('GET', `/jobs/${id}`)).job,
    listPosts: async (id: number, params: PostsParams) => {
        const qs = new URLSearchParams();
        if (params.onlyNew) qs.set('onlyNew', 'true');
        if (params.q) qs.set('q', params.q);
        if (params.sort) qs.set('sort', params.sort);
        if (params.page) qs.set('page', String(params.page));
        if (params.pageSize) qs.set('pageSize', String(params.pageSize));
        return request<PostsPage>('GET', `/jobs/${id}/posts?${qs.toString()}`);
    },
    cancelJob: async (id: number) => (await request<{ job: Job }>('POST', `/jobs/${id}/cancel`)).job,
    downloadUrl: (id: number) => `/api/jobs/${id}/download`,

    listSchedules: async () => (await request<{ schedules: Schedule[] }>('GET', '/schedules')).schedules,
    createSchedule: async (input: ScheduleInput) =>
        (await request<{ schedule: Schedule }>('POST', '/schedules', input)).schedule,
    updateSchedule: async (id: number, input: ScheduleInput) =>
        (await request<{ schedule: Schedule }>('PUT', `/schedules/${id}`, input)).schedule,
    deleteSchedule: async (id: number) => request<void>('DELETE', `/schedules/${id}`),
};
