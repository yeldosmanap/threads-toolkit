package api

import (
	"net/url"
	"testing"
)

func TestParsePostsQuery(t *testing.T) {
	q, msg := parsePostsQuery(url.Values{})
	if msg != "" || q.Page != 1 || q.PageSize != 50 || q.Sort != "newest" || q.OnlyNew || q.Offset != 0 {
		t.Fatalf("defaults: %+v %q", q, msg)
	}

	q, msg = parsePostsQuery(url.Values{"onlyNew": {"1"}, "q": {"  тариф   kz "}, "sort": {"likes"}, "page": {"3"}, "pageSize": {"20"}})
	if msg != "" || !q.OnlyNew || q.Search != "тариф kz" || q.Sort != "likes" || q.Limit != 20 || q.Offset != 40 {
		t.Fatalf("parsed: %+v %q", q, msg)
	}

	for _, bad := range []url.Values{
		{"onlyNew": {"maybe"}},
		{"sort": {"p.likes; DROP TABLE posts"}},
		{"page": {"0"}},
		{"page": {"abc"}},
		{"pageSize": {"101"}},
		{"q": {string(make([]rune, 101))}},
	} {
		if _, msg := parsePostsQuery(bad); msg == "" {
			t.Errorf("expected error for %v", bad)
		}
	}
}
