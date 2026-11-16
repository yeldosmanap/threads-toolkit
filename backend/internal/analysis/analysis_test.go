package analysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExcluded(t *testing.T) {
	cases := []struct {
		l    Label
		want bool
	}{
		{Label{Category: CategoryReview, Market: MarketTarget}, false},
		{Label{Category: CategoryQuestion, Market: MarketUnclear}, false},
		{Label{Category: CategoryNews, Market: MarketTarget}, false},
		{Label{Category: CategoryReview, Market: MarketOther}, true},
		{Label{Category: CategoryOfficial, Market: MarketTarget}, true},
		{Label{Category: CategoryAd, Market: MarketTarget}, true},
		{Label{Category: CategorySpam, Market: MarketTarget}, true},
		{Label{Category: CategoryOfftopic, Market: MarketTarget}, true},
	}
	for _, c := range cases {
		if got := c.l.Excluded(); got != c.want {
			t.Errorf("%+v: Excluded() = %v, want %v", c.l, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Label{Category: "review", Market: "target", Sentiment: "negative", Language: "mixed"}
	if err := ok.validate(); err != nil || ok.Aspects == nil {
		t.Fatalf("valid label rejected: %v (aspects %v)", err, ok.Aspects)
	}
	for _, bad := range []Label{
		{Category: "complaint", Market: "target", Sentiment: "negative", Language: "ru"},
		{Category: "review", Market: "target", Sentiment: "angry", Language: "ru"},
		{Category: "review", Market: "target", Sentiment: "negative", Language: "ru", Aspects: []string{"speed"}},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func TestSchemaMatchesEnums(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			Enum  []string `json:"enum"`
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(labelSchema, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Required) != 6 || s.AdditionalProperties {
		t.Errorf("schema must require all 6 fields and forbid extras: %+v", s)
	}
	if strings.Join(s.Properties["sentiment"].Enum, ",") != "negative,neutral,positive" {
		t.Errorf("sentiment enum: %v", s.Properties["sentiment"].Enum)
	}
	if len(s.Properties["aspects"].Items.Enum) != len(Aspects) {
		t.Errorf("aspects enum: %v", s.Properties["aspects"].Items.Enum)
	}
}

func TestPrompts(t *testing.T) {
	c := Company{Name: "Beeline Казахстан", Market: "Казахстан", Aliases: []string{"билайн"}, OfficialAccounts: []string{"beeline_kz"}}
	sp := systemPrompt(c)
	for _, want := range []string{"Beeline Казахстан", "@beeline_kz", "билайн", "Казахстан"} {
		if !strings.Contains(sp, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if um := userMessage(Post{AuthorUsername: "aida", Content: "интернет жоқ"}); !strings.Contains(um, `author="@aida"`) || !strings.Contains(um, "интернет жоқ") {
		t.Errorf("user message: %q", um)
	}
}

func TestUserMessageContext(t *testing.T) {
	plain := userMessage(Post{AuthorUsername: "a", Content: "2/2"})
	if strings.Contains(plain, "<context>") {
		t.Error("no context block expected")
	}
	withCtx := userMessage(Post{AuthorUsername: "a", Content: "@beeline_kz 2/2", Context: "1/2 интернет не работает третий день"})
	if !strings.HasPrefix(withCtx, "<context>\n1/2 интернет") || !strings.Contains(withCtx, "@beeline_kz 2/2") {
		t.Errorf("context message: %q", withCtx)
	}
}
