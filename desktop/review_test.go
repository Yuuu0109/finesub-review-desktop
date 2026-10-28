package main

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"finesub-review-desktop/internal/client"
	"finesub-review-desktop/internal/localstore"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type reviewTestSecrets struct{}
func (reviewTestSecrets) Get(string) ([]byte,error) { return nil, os.ErrNotExist }
func (reviewTestSecrets) Set(string,[]byte) error { return nil }
func (reviewTestSecrets) Delete(string) error { return nil }

func TestReviewNextKeepsScopeAfterCurrentItemLeavesQueue(t *testing.T) {
	q, err := url.ParseQuery(reviewNextQuery("q=中文&status=pending&category=common&batch_id=batch&group_id=linked&mine=true&reviewed=no&page=8&limit=30&after=old&revision_id=wrong", "handled-revision"))
	if err != nil { t.Fatal(err) }
	for key, want := range map[string]string{"q":"中文", "status":"pending", "category":"common", "batch_id":"batch", "group_id":"linked", "mine":"true", "reviewed":"no", "after":"handled-revision", "limit":"1"} {
		if q.Get(key) != want { t.Fatalf("%s = %q, want %q", key, q.Get(key), want) }
	}
	if q.Has("page") || q.Has("revision_id") { t.Fatalf("next cursor includes stale page/version: %v", q) }
}

func reviewFixture(t *testing.T, store *localstore.Store, user, role, revision string) *application {
	t.Helper()
	transport, err := client.New("https://review.example.test", reviewTestSecrets{})
	if err != nil { t.Fatal(err) }
	a := &application{client:transport, store:store, user:object{"id":user,"role":role}}
	a.installReview("entry", "status=pending", "previous", object{"entry":object{
		"id":revision,"entry_id":"entry","current_revision":revision,
		"content":"# 原文\n正文  \n","source":"原始来源","operation":"upsert",
	}}, nil)
	return a
}

func TestNativeReviewDraftsSurviveRestartWithoutCrossingIdentity(t *testing.T) {
	store, err := localstore.Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	a := reviewFixture(t, store, "member-a", "member", "revision-1")
	a.review.draft.Body = "尚未提交的审核意见"
	a.review.draft.ReplyTarget = "record-1"
	a.review.draft.ReplyRevision = "revision-1"
	a.review.draft.ReplyBody = "尚未提交的回复"
	a.review.dirty = true
	if err := a.saveReviewDrafts(); err != nil { t.Fatal(err) }
	restarted := reviewFixture(t, store, "member-a", "member", "revision-1")
	if restarted.review.draft.Body != a.review.draft.Body || restarted.review.draft.ReplyBody != a.review.draft.ReplyBody {
		t.Fatal("restart did not restore the review and reply drafts")
	}
	other := reviewFixture(t, store, "member-b", "member", "revision-1")
	if other.review.draft.Body != "" || other.review.draft.ReplyBody != "" { t.Fatal("another member received the draft") }
	newVersion := reviewFixture(t, store, "member-a", "member", "revision-2")
	if newVersion.review.draft.Body != "" { t.Fatal("old-version opinion silently moved to a new revision") }
}

func TestNativeEditKeepsStaleBaseAndExactMarkdownForManualMerge(t *testing.T) {
	store, err := localstore.Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	a := reviewFixture(t, store, "admin", "admin", "revision-1")
	a.review.edit.Content = "# 原文\n含有  双空格的修订\n\n"
	a.review.draft.ResolutionNote = "尚未提交的处理说明"
	a.review.draft.ResolveTarget = "record-1"
	a.review.dirty = true
	if err := a.saveReviewDrafts(); err != nil { t.Fatal(err) }
	reloaded := reviewFixture(t, store, "admin", "admin", "revision-2")
	if reloaded.review.edit.BaseRevision != "revision-1" { t.Fatal("refresh rebased the stale edit without a manual merge") }
	if reloaded.review.edit.Content != a.review.edit.Content { t.Fatal("Markdown was normalized or overwritten while restoring") }
	if !reloaded.review.editDirty() { t.Fatal("restored stale edit is not shown as dirty") }
	old := reviewFixture(t, store, "admin", "admin", "revision-1")
	if old.review.draft.ResolutionNote != a.review.draft.ResolutionNote { t.Fatal("opinion resolution draft was lost") }
}

func TestReviewStorageSeparatesServerPortUserEntryAndRevision(t *testing.T) {
	keys := map[string]bool{}
	for _, v := range [][4]string{
		{"https://review.test","u","e","r"},
		{"https://review.test:8443","u","e","r"},
		{"https://review.test","u2","e","r"},
		{"https://review.test","u/e","r","x"},
		{"https://review.test","u","e/r","x"},
		{"https://review.test","u","e","r2"},
	} {
		key, _ := reviewStorageKeys(v[0],v[1],v[2],v[3])
		if keys[key] { t.Fatalf("colliding draft namespace: %q", key) }
		keys[key] = true
	}
}

func TestExpiredIdentityStillFlushesDraftUnderOriginalOwner(t *testing.T) {
	store, err := localstore.Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	a := reviewFixture(t, store, "admin", "admin", "revision-1")
	a.review.edit.Content = "管理员会话过期前尚未落盘的正文"
	a.review.dirty = true
	// A 401 may clear the live identity before the next autosave callback. The
	// loaded draft's owner and edit capability must still be used for the flush.
	a.user = nil
	if err := a.saveReviewDrafts(); err != nil { t.Fatal(err) }
	restored := reviewFixture(t, store, "admin", "admin", "revision-1")
	if restored.review.edit.Content != a.review.edit.Content { t.Fatal("session expiry discarded the pending edit") }
	member := reviewFixture(t, store, "member", "member", "revision-1")
	if member.review.edit.Content == a.review.edit.Content { t.Fatal("draft was reassigned to another identity") }
}

func nativeReviewUIFixture(t *testing.T, role string) (*application, *ui.Tester) {
	t.Helper()
	store, err := localstore.Open(t.TempDir())
	if err != nil { t.Fatal(err) }
	a := reviewFixture(t, store, "native-ui-user", role, "revision-2")
	entry := obj(a.review.detail, "entry")
	entry["title"], entry["category"], entry["status"], entry["number"] = "原生审核测试词条", "common", "pending", 2
	a.review.detail["comparison"] = object{"number":1,"id":"revision-1","kind":"previous"}
	a.review.diff = &editor.DiffState{
		Old: editor.NewBuffer("# 原文\n旧正文\n"), New: editor.NewBuffer(a.review.raw),
		Layout: editor.DiffSplit, Wrap: true,
	}
	// A tall test surface keeps both the document and form visible. These
	// tests exercise the real native layout and input handlers, not a mock IPC.
	return a, ui.NewTester(a.reviewView, 1100, 2200)
}

func TestNativeReviewTabsAndChineseEditingKeepSourceIntact(t *testing.T) {
	a, view := nativeReviewUIFixture(t, "admin")
	original := str(obj(a.review.detail, "entry"), "content")
	if !view.HasText("原生审核测试词条") || !view.HasText("提交审核意见") {
		t.Fatalf("native review did not render: %q", view.Texts())
	}
	for _, tab := range []struct{label, value, expected string}{
		{"原文", "raw", "Markdown 原文"},
		{"版本差异", "diff", "基线 v1 → 当前展示 v2"},
		{"修订", "edit", "Markdown 正文"},
		{"正文", "read", "引用已复制的原文"},
	} {
		if err := view.Click(tab.label); err != nil { t.Fatalf("switch %s: %v", tab.label, err) }
		if a.review.tab != tab.value || !view.HasText(tab.expected) {
			t.Fatalf("tab %s did not show native content: state=%s texts=%q", tab.label, a.review.tab, view.Texts())
		}
	}
	if err := view.Click("意见说明（必填）"); err != nil { t.Fatal(err) }
	view.Type("中文审核意见：请核实节目来源。")
	if a.review.draft.Body != "中文审核意见：请核实节目来源。" { t.Fatalf("native Chinese input was lost: %q", a.review.draft.Body) }
	if a.review.raw != original || str(obj(a.review.detail,"entry"),"content") != original || a.review.edit.Content != original {
		t.Fatal("typing an opinion modified the source or revision draft")
	}
	if err := view.Click("修订"); err != nil { t.Fatal(err) }
	if err := view.Click("Markdown 正文"); err != nil { t.Fatal(err) }
	view.Type("中文正文修订")
	if !strings.Contains(a.review.edit.Content,"中文正文修订") || !a.review.editDirty() { t.Fatal("native revision editor did not update its draft") }
	if a.review.raw != original || str(obj(a.review.detail,"entry"),"content") != original { t.Fatal("editing a draft changed the server snapshot") }
	if err := a.saveReviewDrafts(); err != nil { t.Fatal(err) }
	if a.review.dirty { t.Fatal("native input was not flushed for restart") }
}

func TestNativeMemberReviewHasNoAdministratorActions(t *testing.T) {
	a, view := nativeReviewUIFixture(t, "member")
	for _, forbidden := range []string{"修订", "Markdown 正文", "确认管理员决定", "裁决理由", "处理意见"} {
		if _, found := view.Find(forbidden); found { t.Fatalf("ordinary member sees administrator action %q", forbidden) }
	}
	if err := view.Click("意见说明（必填）"); err != nil { t.Fatal(err) }
	view.Type("普通成员中文意见")
	if a.review.draft.Body != "普通成员中文意见" { t.Fatal("member's review form does not accept native input") }
	if _, found := view.Find("提交记录"); !found { t.Fatal("member cannot submit a review") }
	if err := view.Click("原文"); err != nil { t.Fatal(err) }
	if err := view.Click("Markdown 原文"); err != nil { t.Fatal(err) }
	original := a.review.raw
	view.Type("不应写入只读原文")
	if a.review.raw != original { t.Fatal("member changed the read-only source") }
}
