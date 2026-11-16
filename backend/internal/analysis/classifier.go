package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

const DefaultModel = "claude-opus-5-5"

// Models that accept `fallbacks: "default"` (beta server-side-fallback-2026-07-01)
var fallbackModels = []string{"claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5", "claude-fable-5-2", "claude-fable-5-1"}

// ErrRefused: Claude (and the server-side fallback) declined to classify the post.
var ErrRefused = errors.New("classification refused")

type Classifier struct {
	client anthropic.Client
	model  string
	effort anthropic.BetaOutputConfigEffort
}

type Options struct {
	APIKey string
	Model  string // default DefaultModel
	// low | medium | high | xhigh | max. Classification does well at low.
	Effort string
}

func NewClassifier(o Options) (*Classifier, error) {
	if o.APIKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	if o.Model == "" {
		o.Model = DefaultModel
	}
	if o.Effort == "" {
		o.Effort = "low"
	}
	return &Classifier{
		client: anthropic.NewClient(option.WithAPIKey(o.APIKey), option.WithMaxRetries(5)),
		model:  o.Model,
		effort: anthropic.BetaOutputConfigEffort(o.Effort),
	}, nil
}

func (c *Classifier) Model() string { return c.model }

// PromptVersion identifies the prompt + schema for a company. Stored results labeled with
// an older version can be re-classified after the prompt changes.
func PromptVersion(company Company) string {
	sum := sha256.Sum256(append([]byte(systemPrompt(company)), labelSchema...))
	return hex.EncodeToString(sum[:4])
}

// labelSchema constrains the response (structured outputs), so the output is always valid JSON with known enums.
var labelSchema = func() json.RawMessage {
	enum := func(values []string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":  enum(Categories),
			"market":    enum(Markets),
			"sentiment": enum(Sentiments),
			"aspects":   map[string]any{"type": "array", "items": enum(Aspects)},
			"language":  enum(Languages),
			"reason":    map[string]any{"type": "string"},
		},
		"required":             []string{"category", "market", "sentiment", "aspects", "language", "reason"},
		"additionalProperties": false,
	}
	raw, _ := json.Marshal(schema)
	return raw
}()

// Classify labels one post. The system prompt is cached per company, so classifying
// many posts of the same company only pays full price for the post text.
func (c *Classifier) Classify(ctx context.Context, company Company, post Post) (Label, Usage, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     c.model,
		MaxTokens: 4096,
		System: []anthropic.BetaTextBlockParam{{
			Text:         systemPrompt(company),
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userMessage(post))),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: c.effort,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: labelSchema},
		},
	}
	// Server-side fallback re-runs a safety-classifier decline on another model.
	// Only some models support it (e.g. not Haiku 5.5) - sending it elsewhere is a 400.
	if slices.Contains(fallbackModels, c.model) {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	resp, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return Label{}, Usage{}, err
	}
	usage := Usage{
		InputTokens:         resp.Usage.InputTokens,
		OutputTokens:        resp.Usage.OutputTokens,
		CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
	}

	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return Label{}, usage, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return Label{}, usage, errors.New("response truncated (max_tokens)")
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	var label Label
	if err := json.Unmarshal([]byte(text.String()), &label); err != nil {
		return Label{}, usage, fmt.Errorf("parse label: %w", err)
	}
	if err := label.validate(); err != nil {
		return Label{}, usage, err
	}

	// The official accounts are known - don't depend on the model for that
	author := strings.ToLower(strings.TrimPrefix(post.AuthorUsername, "@"))
	for _, acc := range company.OfficialAccounts {
		if author == strings.ToLower(strings.TrimPrefix(acc, "@")) {
			label.Category = CategoryOfficial
		}
	}
	return label, usage, nil
}

func (l *Label) validate() error {
	check := func(field, v string, allowed []string) error {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("invalid %s %q", field, v)
		}
		return nil
	}
	if err := errors.Join(
		check("category", l.Category, Categories),
		check("market", l.Market, Markets),
		check("sentiment", l.Sentiment, Sentiments),
		check("language", l.Language, Languages),
	); err != nil {
		return err
	}
	for _, a := range l.Aspects {
		if err := check("aspect", a, Aspects); err != nil {
			return err
		}
	}
	if l.Aspects == nil {
		l.Aspects = []string{}
	}
	return nil
}
