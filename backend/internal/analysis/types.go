// Package analysis classifies scraped Threads posts about a company with Claude:
// is the post really about the company (right company, right country), what kind of
// post it is (review, question, ad, spam, ...), the sentiment toward the company and
// which aspects of the service it talks about. Posts are in Russian, Kazakh or a mix.
package analysis

import "slices"

// Company is the brand a project tracks.
type Company struct {
	// Display name, e.g. "Beeline Казахстан"
	Name string
	// Country / market the project cares about, e.g. "Казахстан"
	Market string
	// How people mention it: "@beeline_kz", "beeline", "билайн", "билаин", ...
	Aliases []string
	// The company's own accounts (without @); their posts are always "official"
	OfficialAccounts []string
	// Direct competitors - helps with comparisons like "у актива лучше, чем у билайна"
	Competitors []string
	// Free-form products and context written by the user, e.g.
	// "Starlink Mobile - спутниковая связь Beeline; izi - суббренд Beeline"
	Notes string
}

// Post is what the classifier sees.
type Post struct {
	ID             string
	AuthorUsername string
	Content        string
	// Optional surrounding text: the parent post for a comment, or the other parts of a
	// multi-part post ("1/3 ... 3/3"). Used only to understand the post, not labeled itself.
	Context string
}

const (
	CategoryReview   = "review"   // experience, complaint or praise
	CategoryQuestion = "question" // asks about the company's service
	CategoryNews     = "news"     // news / informational mention
	CategoryOfficial = "official" // posted by the company itself
	CategoryAd       = "ad"       // promotion or selling (SIM numbers, resale, referral links)
	CategorySpam     = "spam"     // bots, scams, mass-posted junk
	CategoryOfftopic = "offtopic" // alias matched but the post is not about the company
)

const (
	MarketTarget  = "target"        // about the company in the tracked country
	MarketOther   = "other_country" // about the same brand in another country
	MarketUnclear = "unclear"
)

const (
	SentimentNegative = "negative"
	SentimentNeutral  = "neutral"
	SentimentPositive = "positive"
)

var (
	Categories = []string{CategoryReview, CategoryQuestion, CategoryNews, CategoryOfficial, CategoryAd, CategorySpam, CategoryOfftopic}
	Markets    = []string{MarketTarget, MarketOther, MarketUnclear}
	Sentiments = []string{SentimentNegative, SentimentNeutral, SentimentPositive}
	Languages  = []string{"ru", "kk", "mixed", "other"}
	Aspects    = []string{
		"network_coverage", // связь, покрытие, «не ловит»
		"mobile_internet",  // скорость и стабильность мобильного интернета
		"home_internet",    // домашний / проводной интернет
		"pricing",          // цены, тарифы, повышение стоимости
		"billing",          // списания, баланс, возврат денег, оплата
		"customer_support", // колл-центр, чат, офисы, консультанты
		"bonuses",          // бонусы, акции, программы лояльности
		"roaming",
		"sim_number", // SIM-карты, номера, перенос номера
		"app",        // мобильное приложение, личный кабинет
		"other",
	}
)

// Label is the classifier output for one post.
type Label struct {
	Category  string   `json:"category"`
	Market    string   `json:"market"`
	Sentiment string   `json:"sentiment"`
	Aspects   []string `json:"aspects"`
	Language  string   `json:"language"`
	// Short explanation in Russian, shown to the user next to the label
	Reason string `json:"reason"`
}

// Excluded reports whether the post should be left out of the analytics
// (it goes to the «Удаленные_посты» export instead).
func (l Label) Excluded() bool {
	if l.Market == MarketOther {
		return true
	}
	return slices.Contains([]string{CategoryOfficial, CategoryAd, CategorySpam, CategoryOfftopic}, l.Category)
}

// Usage is the token usage of one classification, for cost accounting.
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheCreationTokens += o.CacheCreationTokens
}
