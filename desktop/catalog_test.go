package main

import (
	"net/url"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCatalogQueuePreservesScopeAndEscapesSearch(t *testing.T) {
	state := catalogState{
		Query: "美香&status=rejected", Status: "pending", Category: "streamer", Reviewed: "no",
		BatchID: "batch-1", GroupID: "group-1", Page: 4,
	}
	params, err := url.ParseQuery(catalogParams(state, true).Encode())
	if err != nil {
		t.Fatal(err)
	}
	if params.Get("q") != state.Query || params.Get("status") != "pending" {
		t.Fatalf("search text escaped its parameter: %v", params)
	}
	for key, expected := range map[string]string{
		"category": "streamer", "reviewed": "no", "batch_id": "batch-1", "group_id": "group-1", "mine": "true", "page": "4",
	} {
		if params.Get(key) != expected {
			t.Errorf("return-to-queue scope lost %s: %v", key, params)
		}
	}
	if params.Has("after") {
		t.Fatal("the queue page must not also carry a next-review cursor")
	}
	if catalogParams(catalogState{}, false).Get("page") != "1" {
		t.Fatal("a newly opened queue must start on page 1")
	}
}

func TestNativeKnowledgeListDisplaysReviewMetadata(t *testing.T) {
	app := &application{
		page: "mine", busy: true,
		user:   object{"id": "member-1", "role": "member"},
		people: []object{{"id": "member-1", "name": "审核成员"}},
		catalog: catalogState{Page: 1, Total: 1, Rows: []object{{
			"id": "revision-1", "entry_id": "entry-1", "title": "测试知识",
			"summary": "本行包含完整的审核摘要", "category": "common", "kind": "人物",
			"status": "pending", "number": float64(3), "review_count": float64(2),
			"assignee_id": "member-1", "my_review": true,
		}}},
	}
	view := ui.NewTester(app.entriesView, 1280, 860)
	for _, text := range []string{"我的审核", "测试知识", "本行包含完整的审核摘要", "待裁决"} {
		if !view.HasText(text) {
			t.Errorf("native list omitted %q: %q", text, view.Texts())
		}
	}
}

func TestNativeStreamerRendersMarkdownWithoutChangingSource(t *testing.T) {
	const source = "# 主播完整档案\n\n原文包含 **强调内容**。\n\n## 重要经历\n\n- 第一条\n- 第二条\n"
	app := &application{page: "streamer", busy: true, user: object{"role": "member"}, catalog: catalogState{
		StreamerID: "streamer-1", StreamPage: 1, StreamTotal: 1,
		Streamers: []object{{"id": "rev-1", "entry_id": "streamer-1", "title": "主播名字"}},
		Profile:   object{"entry": object{"entry_id": "streamer-1", "title": "主播名字", "category": "streamer", "content": source}},
	}}
	view := ui.NewTester(app.streamerView, 1280, 860)
	if !view.HasText("主播完整档案") {
		t.Fatalf("native Markdown did not render the profile: %q", view.Texts())
	}
	if got := str(obj(app.catalog.Profile, "entry"), "content"); got != source {
		t.Fatalf("reading a profile rewrote Markdown: %q", got)
	}
}
