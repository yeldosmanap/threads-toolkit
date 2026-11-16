// Command evalsentiment measures the post classifier (internal/analysis) against a
// hand-labeled xlsx (column «тональность»: негативная / нейтральная / позитивная).
//
//	go run ./cmd/evalsentiment -files "a.xlsx,b.xlsx" -limit 20          # cheap cost check first
//	go run ./cmd/evalsentiment -files "a.xlsx,b.xlsx"                    # full run (Claude Opus 5.5)
//	go run ./cmd/evalsentiment -files "a.xlsx,b.xlsx" -model claude-haiku-5-5
//
// Every API call costs money. Results are cached per model+effort in -out, so a rerun
// only classifies posts that have no result yet. Reads ANTHROPIC_API_KEY from the
// environment or from the repository's .env file.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/analysis"
)

var beeline = analysis.Company{
	Name:             "Beeline Казахстан",
	Market:           "Казахстан",
	Aliases:          []string{"@beeline_kz", "beeline", "Beeline", "билайн", "Билайн", "билаин", "белайн"},
	OfficialAccounts: []string{"beeline_kz"},
	Competitors:      []string{"Activ", "Kcell", "Tele2", "Altel", "Казахтелеком"},
	Notes:            "Starlink Mobile / спутниковая мобильная связь - услуга Beeline Казахстан. izi - цифровой оператор (суббренд) Beeline.",
}

var goldMap = map[string]string{
	"негативная":  analysis.SentimentNegative,
	"нейтральная": analysis.SentimentNeutral,
	"позитивная":  analysis.SentimentPositive,
}

// USD per million tokens - estimates for the cost report (check current pricing before relying on them)
var prices = map[string]struct{ in, out, cacheRead, cacheWrite float64 }{
	"claude-opus-5-5":   {4, 20, 0.20, 5},
	"claude-sonnet-5-5": {2, 10, 0.10, 2.5},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01, 0.125},
}

type sample struct {
	analysis.Post
	Gold string
}

type result struct {
	ID    string         `json:"id"`
	Label analysis.Label `json:"label"`
	Usage analysis.Usage `json:"usage"`
	Error string         `json:"error,omitempty"`
}

func main() {
	files := flag.String("files", "", "comma-separated labeled xlsx files")
	model := flag.String("model", analysis.DefaultModel, "Claude model")
	effort := flag.String("effort", "low", "effort: low|medium|high|xhigh|max")
	limit := flag.Int("limit", 0, "classify at most N posts (0 = all)")
	concurrency := flag.Int("concurrency", 6, "parallel requests")
	reportOnly := flag.Bool("report-only", false, "only rebuild the report from cached results (no API calls)")
	outDir := flag.String("out", "eval-out", "directory for cached results and reports (contains post texts - keep it out of git)")
	flag.Parse()

	if *files == "" {
		log.Fatal("-files is required")
	}
	samples, err := loadSamples(strings.Split(*files, ","))
	if err != nil {
		log.Fatal(err)
	}
	if *limit > 0 && *limit < len(samples) {
		samples = samples[:*limit]
	}

	key := apiKey()
	if *reportOnly && key == "" {
		key = "unused" // no API calls in report-only mode
	}
	clf, err := analysis.NewClassifier(analysis.Options{APIKey: key, Model: *model, Effort: *effort})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	// Cache per model + effort + prompt version: editing the prompt starts a fresh run
	base := filepath.Join(*outDir, fmt.Sprintf("%s-%s-p%s", *model, *effort, analysis.PromptVersion(beeline)))
	cache := loadCache(base + ".jsonl")

	todo := []sample{}
	for _, s := range samples {
		if r, ok := cache[s.ID]; !ok || r.Error != "" {
			todo = append(todo, s)
		}
	}
	fmt.Printf("%d labeled posts, %d cached, %d to classify with %s (effort %s)\n", len(samples), len(samples)-len(todo), len(todo), *model, *effort)

	fmt.Printf("results file: %s.jsonl\n", base)
	if len(todo) > 0 && !*reportOnly {
		classifyAll(clf, todo, *concurrency, base+".jsonl", cache)
	}
	report(samples, cache, *model, *effort, base+"-report.md")
}

func classifyAll(clf *analysis.Classifier, todo []sample, concurrency int, path string, cache map[string]result) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var mu sync.Mutex
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	done, start := 0, time.Now()
	for _, s := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(s sample) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			label, usage, err := clf.Classify(ctx, beeline, s.Post)
			r := result{ID: s.ID, Label: label, Usage: usage}
			if err != nil {
				r.Error = err.Error()
			}
			line, _ := json.Marshal(r)

			mu.Lock()
			defer mu.Unlock()
			cache[s.ID] = r
			_, _ = f.Write(append(line, '\n'))
			done++
			if done%25 == 0 || done == len(todo) {
				fmt.Printf("  %d/%d (%s)\n", done, len(todo), time.Since(start).Round(time.Second))
			}
			if err != nil {
				fmt.Printf("  error on %s: %v\n", s.ID, err)
			}
		}(s)
	}
	wg.Wait()
}

func loadSamples(paths []string) ([]sample, error) {
	seen := map[string]bool{}
	var out []sample
	for _, p := range paths {
		f, err := excelize.OpenFile(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		rows, err := f.GetRows(f.GetSheetList()[0])
		f.Close()
		if err != nil {
			return nil, err
		}
		col := map[string]int{}
		for i, h := range rows[0] {
			col[strings.TrimSpace(h)] = i
		}
		for _, need := range []string{"id", "content", "author_username", "тональность"} {
			if _, ok := col[need]; !ok {
				return nil, fmt.Errorf("%s: column %q not found", p, need)
			}
		}
		get := func(row []string, name string) string {
			if i := col[name]; i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		for _, row := range rows[1:] {
			id := get(row, "id")
			gold, ok := goldMap[strings.ToLower(get(row, "тональность"))]
			if id == "" || !ok || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, sample{
				Post: analysis.Post{ID: id, AuthorUsername: get(row, "author_username"), Content: get(row, "content")},
				Gold: gold,
			})
		}
	}
	return out, nil
}

func loadCache(path string) map[string]result {
	cache := map[string]result{}
	f, err := os.Open(path)
	if err != nil {
		return cache
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r result
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			cache[r.ID] = r // later lines win (retries)
		}
	}
	return cache
}

var kazakhLetters = regexp.MustCompile(`[әғқңөұүһіӘҒҚҢӨҰҮҺІ]`)

type confusion map[string]map[string]int // gold -> predicted -> count

func (c confusion) add(gold, pred string) {
	if c[gold] == nil {
		c[gold] = map[string]int{}
	}
	c[gold][pred]++
}

func (c confusion) write(b *strings.Builder, title string) {
	total, correct := 0, 0
	fmt.Fprintf(b, "\n### %s\n\n| эталон \\ модель | negative | neutral | positive | всего |\n|---|---|---|---|---|\n", title)
	for _, g := range analysis.Sentiments {
		row := 0
		for _, p := range analysis.Sentiments {
			row += c[g][p]
		}
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d |\n", g, c[g][analysis.SentimentNegative], c[g][analysis.SentimentNeutral], c[g][analysis.SentimentPositive], row)
		total += row
		correct += c[g][g]
	}
	fmt.Fprintf(b, "\n| класс | precision | recall | F1 |\n|---|---|---|---|\n")
	macro := 0.0
	for _, cls := range analysis.Sentiments {
		tp := c[cls][cls]
		predicted, actual := 0, 0
		for _, g := range analysis.Sentiments {
			predicted += c[g][cls]
			actual += c[cls][g]
		}
		p, r := ratio(tp, predicted), ratio(tp, actual)
		f1 := 0.0
		if p+r > 0 {
			f1 = 2 * p * r / (p + r)
		}
		macro += f1
		fmt.Fprintf(b, "| %s | %.2f | %.2f | %.2f |\n", cls, p, r, f1)
	}
	fmt.Fprintf(b, "\nAccuracy: **%.1f%%** (%d/%d) · Macro-F1: **%.3f**\n", 100*ratio(correct, total), correct, total, macro/3)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func report(samples []sample, cache map[string]result, model, effort, path string) {
	all, kk := confusion{}, confusion{}
	var usage analysis.Usage
	errorsN := 0
	excluded := map[string]int{}
	var mismatches []string
	for _, s := range samples {
		r, ok := cache[s.ID]
		if !ok {
			continue
		}
		usage.Add(r.Usage)
		if r.Error != "" {
			errorsN++
			continue
		}
		pred := r.Label.Sentiment
		all.add(s.Gold, pred)
		if kazakhLetters.MatchString(s.Content) {
			kk.add(s.Gold, pred)
		}
		if r.Label.Excluded() {
			key := r.Label.Category
			if r.Label.Market == analysis.MarketOther {
				key = "other_country"
			}
			excluded[key]++
		}
		if pred != s.Gold {
			text := strings.ReplaceAll(s.Content, "\n", " ")
			if rs := []rune(text); len(rs) > 220 {
				text = string(rs[:220]) + "…"
			}
			mismatches = append(mismatches, fmt.Sprintf("| %s | %s | %s | %s | %s |", s.ID, s.Gold, pred, strings.ReplaceAll(text, "|", "/"), strings.ReplaceAll(r.Label.Reason, "|", "/")))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Оценка классификатора: %s, effort %s\n\nПостов с результатом: %d из %d, ошибок API: %d\n", model, effort, countDone(samples, cache), len(samples), errorsN)
	all.write(&b, "Все посты")
	kk.write(&b, "Посты с казахскими буквами")

	fmt.Fprintf(&b, "\n### Отсеяно бы из аналитики (набор считается «чистым», так что это скорее ложные срабатывания)\n\n")
	keys := make([]string, 0, len(excluded))
	for k := range excluded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		b.WriteString("Ничего.\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", k, excluded[k])
	}

	if p, ok := prices[model]; ok {
		cost := (float64(usage.InputTokens)*p.in + float64(usage.OutputTokens)*p.out +
			float64(usage.CacheReadTokens)*p.cacheRead + float64(usage.CacheCreationTokens)*p.cacheWrite) / 1e6
		n := countDone(samples, cache)
		fmt.Fprintf(&b, "\n### Токены и стоимость (оценка)\n\nВход: %d, выход: %d, из кэша: %d, запись в кэш: %d\n\n≈ **$%.2f** за %d постов, ≈ $%.2f за 1000 постов\n",
			usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, cost, n, cost/float64(max(1, n))*1000)
	}

	fmt.Fprintf(&b, "\n### Расхождения с разметкой (%d)\n\n| id | эталон | модель | текст | объяснение модели |\n|---|---|---|---|---|\n%s\n",
		len(mismatches), strings.Join(mismatches, "\n"))

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		log.Fatal(err)
	}
	// Print everything except the long mismatch table
	summary := b.String()
	if i := strings.Index(summary, "\n### Расхождения"); i > 0 {
		summary = summary[:i]
	}
	fmt.Println(summary)
	fmt.Printf("\nПолный отчёт с расхождениями: %s\n", path)
}

func countDone(samples []sample, cache map[string]result) int {
	n := 0
	for _, s := range samples {
		if r, ok := cache[s.ID]; ok && r.Error == "" {
			n++
		}
	}
	return n
}

// apiKey reads ANTHROPIC_API_KEY from the environment, else from the nearest .env up the tree.
func apiKey() string {
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return k
	}
	dir, _ := os.Getwd()
	for i := 0; i < 4; i++ {
		if data, err := os.ReadFile(filepath.Join(dir, ".env")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
				if ok && strings.TrimSpace(k) == "ANTHROPIC_API_KEY" {
					return strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
