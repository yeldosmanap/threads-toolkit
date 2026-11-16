import type { Schedule, ScheduleInput } from './api';

/** Frequency presets shown in the UI; `hours` = every N hours, `daily` = every day at a time */
export const FREQUENCIES: { value: string; label: string; hours?: number }[] = [
    { value: 'h6', label: 'Каждые 6 часов', hours: 6 },
    { value: 'h12', label: 'Каждые 12 часов', hours: 12 },
    { value: 'h24', label: 'Раз в сутки (от момента создания)', hours: 24 },
    { value: 'daily', label: 'Каждый день в указанное время' },
    { value: 'h168', label: 'Раз в неделю', hours: 168 },
];

export const browserTimezone = (): string => {
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
        return 'UTC';
    }
};

export function describeSchedule(s: Pick<ScheduleInput, 'kind' | 'everyHours' | 'dailyTime' | 'timezone'>): string {
    if (s.kind === 'daily') {
        const tz = s.timezone === browserTimezone() ? '' : ` (${s.timezone})`;
        return `Каждый день в ${s.dailyTime}${tz}`;
    }
    switch (s.everyHours) {
        case 168:
            return 'Раз в неделю';
        case 24:
            return 'Раз в сутки';
        default:
            return `Каждые ${s.everyHours} часов`;
    }
}

/** Copy of a schedule's editable fields, e.g. to toggle `enabled` via PUT */
export function toInput(s: Schedule): ScheduleInput {
    const { projectId, keyword, filter, maxItems, kind, everyHours, dailyTime, timezone, enabled } = s;
    return { projectId, keyword, filter, maxItems, kind, everyHours, dailyTime, timezone, enabled };
}

const rtf = new Intl.RelativeTimeFormat('ru-RU', { numeric: 'auto' });

/** "через 3 часа", "через 20 минут", "через 2 дня" */
export function relativeFromNow(iso: string): string {
    const diffMin = Math.round((Date.parse(iso) - Date.now()) / 60000);
    if (Math.abs(diffMin) < 1) return 'сейчас';
    if (Math.abs(diffMin) < 60) return rtf.format(diffMin, 'minute');
    const diffH = Math.round(diffMin / 60);
    if (Math.abs(diffH) < 48) return rtf.format(diffH, 'hour');
    return rtf.format(Math.round(diffH / 24), 'day');
}
