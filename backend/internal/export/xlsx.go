// Package export builds the Excel file for a finished search.
package export

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

const excelCellLimit = 32767

type column struct {
	header string
	width  float64
	value  func(p store.SavedPost) any
	kind   string // "", "datetime", "date", "link", "wrap"
}

var columns = []column{
	{"Опубликован (UTC)", 18, func(p store.SavedPost) any { return timeOrNil(p.PublishedAt) }, "datetime"},
	{"Дата", 11, func(p store.SavedPost) any { return dateOrNil(p.PublishedAt) }, "date"},
	{"Новый", 8, func(p store.SavedPost) any { return yesNo(p.IsNew) }, ""},
	{"Автор", 20, func(p store.SavedPost) any { return p.AuthorUsername }, ""},
	{"Имя автора", 22, func(p store.SavedPost) any { return p.AuthorName }, ""},
	{"Верифицирован", 8, func(p store.SavedPost) any { return yesNo(p.AuthorVerified) }, ""},
	{"Текст", 80, func(p store.SavedPost) any { return truncate(p.Content) }, "wrap"},
	{"Лайки", 8, func(p store.SavedPost) any { return p.Likes }, ""},
	{"Ответы", 8, func(p store.SavedPost) any { return p.Replies }, ""},
	{"Репосты", 8, func(p store.SavedPost) any { return p.Reposts }, ""},
	{"Поделились", 8, func(p store.SavedPost) any { return p.Shares }, ""},
	{"Фото", 6, func(p store.SavedPost) any { return len(p.Images) }, ""},
	{"Видео", 6, func(p store.SavedPost) any { return len(p.Videos) }, ""},
	{"Ссылки в посте", 30, func(p store.SavedPost) any { return strings.Join(p.Links, " ") }, ""},
	{"Цитируемый пост", 30, func(p store.SavedPost) any { return p.QuotedPostURL }, "link"},
	{"Ссылка на пост", 40, func(p store.SavedPost) any { return p.URL }, "link"},
	{"Профиль автора", 30, func(p store.SavedPost) any { return p.AuthorURL }, "link"},
	{"ID", 14, func(p store.SavedPost) any { return p.ID }, ""},
}

// BuildXLSX returns an .xlsx with all posts (newest first) and a separate sheet with new posts only.
// On the first run of a search every post is "new", so the flag column and the "Новые" sheet are omitted.
func BuildXLSX(posts []store.SavedPost, firstRun bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	styles, err := newStyles(f)
	if err != nil {
		return nil, err
	}

	all := sortedNewestFirst(posts)
	if firstRun {
		for i := range all {
			all[i].IsNew = false
		}
	}
	var fresh []store.SavedPost
	for _, p := range all {
		if p.IsNew {
			fresh = append(fresh, p)
		}
	}

	if err := f.SetSheetName("Sheet1", "Все посты"); err != nil {
		return nil, err
	}
	if err := writeSheet(f, "Все посты", all, styles); err != nil {
		return nil, err
	}
	if !firstRun {
		if _, err := f.NewSheet("Новые"); err != nil {
			return nil, err
		}
		if err := writeSheet(f, "Новые", fresh, styles); err != nil {
			return nil, err
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type styleSet struct {
	header, datetime, date, link, wrap int
}

func newStyles(f *excelize.File) (styleSet, error) {
	var s styleSet
	var err error
	if s.header, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E7E6E6"}},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	}); err != nil {
		return s, err
	}
	dtFmt, dFmt := "yyyy-mm-dd hh:mm", "yyyy-mm-dd"
	if s.datetime, err = f.NewStyle(&excelize.Style{CustomNumFmt: &dtFmt, Alignment: &excelize.Alignment{Vertical: "top"}}); err != nil {
		return s, err
	}
	if s.date, err = f.NewStyle(&excelize.Style{CustomNumFmt: &dFmt, Alignment: &excelize.Alignment{Vertical: "top"}}); err != nil {
		return s, err
	}
	if s.link, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Color: "0563C1", Underline: "single"},
		Alignment: &excelize.Alignment{Vertical: "top"},
	}); err != nil {
		return s, err
	}
	s.wrap, err = f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	return s, err
}

func writeSheet(f *excelize.File, sheet string, posts []store.SavedPost, st styleSet) error {
	for i, c := range columns {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(sheet, col, col, c.width); err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, col+"1", c.header); err != nil {
			return err
		}
	}
	lastCol, _ := excelize.ColumnNumberToName(len(columns))
	if err := f.SetCellStyle(sheet, "A1", lastCol+"1", st.header); err != nil {
		return err
	}

	for r, p := range posts {
		row := r + 2
		for i, c := range columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			v := c.value(p)
			if v == nil || v == "" {
				continue
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return err
			}
			switch c.kind {
			case "datetime":
				_ = f.SetCellStyle(sheet, cell, cell, st.datetime)
			case "date":
				_ = f.SetCellStyle(sheet, cell, cell, st.date)
			case "wrap":
				_ = f.SetCellStyle(sheet, cell, cell, st.wrap)
			case "link":
				if err := f.SetCellHyperLink(sheet, cell, fmt.Sprint(v), "External"); err != nil {
					return err
				}
				_ = f.SetCellStyle(sheet, cell, cell, st.link)
			}
		}
	}

	if err := f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	return f.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, max(1, len(posts)+1)), nil)
}

func sortedNewestFirst(posts []store.SavedPost) []store.SavedPost {
	out := make([]store.SavedPost, len(posts))
	copy(out, posts)
	// Newest first; posts without a publish time go last
	sort.SliceStable(out, func(i, j int) bool { return newer(out[i], out[j]) })
	return out
}

func newer(a, b store.SavedPost) bool {
	switch {
	case a.PublishedAt == nil:
		return false
	case b.PublishedAt == nil:
		return true
	default:
		return a.PublishedAt.After(*b.PublishedAt)
	}
}

func timeOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func dateOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return ""
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > excelCellLimit {
		return string(r[:excelCellLimit])
	}
	return s
}
