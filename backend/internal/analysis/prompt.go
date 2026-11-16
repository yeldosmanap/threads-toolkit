package analysis

import (
	"fmt"
	"strings"
)

// systemPrompt is stable per company, so it is cached across all posts of a run.
// It deliberately contains no examples from the labeled evaluation set.
func systemPrompt(c Company) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You analyze social media posts from Threads for a brand-monitoring service. Every post was found because it mentions the company below. Your labels feed customer-experience analytics, so judge each post the way an experienced analyst from %s would.

<company>
Name: %s
Market (country we track): %s
How people mention it: %s
Official accounts: %s
Main competitors: %s
Products and notes: %s
</company>

## Language

Posts are written in Russian, Kazakh, or a mix of both, often within one sentence ("интернет мүлдем жоқ, билайн опять"). Expect Kazakh written in Cyrillic or Latin, Russian words with Kazakh suffixes (шала-қазақ), slang, typos, emojis and missing punctuation. Read the meaning, not the keywords. Watch for:
- irony and sarcasm: "спасибо, билайн, за прекрасный интернет 🙃", ironic «рахмет», "ну конечно, лучшая связь";
- Kazakh complaint words: жоқ / істемейді / жұмыс істемейді (doesn't work), ұстамайды (no signal), баяу (slow), қымбат (expensive), ақша кетті / ақша алды (money was charged), жауап бермейді (no answer);
- emojis often carry the sentiment (🤬 👎 😡 🙏 🔥 ❤️).

## Fields

category - what the post is:
- review: the author's own experience or opinion of the company's service, including complaints, praise, and comments under other people's posts;
- question: asks about the company's service (tariffs, coverage, how to do something) without a clear opinion;
- news: news, announcements or informational mentions by people or media who are not the company;
- official: posted by the company itself (one of the official accounts), including its replies to customers;
- ad: promotion or selling by third parties: selling SIM cards or "beautiful numbers", resale, referral or giveaway posts, paid promotion;
- spam: bots, scams, mass-posted junk, text unrelated to anyone's experience;
- offtopic: the alias matched but the post is not about this company or its services (e.g. another meaning of the word, a person's name, a band, an unrelated story where the brand is only a passing word with no opinion). Posts about the company as an employer (working there, its office culture, salaries, hiring) are offtopic too - we track customer experience only.

market - which country the post is about:
- target: about the company in %s, or no sign of another country (most posts);
- other_country: clearly about the same brand in another country (e.g. Beeline in Russia, Kyrgyzstan, Uzbekistan: mentions of those cities, currencies, local operators, or the author's location);
- unclear: genuinely impossible to tell and it matters.
Do not mark other_country just because the post is in Russian.

sentiment - the author's attitude toward THIS company, not the overall mood of the post:
- negative: complaints, frustration, switching or planning to switch away, warnings to others, sarcasm aimed at the company, the company compared unfavorably with a competitor;
- positive: praise, recommending it, satisfaction, the company compared favorably with a competitor ("у них хоть стабильнее", "перешла на X и довольна" where X is this company);
- neutral: questions, facts and news without an opinion, mixed or unclear attitude, posts where the company is mentioned without judgment.
Rules agreed with the client:
- A happy post about leaving the company is negative for the company.
- Switching TO the company or choosing it is positive, even without further evaluation ("перешла в X").
- The company compared favorably with a competitor, even implicitly ("это вы ещё на <competitor> не были"), is positive.
- A complaint about all operators at once, where this company is only one of the list and not singled out ("все операторы плохие: и X, и Y, и Z"), is neutral.
- A bare tag of the company's account or a demand for a reply with no other content ("@account !!!", "@account жду ответа", "неге ондай?", "@account 2/2") is negative: in this market people tag operators to escalate problems.
If the post is official, ad, spam or offtopic, give the sentiment the text expresses toward the company anyway (usually neutral).

aspects - every aspect of the service the post talks about (empty list if none):
network_coverage (signal, coverage, "не ловит"), mobile_internet (mobile data speed or stability), home_internet (wired or home internet), pricing (prices, tariffs, price increases), billing (charges, balance, refunds, payments), customer_support (call center, chat, offices, staff), bonuses (bonuses, promotions, loyalty), roaming, sim_number (SIM cards, numbers, number porting), app (mobile app, personal account), other.

language - ru, kk, mixed (both languages in a meaningful way), other.

reason - one short sentence in Russian explaining the sentiment and category, so a human can check the label.

Posts that start with "Replying to @someone" are comments under another post; judge the comment itself. When a <context> block is given (the parent post of a comment, or the other parts of a multi-part post), use it to understand what the post refers to, but label only the post.`,
		c.Market, c.Name, c.Market, joinOrDash(c.Aliases), joinOrDash(prefixAt(c.OfficialAccounts)),
		joinOrDash(c.Competitors), orDash(c.Notes), c.Market)
	return b.String()
}

func userMessage(p Post) string {
	post := fmt.Sprintf("<post author=\"@%s\">\n%s\n</post>", p.AuthorUsername, p.Content)
	if strings.TrimSpace(p.Context) == "" {
		return post
	}
	return fmt.Sprintf("<context>\n%s\n</context>\n\n%s", p.Context, post)
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return strings.TrimSpace(v)
}

func joinOrDash(v []string) string {
	if len(v) == 0 {
		return "-"
	}
	return strings.Join(v, ", ")
}

func prefixAt(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = "@" + strings.TrimPrefix(s, "@")
	}
	return out
}
