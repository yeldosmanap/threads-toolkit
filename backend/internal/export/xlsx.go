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
	value  func(p store.LabeledPost) any
	kind   string // "", "datetime", "date", "link", "wrap"
}

var baseColumns = []column{
	{"Опубликован (UTC)", 18, func(p store.LabeledPost) any { return timeOrNil(p.PublishedAt) }, "datetime"},
	{"Дата", 11, func(p store.LabeledPost) any { return dateOrNil(p.PublishedAt) }, "date"},
	{"Новый", 8, func(p store.LabeledPost) any { return yesNo(p.IsNew) }, ""},
	{"Автор", 20, func(p store.LabeledPost) any { return p.AuthorUsername }, ""},
	{"Имя автора", 22, func(p store.LabeledPost) any { return p.AuthorName }, ""},
	{"Верифицирован", 8, func(p store.LabeledPost) any { return yesNo(p.AuthorVerified) }, ""},
	{"Текст", 80, func(p store.LabeledPost) any { return truncate(p.Content) }, "wrap"},
	{"Лайки", 8, func(p store.LabeledPost) any { return p.Likes }, ""},
	{"Ответы", 8, func(p store.LabeledPost) any { return p.Replies }, ""},
	{"Репосты", 8, func(p store.LabeledPost) any { return p.Reposts }, ""},
	{"Поделились", 8, func(p store.LabeledPost) any { return p.Shares }, ""},
	{"Фото", 6, func(p store.LabeledPost) any { return len(p.Images) }, ""},
	{"Видео", 6, func(p store.LabeledPost) any { return len(p.Videos) }, ""},
	{"Ссылки в посте", 30, func(p store.LabeledPost) any { return strings.Join(p.Links, " ") }, ""},
	{"Цитируемый пост", 30, func(p store.LabeledPost) any { return p.QuotedPostURL }, "link"},
	{"Ссылка на пост", 40, func(p store.LabeledPost) any { return p.URL }, "link"},
	{"Профиль автора", 30, func(p store.LabeledPost) any { return p.AuthorURL }, "link"},
	{"ID", 14, func(p store.LabeledPost) any { return p.ID }, ""},
}

// BuildXLSX returns an .xlsx with all posts (newest first) and a separate sheet with new posts only.
// On the first run of a search every post is "new", so the flag column and the "Новые" sheet are omitted.
func BuildXLSX(posts []store.SavedPost, firstRun bool) ([]byte, error) {
	rows := make([]store.LabeledPost, len(posts))
	for i, p := range posts {
		rows[i] = store.LabeledPost{SavedPost: p}
	}
	return buildSheets(postSheets(rows, firstRun, baseColumns), firstRun)
}

// BuildLabeledXLSX builds the two files of an analyzed project run:
//   - main: kept posts with sentiment, category and aspects (sheets «Все посты», «Негативные», «Новые»)
//     plus «Комментарии» collected under negative posts;
//   - «Удаленные_посты»: official, ads, spam, offtopic, other country - with the reason
//     (and «Удаленные_комментарии» for excluded comments). removed is nil if nothing was removed.
func BuildLabeledXLSX(posts, comments []store.LabeledPost, firstRun bool) (main, removed []byte, err error) {
	kept, dropped := splitExcluded(posts)
	keptComments, droppedComments := splitExcluded(comments)

	cols := append(append([]column{}, labelColumns...), baseColumns...)
	sheets := postSheets(kept, firstRun, cols)
	if len(keptComments) > 0 {
		sheets = append(sheets, sheet{"Комментарии", keptComments, commentColumns(cols)})
	}
	if main, err = buildSheets(sheets, firstRun); err != nil {
		return nil, nil, err
	}

	var removedSheets []sheet
	if len(dropped) > 0 {
		removedSheets = append(removedSheets, sheet{"Удаленные_посты", dropped, append([]column{removalColumn}, cols...)})
	}
	if len(droppedComments) > 0 {
		removedSheets = append(removedSheets, sheet{"Удаленные_комментарии", droppedComments, append([]column{removalColumn}, commentColumns(cols)...)})
	}
	if len(removedSheets) > 0 {
		if removed, err = buildSheets(removedSheets, firstRun); err != nil {
			return nil, nil, err
		}
	}
	return main, removed, nil
}

type sheet struct {
	name string
	rows []store.LabeledPost
	cols []column
}

func splitExcluded(posts []store.LabeledPost) (kept, dropped []store.LabeledPost) {
	for _, p := range posts {
		if p.Label != nil && p.Label.Excluded {
			dropped = append(dropped, p)
		} else {
			kept = append(kept, p)
		}
	}
	return kept, dropped
}

// commentColumns: which post the comment belongs to, then the usual columns (no "Новый" - comments aren't tracked as new).
func commentColumns(cols []column) []column {
	out := []column{
		{"К посту", 30, func(p store.LabeledPost) any { return parentField(p, func(r *store.ParentRef) string { return r.URL }) }, "link"},
		{"Автор поста", 18, func(p store.LabeledPost) any {
			return parentField(p, func(r *store.ParentRef) string { return "@" + r.AuthorUsername })
		}, ""},
		{"Текст поста", 50, func(p store.LabeledPost) any {
			return parentField(p, func(r *store.ParentRef) string { return shorten(r.Content, 300) })
		}, "wrap"},
	}
	for _, c := range cols {
		if c.header != "Новый" {
			out = append(out, c)
		}
	}
	return out
}

func parentField(p store.LabeledPost, get func(*store.ParentRef) string) any {
	if p.Parent == nil {
		return nil
	}
	return get(p.Parent)
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// postSheets: «Все посты», «Негативные» (when labeled) and «Новые» (except on the first run).
func postSheets(posts []store.LabeledPost, firstRun bool, cols []column) []sheet {
	all := sortedNewestFirst(posts)
	if firstRun {
		for i := range all {
			all[i].IsNew = false
		}
	}
	sheets := []sheet{{"Все посты", all, cols}}
	if hasLabels(all) {
		var neg []store.LabeledPost
		for _, p := range all {
			if p.Label != nil && p.Label.Sentiment == "negative" {
				neg = append(neg, p)
			}
		}
		sheets = append(sheets, sheet{"Негативные", neg, cols})
	}
	if !firstRun {
		var fresh []store.LabeledPost
		for _, p := range all {
			if p.IsNew {
				fresh = append(fresh, p)
			}
		}
		sheets = append(sheets, sheet{"Новые", fresh, cols})
	}
	return sheets
}

func buildSheets(sheets []sheet, firstRun bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	styles, err := newStyles(f)
	if err != nil {
		return nil, err
	}
	for i, sh := range sheets {
		cols := sh.cols
		if firstRun {
			// "Новый" carries no information on the first run
			filtered := cols[:0:0]
			for _, c := range cols {
				if c.header != "Новый" {
					filtered = append(filtered, c)
				}
			}
			cols = filtered
		}
		if i == 0 {
			if err := f.SetSheetName("Sheet1", sh.name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(sh.name); err != nil {
			return nil, err
		}
		if err := writeSheet(f, sh.name, cols, sortedNewestFirst(sh.rows), styles); err != nil {
			return nil, err
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hasLabels(posts []store.LabeledPost) bool {
	for _, p := range posts {
		if p.Label != nil {
			return true
		}
	}
	return false
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

func writeSheet(f *excelize.File, sheet string, columns []column, posts []store.LabeledPost, st styleSet) error {
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

func sortedNewestFirst(posts []store.LabeledPost) []store.LabeledPost {
	out := make([]store.LabeledPost, len(posts))
	copy(out, posts)
	// Newest first; posts without a publish time go last
	sort.SliceStable(out, func(i, j int) bool { return newer(out[i], out[j]) })
	return out
}

func newer(a, b store.LabeledPost) bool {
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
