package export

import (
	"strings"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

// Russian names for classifier labels (UI and exports)
var (
	SentimentRu = map[string]string{"negative": "негатив", "neutral": "нейтрал", "positive": "позитив"}
	CategoryRu  = map[string]string{
		"review": "отзыв", "question": "вопрос", "news": "новость", "official": "официальный аккаунт",
		"ad": "реклама/продажа", "spam": "спам", "offtopic": "не по теме",
	}
	AspectRu = map[string]string{
		"network_coverage": "связь/покрытие", "mobile_internet": "мобильный интернет", "home_internet": "домашний интернет",
		"pricing": "цены/тарифы", "billing": "списания/оплата", "customer_support": "поддержка", "bonuses": "бонусы/акции",
		"roaming": "роуминг", "sim_number": "SIM/номер", "app": "приложение", "other": "другое",
	}
	LanguageRu = map[string]string{"ru": "русский", "kk": "казахский", "mixed": "смешанный", "other": "другой"}
)

// RemovalReason explains why a post was left out of the analytics.
func RemovalReason(l *store.PostLabel) string {
	if l == nil {
		return ""
	}
	if l.Market == "other_country" {
		return "другая страна"
	}
	return CategoryRu[l.Category]
}

func labelField(p store.LabeledPost, get func(*store.PostLabel) string) any {
	if p.Label == nil {
		return nil
	}
	return get(p.Label)
}

var labelColumns = []column{
	{"Тональность", 11, func(p store.LabeledPost) any {
		return labelField(p, func(l *store.PostLabel) string { return SentimentRu[l.Sentiment] })
	}, ""},
	{"Категория", 14, func(p store.LabeledPost) any {
		return labelField(p, func(l *store.PostLabel) string { return CategoryRu[l.Category] })
	}, ""},
	{"Аспекты", 26, func(p store.LabeledPost) any {
		return labelField(p, func(l *store.PostLabel) string {
			names := make([]string, len(l.Aspects))
			for i, a := range l.Aspects {
				names[i] = AspectRu[a]
			}
			return strings.Join(names, ", ")
		})
	}, ""},
	{"Язык", 11, func(p store.LabeledPost) any {
		return labelField(p, func(l *store.PostLabel) string { return LanguageRu[l.Language] })
	}, ""},
	{"Пояснение модели", 40, func(p store.LabeledPost) any {
		return labelField(p, func(l *store.PostLabel) string { return l.Reason })
	}, "wrap"},
}

var removalColumn = column{"Причина удаления", 18, func(p store.LabeledPost) any { return RemovalReason(p.Label) }, ""}
