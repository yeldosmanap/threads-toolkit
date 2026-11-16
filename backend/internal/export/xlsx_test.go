package export

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

func TestBuildXLSX(t *testing.T) {
	older := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 11, 16, 13, 26, 0, 0, time.UTC)
	posts := []store.SavedPost{
		{Post: store.Post{ID: "a", URL: "https://www.threads.com/@x/post/a", AuthorUsername: "x", Content: "старый пост", PublishedAt: &older, Likes: 3}, IsNew: false},
		{Post: store.Post{ID: "b", URL: "https://www.threads.com/@y/post/b", AuthorUsername: "y", Content: "новый\nпост", PublishedAt: &newer, Likes: 7}, IsNew: true},
		{Post: store.Post{ID: "c", URL: "https://www.threads.com/@z/post/c", AuthorUsername: "z", Content: "без даты"}, IsNew: true},
	}

	data, err := BuildXLSX(posts, false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	all, err := f.GetRows("Все посты")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want header + 3 rows, got %d", len(all))
	}
	// Newest first, undated last
	if got := all[1][17]; got != "b" {
		t.Errorf("first row id = %q, want b", got)
	}
	if got := all[3][17]; got != "c" {
		t.Errorf("last row id = %q, want c (no date)", got)
	}
	if got := all[1][2]; got != "да" {
		t.Errorf("new flag = %q, want да", got)
	}

	fresh, err := f.GetRows("Новые")
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 3 {
		t.Fatalf("new sheet: want header + 2 rows, got %d", len(fresh))
	}

	link, target, err := f.GetCellHyperLink("Все посты", "P2")
	if err != nil || !link || target != "https://www.threads.com/@y/post/b" {
		t.Errorf("post url hyperlink = %v %q %v", link, target, err)
	}
}

func TestBuildXLSXFirstRun(t *testing.T) {
	ts := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	data, err := BuildXLSX([]store.SavedPost{
		{Post: store.Post{ID: "a", URL: "https://www.threads.com/@x/post/a", AuthorUsername: "x", Content: "c", PublishedAt: &ts}, IsNew: true},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if sheets := f.GetSheetList(); len(sheets) != 1 || sheets[0] != "Все посты" {
		t.Errorf("first run must only have the all-posts sheet, got %v", sheets)
	}
	header, _ := f.GetRows("Все посты")
	for _, h := range header[0] {
		if h == "Новый" {
			t.Error("first run must not have the «Новый» column")
		}
	}
}

func TestBuildLabeledXLSX(t *testing.T) {
	ts := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	mk := func(id, sentiment, category string, excluded bool) store.LabeledPost {
		return store.LabeledPost{
			SavedPost: store.SavedPost{Post: store.Post{ID: id, URL: "https://www.threads.com/@x/post/" + id, AuthorUsername: "x", Content: "c", PublishedAt: &ts}, IsNew: id == "a"},
			Label:     &store.PostLabel{Category: category, Market: "target", Sentiment: sentiment, Aspects: []string{"mobile_internet", "pricing"}, Language: "mixed", Reason: "r", Excluded: excluded},
		}
	}
	comment := mk("cm", "negative", "review", false)
	comment.Parent = &store.ParentRef{URL: "https://www.threads.com/@x/post/a", AuthorUsername: "x", Content: "родительский пост"}
	spamComment := mk("cs", "neutral", "spam", true)
	spamComment.Parent = comment.Parent
	main, removed, err := BuildLabeledXLSX([]store.LabeledPost{
		mk("a", "negative", "review", false),
		mk("b", "positive", "review", false),
		mk("c", "neutral", "ad", true),
	}, []store.LabeledPost{comment, spamComment}, false)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenReader(bytes.NewReader(main))
	if got := f.GetSheetList(); strings.Join(got, ",") != "Все посты,Негативные,Новые,Комментарии" {
		t.Errorf("sheets: %v", got)
	}
	all, _ := f.GetRows("Все посты")
	if len(all) != 3 || all[0][0] != "Тональность" || all[1][2] != "мобильный интернет, цены/тарифы" {
		t.Errorf("all-posts sheet: %v", all)
	}
	if neg, _ := f.GetRows("Негативные"); len(neg) != 2 {
		t.Errorf("negative sheet rows: %d", len(neg))
	}
	cm, _ := f.GetRows("Комментарии")
	if len(cm) != 2 || cm[0][0] != "К посту" || cm[1][2] != "родительский пост" || cm[1][3] != "негатив" {
		t.Errorf("comments sheet: %v", cm)
	}
	f.Close()

	r, _ := excelize.OpenReader(bytes.NewReader(removed))
	if got := r.GetSheetList(); strings.Join(got, ",") != "Удаленные_посты,Удаленные_комментарии" {
		t.Errorf("removed sheets: %v", got)
	}
	rows, _ := r.GetRows("Удаленные_посты")
	if len(rows) != 2 || rows[0][0] != "Причина удаления" || rows[1][0] != "реклама/продажа" {
		t.Errorf("removed file: %v", rows)
	}
	r.Close()

	if _, rem, _ := BuildLabeledXLSX([]store.LabeledPost{mk("a", "negative", "review", false)}, nil, true); rem != nil {
		t.Error("no removed file expected when nothing was removed")
	}
}
