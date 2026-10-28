package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

// Review drafts are scoped to the authenticated user and server. In particular,
// a resumed ordinary-member session must not adopt another member's draft.
type reviewDraft struct {
	Verdict        string `json:"verdict"`
	Body           string `json:"body"`
	Quote          string `json:"quote"`
	Suggestion     string `json:"suggestion"`
	Evidence       string `json:"evidence"`
	DecisionStatus string `json:"decisionStatus"`
	DecisionReason string `json:"decisionReason"`
	ReplyTarget    string `json:"replyTarget"`
	ReplyRevision  string `json:"replyRevision"`
	ReplyBody       string `json:"replyBody"`
	ResolveTarget  string `json:"resolveTarget"`
	Resolution     string `json:"resolution"`
	ResolutionNote string `json:"resolutionNote"`
}

type reviewEditDraft struct {
	Content      string `json:"content"`
	Source       string `json:"source"`
	BaseRevision string `json:"baseRevision"`
	Delete       bool   `json:"delete"`
}

type reviewState struct {
	entryID      string
	query        string
	anchor       string
	detail       object
	tab          string
	compare      string
	raw          string
	rawSelection string
	diff         *editor.DiffState
	markdown     markdown.Options
	draft        reviewDraft
	edit         reviewEditDraft
	showHistory  bool
	showMetadata bool
	replyOpen    bool
	resolveOpen  bool
	continueNext bool
	queueDone    bool
	draftError   string
	dirty        bool
	editable     bool
	// These keys belong to the session that loaded this page. Saving must not
	// silently retarget a draft after logout or a server change.
	draftKey string
	editKey  string
	timer    *time.Timer
}

type reviewOption struct{ value, label string }

var reviewVerdicts = []reviewOption{{"approve", "建议通过"}, {"changes", "建议修改"}, {"reject", "建议驳回"}, {"evidence", "证据不足"}}
var reviewDecisions = []reviewOption{{"approved", "通过，等待发布"}, {"needs_changes", "待补充 / 需修改"}, {"rejected", "驳回"}, {"pending", "重新进入待裁决"}}
var reviewResolutions = []reviewOption{{"open", "未处理"}, {"accepted", "已采纳"}, {"partial", "部分采纳"}, {"declined", "未采纳"}, {"more_evidence", "待补证"}}

func reviewLabel(value string) string {
	for _, options := range [][]reviewOption{reviewVerdicts, reviewDecisions, reviewResolutions} {
		for _, option := range options {
			if option.value == value {
				return option.label
			}
		}
	}
	switch value {
	case "reply":
		return "回复"
	case "common":
		return "通用知识"
	case "streamer":
		return "主播档案"
	case "style":
		return "翻译风格"
	case "translation":
		return "翻译纠错"
	}
	return value
}

func reviewSelect(c *ui.Context, label string, value *string, options []reviewOption, disabled bool) bool {
	labels := make([]string, 0, len(options))
	current := *value
	for _, option := range options {
		labels = append(labels, option.label)
		if option.value == *value {
			current = option.label
		}
	}
	if !ui.Select(c, &current, labels).Label(label).Disabled(disabled).Changed() {
		return false
	}
	for _, option := range options {
		if option.label == current {
			*value = option.value
			return true
		}
	}
	return false
}

func reviewStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

// A decision/review can remove the current item from its filter. The revision
// cursor survives that change; carrying an offset into this request skips work.
func reviewNextQuery(query, anchor string) string {
	input, _ := url.ParseQuery(query)
	output := url.Values{}
	for _, key := range []string{"q", "status", "category", "batch_id", "mine", "reviewed", "group_id"} {
		if value := input.Get(key); value != "" {
			output.Set(key, value)
		}
	}
	if query == "" {
		output.Set("status", "pending")
	}
	output.Set("limit", "1")
	output.Set("after", anchor)
	return output.Encode()
}

func reviewStorageKeys(server, user, entry, revision string) (string, string) {
	// Escaping each component keeps the namespace unambiguous, including origins
	// with a port. The local store hashes this key before using it as a filename.
	prefix := "review/" + url.QueryEscape(server) + "/" + url.QueryEscape(user) + "/" + url.QueryEscape(entry)
	return prefix + "/revision/" + url.QueryEscape(revision), prefix + "/edit"
}

func (a *application) openReview(id string) { a.loadReview(id, "status=pending") }

func (a *application) loadReview(id, query string) {
	if a.busy || a.client == nil || id == "" {
		return
	}
	if err := a.saveReviewDrafts(); err != nil {
		a.error = "草稿保存失败，暂不能切换词条：" + err.Error()
		return
	}
	a.loadReviewRevision(id, query, "", "previous", true)
}

func (a *application) fetchReview(ctx context.Context, id, revision, comparison string) (object, *editor.DiffState, error) {
	query := url.Values{"revision_id": {revision}, "compare_to": {comparison}}
	var detail object
	if err := a.client.Get(ctx, "/entries/"+url.PathEscape(id)+"?"+query.Encode(), &detail); err != nil {
		return nil, nil, err
	}
	entry := obj(detail, "entry")
	if str(entry, "id") == "" || str(entry, "entry_id") != id {
		return nil, nil, errors.New("服务器返回了无效的词条版本")
	}
	old := ""
	if base := str(obj(detail, "comparison"), "id"); base != "" {
		if base == str(entry, "id") {
			old = str(entry, "content")
		} else {
			var baseline object
			q := url.Values{"revision_id": {base}}
			if err := a.client.Get(ctx, "/entries/"+url.PathEscape(id)+"?"+q.Encode(), &baseline); err != nil {
				return nil, nil, fmt.Errorf("读取差异基线失败：%w", err)
			}
			old = str(obj(baseline, "entry"), "content")
		}
	}
	return detail, &editor.DiffState{
		Old: editor.NewBuffer(old), New: editor.NewBuffer(str(entry, "content")),
		Layout: editor.DiffSplit, Wrap: true,
	}, nil
}

func (a *application) loadReviewRevision(id, query, revision, comparison string, fresh bool) {
	a.run("读取审核词条", func(ctx context.Context) (func(), error) {
		detail, diff, err := a.fetchReview(ctx, id, revision, comparison)
		if err != nil {
			return nil, err
		}
		return func() {
			if fresh { a.review.entryID = "" }
			a.installReview(id, query, comparison, detail, diff)
			a.page = "review"
		}, nil
	})
}

func (a *application) installReview(id, query, comparison string, detail object, diff *editor.DiffState) {
	previous := a.review
	if previous.timer != nil {
		previous.timer.Stop()
	}
	entry := obj(detail, "entry")
	s := reviewState{
		entryID: id, query: query, anchor: str(entry, "id"), detail: detail,
		compare: comparison, tab: "read", raw: str(entry, "content"), diff: diff,
		continueNext: true, editable: a.admin(),
		draft: reviewDraft{Verdict: "changes", DecisionStatus: "approved", Resolution: "accepted"},
		edit: reviewEditDraft{Content: str(entry, "content"), Source: str(entry, "source"), BaseRevision: str(entry, "id"), Delete: str(entry, "operation") == "delete"},
	}
	if num(entry, "number") > 1 {
		s.tab = "diff"
	}
	if previous.entryID == id && previous.query == query {
		s.anchor, s.tab = previous.anchor, previous.tab
		s.showHistory, s.showMetadata, s.continueNext = previous.showHistory, previous.showMetadata, previous.continueNext
		if s.tab == "edit" && str(entry, "id") != str(entry, "current_revision") {
			s.tab = "read"
		}
	}
	s.draftKey, s.editKey = reviewStorageKeys(a.client.Server(), str(a.user, "id"), id, str(entry, "id"))
	if a.store != nil {
		if err := a.store.Get(s.draftKey, &s.draft); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.draftError = "读取审核草稿失败：" + err.Error()
		}
		if str(entry, "id") == str(entry, "current_revision") && a.admin() {
			if err := a.store.Get(s.editKey, &s.edit); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.draftError = "读取正文草稿失败：" + err.Error()
			}
		}
	}
	if !reviewKnown(s.draft.Verdict, reviewVerdicts) {
		s.draft.Verdict = "changes"
	}
	if !reviewKnown(s.draft.DecisionStatus, reviewDecisions) {
		s.draft.DecisionStatus = "approved"
	}
	if !reviewKnown(s.draft.Resolution, reviewResolutions) {
		s.draft.Resolution = "accepted"
	}
	s.replyOpen = s.draft.ReplyTarget != ""
	s.resolveOpen = s.draft.ResolveTarget != ""
	a.review = s
	if s.draftError != "" {
		a.error = s.draftError
	}
}

func reviewKnown(value string, options []reviewOption) bool {
	for _, option := range options {
		if option.value == value {
			return true
		}
	}
	return false
}

func (s *reviewState) current() bool {
	entry := obj(s.detail, "entry")
	return str(entry, "id") != "" && str(entry, "id") == str(entry, "current_revision")
}

func (s *reviewState) editDirty() bool {
	entry := obj(s.detail, "entry")
	return s.current() && (s.edit.Content != str(entry, "content") || s.edit.Source != str(entry, "source") || s.edit.Delete != (str(entry, "operation") == "delete"))
}

func (a *application) scheduleReviewSave() {
	s := &a.review
	s.dirty = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if a.window == nil {
		return
	}
	s.timer = time.AfterFunc(400*time.Millisecond, func() {
		a.window.Update(func() {
			if err := a.saveReviewDrafts(); err != nil {
				a.error = "草稿保存失败：" + err.Error()
			}
		})
	})
}

func (a *application) saveReviewDrafts() error {
	s := &a.review
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.detail == nil || !s.dirty {
		return nil
	}
	if a.store == nil {
		return errors.New("本地草稿存储不可用")
	}
	if err := a.store.Set(s.draftKey, s.draft); err != nil {
		s.draftError = err.Error()
		return err
	}
	if s.editable && s.current() {
		if err := a.store.Set(s.editKey, s.edit); err != nil {
			s.draftError = err.Error()
			return err
		}
	}
	s.dirty, s.draftError = false, ""
	return nil
}

func (a *application) reviewView(c *ui.Context) {
	s := &a.review
	if s.detail == nil {
		ui.Text(c, "请从知识库选择审核词条。")
		return
	}
	entry := obj(s.detail, "entry")
	ui.Column(c).Fill().Gap(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "返回审核队列").Disabled(a.busy).Clicked() {
				if err := a.saveReviewDrafts(); err != nil {
					a.error = err.Error()
				} else {
					queue, _ := url.ParseQuery(s.query)
					a.openEntryQueue(s.query, queue.Get("mine") == "true")
				}
			}
			if ui.Button(c, "刷新当前版本").Disabled(a.busy).Clicked() {
				a.reloadReview("")
			}
			if ui.Button(c, "下一条").Disabled(a.busy || s.queueDone).Clicked() {
				a.nextReview()
			}
			ui.Text(c, fmt.Sprintf("%s · v%d · %s", reviewLabel(str(entry, "category")), num(entry, "number"), reviewLabel(str(entry, "status"))))
		})
		ui.Text(c, str(entry, "title")).FontSize(24).Bold()
		if !s.current() {
			ui.Text(c, "正在查看历史版本；正文修订和裁决只对当前版本开放。")
		}
		if s.queueDone {
			ui.Text(c, "已到当前审核队列末尾。")
		}
		if s.draftError != "" {
			ui.Text(c, "草稿未能保存："+s.draftError)
			if ui.Button(c, "重试保存草稿").Disabled(a.busy).Clicked() {
				s.dirty = true
				if err := a.saveReviewDrafts(); err != nil { a.error = err.Error() }
			}
		}
		ui.Scroll(c).Grow(1).Gap(14).Children(func() {
			ui.Checkbox(c, &s.showMetadata, "来源、负责人、版本历史和关联变更")
			if s.showMetadata {
				a.reviewMetadata(c)
			}
			for _, message := range reviewStrings(s.detail["errors"]) {
				ui.Text(c, "格式错误："+message)
			}
			for _, message := range reviewStrings(s.detail["warnings"]) {
				ui.Text(c, "核实提示："+message)
			}
			ui.Row(c).Gap(8).Children(func() {
				for _, tab := range []reviewOption{{"read", "正文"}, {"raw", "原文"}, {"diff", "版本差异"}, {"edit", "修订"}} {
					if tab.value == "edit" && (!a.admin() || !s.current()) { continue }
					if ui.Button(c, tab.label).Disabled(a.busy).Clicked() { s.tab = tab.value }
				}
			})
			switch s.tab {
			case "diff":
				choice := s.compare
				if reviewSelect(c, "对比基线", &choice, []reviewOption{{"previous", "上一版本"}, {"published", "当前正式版本"}}, a.busy) {
					a.compareReview(choice)
				}
				base := obj(s.detail, "comparison")
				ui.Text(c, fmt.Sprintf("基线 v%d → 当前展示 v%d", num(base, "number"), num(entry, "number")))
				if s.diff != nil { editor.DiffView(c, s.diff).Height(440).FillWidth() }
			case "raw":
				input := ui.TextArea(c, &s.raw).Label("Markdown 原文").ReadOnly(true).Height(420).FillWidth()
				start, end := input.TextSelection()
				runes := []rune(s.raw)
				if start >= 0 && end > start && end <= len(runes) { s.rawSelection = string(runes[start:end]) }
				if ui.Button(c, "引用所选原文").Disabled(a.busy || s.rawSelection == "").Clicked() {
					s.draft.Quote = s.rawSelection
					a.scheduleReviewSave()
				}
			case "edit":
				a.reviewEditor(c)
			default:
				markdown.View(c, str(entry, "content"), &s.markdown)
				if ui.Button(c, "引用已复制的原文").Disabled(a.busy).Clicked() {
					text := strings.TrimSpace(mygo.Clipboard.ReadText())
					if text == "" || !strings.Contains(str(entry, "content"), text) {
						a.error = "请先复制当前正文中的文字，或在原文页选择文字。"
					} else {
						s.draft.Quote = text
						a.scheduleReviewSave()
					}
				}
			}
			if s.editDirty() && s.tab != "edit" { ui.Text(c, "有未提交的正文修订草稿；请在修订页保存或丢弃后再裁决。") }
			a.reviewDecisionView(c)
			a.reviewRecordsView(c)
			a.reviewFormView(c)
		})
	})
}

func (a *application) reviewMetadata(c *ui.Context) {
	s := &a.review
	entry := obj(s.detail, "entry")
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "路径："+str(entry, "path"))
		ui.Text(c, "别名："+str(entry, "aliases"))
		ui.Text(c, "来源："+str(entry, "source"))
		if str(entry, "operation") == "delete" { ui.Text(c, "此版本为删除提案，通过并发布后才会删除正式词条。") }
		if flag(entry, "published") { ui.Text(c, "当前展示版本已经正式发布。") }
		assignee := str(entry, "assignee_id")
		options := []reviewOption{{"", "未分配"}}
		for _, person := range a.people {
			if flag(person, "active") { options = append(options, reviewOption{str(person, "id"), str(person, "name")+" ("+str(person, "username")+")"}) }
		}
		if a.admin() {
			if reviewSelect(c, "负责人", &assignee, options, a.busy) { a.assignReview(assignee) }
		} else {
			name := "未分配"
			for _, person := range a.people { if str(person, "id") == assignee { name = str(person, "name") } }
			ui.Text(c, "负责人："+name)
			if assignee == "" || assignee == str(a.user, "id") {
				label := "认领此词条"
				if assignee != "" { label = "取消认领" }
				if ui.Button(c, label).Disabled(a.busy).Clicked() {
					if assignee == "" { a.assignReview(str(a.user, "id")) } else { a.assignReview("") }
				}
			}
		}
		ui.Text(c, "版本历史").Bold()
		for _, version := range objects(s.detail, "versions") {
			if ui.Button(c, fmt.Sprintf("v%d · %s · %s", num(version, "number"), reviewLabel(str(version, "status")), str(version, "created_at"))).Disabled(a.busy).Clicked() {
				a.reloadReview(str(version, "id"))
			}
		}
		for _, related := range objects(s.detail, "related") {
			if ui.Button(c, "关联变更："+str(related, "title")+" · "+reviewLabel(str(related, "status"))).Disabled(a.busy).Clicked() {
				a.loadReview(str(related, "entry_id"), url.Values{"group_id": {str(entry, "group_id")}}.Encode())
			}
		}
	})
}

func (a *application) reviewEditor(c *ui.Context) {
	s := &a.review
	entry := obj(s.detail, "entry")
	stale := s.edit.BaseRevision != str(entry, "id")
	if stale {
		ui.Text(c, "草稿基于旧版本。请对照最新原文手动合并，完成后再确认基础版本。")
		if ui.Button(c, "已手动合并，使用当前版本作为基础").Disabled(a.busy).Clicked() {
			s.edit.BaseRevision = str(entry, "id")
			a.scheduleReviewSave()
		}
	}
	if ui.TextArea(c, &s.edit.Content).Label("Markdown 正文").Height(440).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
	if ui.TextArea(c, &s.edit.Source).Label("来源说明").Height(80).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
	if ui.Checkbox(c, &s.edit.Delete, "删除提案：通过并发布后删除此词条").Disabled(a.busy).Changed() { a.scheduleReviewSave() }
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "保存为新版本").Disabled(a.busy || stale || !s.editDirty()).Clicked() { a.submitRevision() }
		if ui.Button(c, "丢弃正文草稿").Disabled(a.busy || !s.editDirty()).Clicked() { a.discardReviewEdit() }
	})
}

func (a *application) reviewDecisionView(c *ui.Context) {
	s := &a.review
	entry := obj(s.detail, "entry")
	ui.Column(c).Gap(8).Padding(12).Children(func() {
		ui.Text(c, "管理员决定 · "+reviewLabel(str(entry, "status"))).FontSize(18).Bold()
		for _, decision := range objects(s.detail, "decisions") {
			if str(decision, "revision_id") == str(entry, "id") {
				ui.Text(c, str(decision, "reason"))
				ui.Text(c, str(decision, "author")+" · "+str(decision, "created_at"))
				break
			}
		}
		if !a.admin() || !s.current() || flag(entry, "published") { return }
		if reviewSelect(c, "裁决", &s.draft.DecisionStatus, reviewDecisions, a.busy) { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.DecisionReason).Label("裁决理由").Height(95).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		ui.Checkbox(c, &s.continueNext, "裁决后继续审核下一条").Disabled(a.busy)
		invalid := s.draft.DecisionStatus == "approved" && len(reviewStrings(s.detail["errors"])) > 0
		if invalid { ui.Text(c, "当前内容有格式错误，不能通过。") }
		if ui.PrimaryButton(c, "确认管理员决定").Disabled(a.busy || s.editDirty() || invalid || strings.TrimSpace(s.draft.DecisionReason) == "").Clicked() { a.submitReviewDecision() }
	})
}

func (a *application) reviewRecordsView(c *ui.Context) {
	s := &a.review
	entry := obj(s.detail, "entry")
	ui.Text(c, "审核记录与回复").FontSize(18).Bold()
	ui.Checkbox(c, &s.showHistory, "包含其他版本的记录")
	visible := 0
	for _, record := range objects(s.detail, "reviews") {
		if str(record, "parent_id") != "" || (!s.showHistory && str(record, "revision_id") != str(entry, "id")) { continue }
		visible++
		ui.Column(c).Gap(6).Padding(12).Children(func() {
			ui.Text(c, str(record, "author")+" · "+reviewLabel(str(record, "verdict"))+" · "+str(record, "created_at")).Bold()
			if flag(record, "stale") { ui.Text(c, "历史版本记录") }
			if quote := str(record, "quote"); quote != "" { ui.Text(c, "引用："+quote) }
			ui.Text(c, str(record, "body"))
			if suggestion := str(record, "suggestion"); suggestion != "" { ui.Text(c, "建议文本："+suggestion) }
			if evidence := str(record, "evidence"); evidence != "" { ui.Text(c, "依据："+evidence) }
			if resolution := str(record, "resolution"); resolution != "open" { ui.Text(c, "处理结果："+reviewLabel(resolution)+" · "+str(record, "resolution_note")) }
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "回复").Disabled(a.busy).Clicked() {
					if s.draft.ReplyTarget != "" && s.draft.ReplyTarget != str(record, "id") && strings.TrimSpace(s.draft.ReplyBody) != "" {
						a.error = "请先提交或清空当前回复草稿，再回复另一条记录。"
					} else {
						s.draft.ReplyTarget, s.draft.ReplyRevision = str(record, "id"), str(record, "revision_id")
						s.replyOpen = true
						a.scheduleReviewSave()
					}
				}
				if a.admin() && ui.Button(c, "处理意见").Disabled(a.busy).Clicked() {
					if s.draft.ResolveTarget != "" && s.draft.ResolveTarget != str(record, "id") && strings.TrimSpace(s.draft.ResolutionNote) != "" {
						a.error = "请先保存或清空当前处理说明，再处理另一条记录。"
					} else {
						if s.draft.ResolveTarget != str(record, "id") {
							s.draft.Resolution, s.draft.ResolutionNote = str(record, "resolution"), str(record, "resolution_note")
							if s.draft.Resolution == "open" || s.draft.Resolution == "" { s.draft.Resolution = "accepted" }
						}
						s.draft.ResolveTarget = str(record, "id")
						s.resolveOpen = true
						a.scheduleReviewSave()
					}
				}
			})
			for _, reply := range objects(s.detail, "reviews") {
				if str(reply, "parent_id") == str(record, "id") {
					ui.Text(c, "↳ "+str(reply, "author")+" · "+str(reply, "created_at")+"\n"+str(reply, "body"))
				}
			}
		})
	}
	if visible == 0 { ui.Text(c, "暂无审核记录。") }
	if s.draft.ReplyTarget != "" && !s.replyOpen {
		if ui.Button(c, "继续回复草稿").Disabled(a.busy).Clicked() { s.replyOpen = true }
	}
	if s.draft.ReplyTarget != "" && s.replyOpen {
		ui.Text(c, "回复审核记录").Bold()
		for _, record := range objects(s.detail, "reviews") { if str(record, "id") == s.draft.ReplyTarget { ui.Text(c, str(record, "author")+"："+str(record, "body")) } }
		if ui.TextArea(c, &s.draft.ReplyBody).Label("回复内容").Height(100).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		if ui.PrimaryButton(c, "提交回复").Disabled(a.busy || strings.TrimSpace(s.draft.ReplyBody) == "").Clicked() { a.submitReviewReply() }
		if ui.Button(c, "关闭回复（保留草稿）").Disabled(a.busy).Clicked() { s.replyOpen = false; a.scheduleReviewSave() }
	}
	if a.admin() && s.draft.ResolveTarget != "" && !s.resolveOpen {
		if ui.Button(c, "继续处理意见草稿").Disabled(a.busy).Clicked() { s.resolveOpen = true }
	}
	if a.admin() && s.draft.ResolveTarget != "" && s.resolveOpen {
		ui.Text(c, "处理成员意见").Bold()
		if reviewSelect(c, "处理结果", &s.draft.Resolution, reviewResolutions, a.busy) { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.ResolutionNote).Label("处理说明").Height(100).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		ui.Text(c, "采纳意见只记录处理结果；正文需要另行修订。")
		if ui.PrimaryButton(c, "保存处理结果").Disabled(a.busy || strings.TrimSpace(s.draft.ResolutionNote) == "").Clicked() { a.resolveReview() }
		if ui.Button(c, "关闭处理说明（保留草稿）").Disabled(a.busy).Clicked() { s.resolveOpen = false; a.scheduleReviewSave() }
	}
}

func (a *application) reviewFormView(c *ui.Context) {
	s := &a.review
	ui.Column(c).Gap(8).Padding(12).Children(func() {
		ui.Text(c, "提交审核意见").FontSize(18).Bold()
		if reviewSelect(c, "你的判断", &s.draft.Verdict, reviewVerdicts, a.busy) { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.Body).Label("意见说明（必填）").Height(115).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.Quote).Label("引用原文").Height(70).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.Suggestion).Label("建议文本").Height(100).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		if ui.TextArea(c, &s.draft.Evidence).Label("依据（节目期数、时间码或来源链接）").Height(80).FillWidth().Disabled(a.busy).Changed() { a.scheduleReviewSave() }
		ui.Text(c, "审核意见不会自动改变管理员裁决。")
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "提交记录").Disabled(a.busy || strings.TrimSpace(s.draft.Body) == "").Clicked() { a.submitReviewRecord(false) }
			if ui.PrimaryButton(c, "提交并继续").Disabled(a.busy || strings.TrimSpace(s.draft.Body) == "").Clicked() { a.submitReviewRecord(true) }
		})
	})
}

func (a *application) reloadReview(revision string) {
	if a.busy { return }
	if err := a.saveReviewDrafts(); err != nil { a.error = err.Error(); return }
	s := &a.review
	a.loadReviewRevision(s.entryID, s.query, revision, s.compare, false)
}

func (a *application) compareReview(comparison string) {
	id, revision := a.review.entryID, str(obj(a.review.detail, "entry"), "id")
	a.run("读取版本差异", func(ctx context.Context) (func(), error) {
		detail, diff, err := a.fetchReview(ctx, id, revision, comparison)
		if err != nil { return nil, err }
		return func() {
			// Only comparison data changes. Unsaved form values and the edit base
			// must remain exactly what the user was editing.
			a.review.diff = diff
			a.review.compare = comparison
			a.review.detail["comparison"] = detail["comparison"]
			a.review.detail["diff"] = detail["diff"]
		}, nil
	})
}

func (a *application) nextReview() {
	if a.busy { return }
	if err := a.saveReviewDrafts(); err != nil { a.error = err.Error(); return }
	query, anchor := a.review.query, a.review.anchor
	a.run("读取下一条审核", func(ctx context.Context) (func(), error) {
		var result object
		if err := a.client.Get(ctx, "/entries?"+reviewNextQuery(query, anchor), &result); err != nil { return nil, err }
		items := objects(result, "items")
		if len(items) == 0 { return func() { a.review.queueDone = true }, nil }
		id := str(items[0], "entry_id")
		detail, diff, err := a.fetchReview(ctx, id, "", "previous")
		if err != nil { return nil, err }
		return func() { a.installReview(id, query, "previous", detail, diff); a.page = "review" }, nil
	})
}

func (a *application) reviewMutationSaved(message string, next bool) {
	a.review.dirty = true
	if err := a.saveReviewDrafts(); err != nil {
		a.error = message + "，但本地草稿状态未能保存：" + err.Error()
		return
	}
	a.status = message
	if next { a.nextReview() } else { a.reloadReview(str(obj(a.review.detail, "entry"), "id")) }
}

func (a *application) submitReviewRecord(next bool) {
	s := &a.review
	entry := obj(s.detail, "entry")
	if s.draft.Quote != "" && !strings.Contains(str(entry, "content"), s.draft.Quote) {
		a.error = "引用文字不在当前展示的版本中，请重新选择。"
		return
	}
	id := s.entryID
	payload := object{"revision_id": str(entry, "id"), "verdict": s.draft.Verdict, "body": s.draft.Body, "quote": s.draft.Quote, "suggestion": s.draft.Suggestion, "evidence": s.draft.Evidence}
	a.run("提交审核记录", func(ctx context.Context) (func(), error) {
		if err := a.client.Post(ctx, "/entries/"+url.PathEscape(id)+"/reviews", payload, nil); err != nil { return nil, err }
		return func() {
			a.review.draft.Body, a.review.draft.Quote, a.review.draft.Suggestion, a.review.draft.Evidence = "", "", "", ""
			a.reviewMutationSaved("审核记录已提交", next)
		}, nil
	})
}

func (a *application) submitReviewReply() {
	s := &a.review
	id := s.entryID
	payload := object{"revision_id": s.draft.ReplyRevision, "parent_id": s.draft.ReplyTarget, "verdict": "reply", "body": s.draft.ReplyBody}
	a.run("提交回复", func(ctx context.Context) (func(), error) {
		if err := a.client.Post(ctx, "/entries/"+url.PathEscape(id)+"/reviews", payload, nil); err != nil { return nil, err }
		return func() {
			a.review.draft.ReplyTarget, a.review.draft.ReplyRevision, a.review.draft.ReplyBody = "", "", ""
			a.reviewMutationSaved("回复已保存", false)
		}, nil
	})
}

func (a *application) resolveReview() {
	s := &a.review
	id := s.draft.ResolveTarget
	payload := object{"resolution": s.draft.Resolution, "note": s.draft.ResolutionNote}
	a.run("保存意见处理结果", func(ctx context.Context) (func(), error) {
		if err := a.client.Patch(ctx, "/reviews/"+url.PathEscape(id), payload, nil); err != nil { return nil, err }
		return func() {
			a.review.draft.ResolveTarget, a.review.draft.ResolutionNote = "", ""
			a.reviewMutationSaved("意见处理结果已保存", false)
		}, nil
	})
}

func (a *application) assignReview(userID string) {
	id := a.review.entryID
	var value any
	if userID != "" { value = userID }
	a.run("分配审核任务", func(ctx context.Context) (func(), error) {
		if err := a.client.Put(ctx, "/entries/"+url.PathEscape(id)+"/assignee", object{"user_id": value}, nil); err != nil { return nil, err }
		return func() { a.status = "审核任务已更新"; a.reloadReview(str(obj(a.review.detail, "entry"), "id")) }, nil
	})
}

func (a *application) submitReviewDecision() {
	s := &a.review
	entry := obj(s.detail, "entry")
	if !a.admin() || !s.current() || s.editDirty() || flag(entry, "published") { return }
	id, next := s.entryID, s.continueNext
	payload := object{"revision_id": str(entry, "id"), "status": s.draft.DecisionStatus, "reason": s.draft.DecisionReason}
	title := str(entry, "title")
	a.run("保存管理员决定", func(ctx context.Context) (func(), error) {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.window, Type: mygo.MessageQuestion, Title: "确认管理员决定", Message: title + " · " + reviewLabel(str(payload, "status")), Detail: str(payload, "reason"), Buttons: []string{"取消", "确认"}, DefaultButton: 0, CancelButton: 0})
		if err != nil { return nil, err }
		if answer.Button != 1 { return nil, nil }
		if err := a.client.Post(ctx, "/entries/"+url.PathEscape(id)+"/decision", payload, nil); err != nil { return nil, err }
		return func() { a.review.draft.DecisionReason = ""; a.reviewMutationSaved("管理员决定已保存", next) }, nil
	})
}

func (a *application) submitRevision() {
	s := &a.review
	if !a.admin() || !s.current() || s.edit.BaseRevision != str(obj(s.detail, "entry"), "id") { return }
	id := s.entryID
	payload := object{"base_revision": s.edit.BaseRevision, "content": s.edit.Content, "source": s.edit.Source, "operation": "upsert"}
	if s.edit.Delete { payload["operation"] = "delete" }
	a.run("保存正文新版本", func(ctx context.Context) (func(), error) {
		var revision object
		if err := a.client.Post(ctx, "/entries/"+url.PathEscape(id)+"/revisions", payload, &revision); err != nil { return nil, err }
		return func() {
			// POST succeeded even if a later refresh fails. Persist the returned
			// base before refreshing so that an old base is never re-submitted.
			a.review.edit.BaseRevision = str(revision, "id")
			a.review.tab = "diff"
			a.review.dirty = true
			if err := a.saveReviewDrafts(); err != nil { a.error = "新版本已保存，但草稿状态保存失败："+err.Error(); return }
			a.status = "新版本已保存"
			a.reloadReview("")
		}, nil
	})
}

func (a *application) discardReviewEdit() {
	entry := obj(a.review.detail, "entry")
	a.run("处理修订草稿", func(ctx context.Context) (func(), error) {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.window, Type: mygo.MessageQuestion, Title: "丢弃正文草稿", Message: "恢复当前展示版本的正文与来源？", Buttons: []string{"保留草稿", "丢弃"}, DefaultButton: 0, CancelButton: 0})
		if err != nil { return nil, err }
		if answer.Button != 1 { return nil, nil }
		return func() {
			a.review.edit = reviewEditDraft{Content: str(entry, "content"), Source: str(entry, "source"), BaseRevision: str(entry, "id"), Delete: str(entry, "operation") == "delete"}
			a.review.dirty = true
			if err := a.saveReviewDrafts(); err != nil { a.error = err.Error() }
		}, nil
	})
}
