import type { Sentiment } from './api';

export const SENTIMENTS: { value: Sentiment; label: string }[] = [
    { value: 'negative', label: 'Негатив' },
    { value: 'neutral', label: 'Нейтрал' },
    { value: 'positive', label: 'Позитив' },
];

export const sentimentLabel = (s: string): string => SENTIMENTS.find((x) => x.value === s)?.label ?? s;

export const CATEGORY: Record<string, string> = {
    review: 'Отзыв',
    question: 'Вопрос',
    news: 'Новость',
    official: 'Официальный аккаунт',
    ad: 'Реклама / продажа',
    spam: 'Спам',
    offtopic: 'Не по теме',
    other_country: 'Другая страна',
};

export const ASPECTS: { value: string; label: string }[] = [
    { value: 'network_coverage', label: 'Связь и покрытие' },
    { value: 'mobile_internet', label: 'Мобильный интернет' },
    { value: 'home_internet', label: 'Домашний интернет' },
    { value: 'pricing', label: 'Цены и тарифы' },
    { value: 'billing', label: 'Списания и оплата' },
    { value: 'customer_support', label: 'Поддержка' },
    { value: 'bonuses', label: 'Бонусы и акции' },
    { value: 'roaming', label: 'Роуминг' },
    { value: 'sim_number', label: 'SIM и номера' },
    { value: 'app', label: 'Приложение' },
    { value: 'other', label: 'Другое' },
];

export const aspectLabel = (a: string): string => ASPECTS.find((x) => x.value === a)?.label ?? a;

export const LANGUAGE: Record<string, string> = { ru: 'русский', kk: 'казахский', mixed: 'смешанный', other: 'другой' };

/** Why a post was left out of the analytics */
export function removalReason(category: string, market: string): string {
    return market === 'other_country' ? CATEGORY.other_country : (CATEGORY[category] ?? category);
}

/** Russian plural: plural(2, ['упоминание', 'упоминания', 'упоминаний']) -> "2 упоминания" */
export function plural(n: number, forms: [string, string, string]): string {
    const mod10 = n % 10;
    const mod100 = n % 100;
    const form = mod10 === 1 && mod100 !== 11 ? forms[0] : mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) ? forms[1] : forms[2];
    return `${n} ${form}`;
}

export const MENTION_FORMS: [string, string, string] = ['упоминание', 'упоминания', 'упоминаний'];
