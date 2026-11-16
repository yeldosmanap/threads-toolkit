package export

import (
	"bytes"
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

	data, err := BuildXLSX(posts)
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
