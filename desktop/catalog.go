package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// catalogState belongs to the native window. Query fields are preserved when
// entering a review so that returning to the queue never changes its scope.
type catalogState struct {
	Query, Status, Category, Reviewed string
	BatchID, GroupID                  string
	Page, Total                      int
	Rows                             []object
	Dashboard                        object
	Queue                            []object
	QueueTotal                       int
	QueueQuery                       string

	StreamQuery                 string
	StreamPage, StreamTotal      int
	Streamers                   []object
	StreamerID                  string
	Profile                     object
	ProfileRaw                  bool
	Knowledge                   []object
	KnowledgeTotal              int
	KnowledgeKind               string
	Markdown                    markdown.Options
}

type catalogOption struct{ Value, Label string }

var catalogCategories = []catalogOption{
	{"", "全部分类"}, {"streamer", "主播档案"}, {"common", "通用知识"},
	{"style", "翻译风格"}, {"translation", "翻译纠错"},
}

var catalogStatuses = []catalogOption{
	{"", "全部状态"}, {"pending", "待裁决"}, {"needs_changes", "待补充"},
	{"approved", "已通过"}, {"rejected", "已驳回"},
}

func nativeLabel(value string) string {
	labels := map[string]string{
		"pending": "待裁决", "approved": "已通过", "rejected": "已驳回",
		"needs_changes": "待补充", "queued": "排队中", "processing": "处理中",
		"preview": "待确认", "confirmed": "已导入", "failed": "失败", "ready": "已发布",
		"approve": "建议通过", "changes": "建议修改", "reject": "建议驳回",
		"evidence": "证据不足", "reply": "回复", "open": "未处理", "accepted": "已采纳",
		"partial": "部分采纳", "declined": "未采纳", "more_evidence": "待补证",
		"new": "新增", "changed": "修改", "duplicate": "重复", "invalid": "无法导入",
		"renamed": "重命名", "deleted": "删除", "common": "通用知识", "streamer": "主播档案",
		"style": "翻译风格", "translation": "翻译纠错", "admin": "管理员", "member": "普通成员",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func nativeDate(value string) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.Local().Format("2006-01-02 15:04")
		}
	}
	if value == "" {
		return "—"
	}
	return value
}

func catalogChoice(c *ui.Context, selected *string, options []catalogOption, disabled bool) bool {
	changed := false
	ui.Row(c).Gap(6).Children(func() {
		for _, option := range options {
			button := ui.Button(c.Key(option.Value), option.Label).Disabled(disabled)
			if *selected == option.Value {
				button.TextColor(c.Theme().Accent)
			}
			if button.Clicked() {
				*selected = option.Value
				changed = true
			}
		}
	})
	return changed
}

func catalogParams(s catalogState, mine bool) url.Values {
	p := url.Values{}
	for key, value := range map[string]string{
		"q": s.Query, "status": s.Status, "category": s.Category,
		"reviewed": s.Reviewed, "batch_id": s.BatchID, "group_id": s.GroupID,
	} {
		if value != "" {
			p.Set(key, value)
		}
	}
	if mine {
		p.Set("mine", "true")
	}
	p.Set("page", strconv.Itoa(max(1, s.Page)))
	p.Set("limit", "30")
	return p
}

func (a *application) openEntryQueue(query string, mine bool) {
	p, _ := url.ParseQuery(query)
	s := &a.catalog
	s.Query, s.Status, s.Category, s.Reviewed = p.Get("q"), p.Get("status"), p.Get("category"), p.Get("reviewed")
	s.BatchID, s.GroupID = p.Get("batch_id"), p.Get("group_id")
	s.Page, _ = strconv.Atoi(p.Get("page"))
	s.Page = max(1, s.Page)
	if mine || p.Get("mine") == "true" {
		a.navigate("mine")
	} else {
		a.navigate("entries")
	}
}

func (a *application) openBatchEntries(batchID string) {
	a.openEntryQueue(url.Values{"batch_id": {batchID}}.Encode(), false)
}

func (a *application) openGroupEntries(groupID string) {
	a.openEntryQueue(url.Values{"group_id": {groupID}}.Encode(), false)
}

func (a *application) loadDashboard() {
	client := a.client
	queue := "status=pending"
	if !a.admin() {
		queue += "&reviewed=no"
	}
	a.run("正在读取工作台", func(ctx context.Context) (func(), error) {
		var stats, entries object
		if err := client.Get(ctx, "/dashboard", &stats); err != nil {
			return nil, err
		}
		if err := client.Get(ctx, "/entries?"+queue+"&limit=6", &entries); err != nil {
			return nil, err
		}
		return func() {
			a.catalog.Dashboard = stats
			a.catalog.Queue = objects(entries, "items")
			a.catalog.QueueTotal = num(entries, "total")
			a.catalog.QueueQuery = queue
		}, nil
	})
}

func (a *application) dashboardView(c *ui.Context) {
	s := &a.catalog
	ui.Column(c).Fill().Gap(16).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "工作台").FontSize(26).Bold().Grow(1)
			if ui.Button(c, "刷新").Disabled(a.busy).Clicked() {
				a.loadDashboard()
			}
			if ui.PrimaryButton(c, "继续审核").Disabled(a.busy || len(s.Queue) == 0).Clicked() {
				a.loadReview(str(s.Queue[0], "entry_id"), s.QueueQuery)
			}
		})
		if s.Dashboard == nil {
			ui.Text(c, "工作台尚未载入，可点击刷新重试。")
			return
		}
		counts := obj(s.Dashboard, "counts")
		ui.Row(c).Gap(12).Children(func() {
			for _, card := range []struct {
				label string
				count int
				query string
			}{
				{"知识库", num(s.Dashboard, "total"), ""},
				{"待裁决", num(counts, "pending"), "status=pending"},
				{"待补充", num(counts, "needs_changes"), "status=needs_changes"},
				{"通过待发布", num(s.Dashboard, "pending_publish"), "status=approved"},
			} {
				if ui.Button(c, fmt.Sprintf("%s  %d", card.label, card.count)).Grow(1).Disabled(a.busy).Clicked() {
					a.openEntryQueue(card.query, false)
				}
			}
		})
		ui.Scroll(c).Grow(1).Gap(12).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, fmt.Sprintf("待审队列 · %d 条", s.QueueTotal)).FontSize(18).Bold().Grow(1)
				if ui.Button(c, "查看全部").Disabled(a.busy).Clicked() {
					a.openEntryQueue(s.QueueQuery, false)
				}
			})
			if len(s.Queue) == 0 {
				ui.Text(c, "暂无待审知识。")
			}
			for _, entry := range s.Queue {
				a.catalogEntryRow(c.Key(str(entry, "id")), entry, s.QueueQuery)
			}
			ui.Text(c, "最近导入").FontSize(18).Bold()
			batches := objects(s.Dashboard, "recent_batches")
			if len(batches) == 0 {
				ui.Text(c, "暂无导入批次。")
			}
			for _, batch := range batches {
				ui.Row(c.Key(str(batch, "id"))).Gap(8).Children(func() {
					ui.Text(c, str(batch, "name")).Grow(1)
					ui.Text(c, nativeLabel(str(batch, "status"))+" · "+nativeDate(str(batch, "created_at")))
					if ui.Button(c, "查看词条").Disabled(a.busy).Clicked() {
						a.openBatchEntries(str(batch, "id"))
					}
				})
			}
			ui.Text(c, fmt.Sprintf("共 %d 条审核记录 · 分配给我 %d 条", num(s.Dashboard, "reviews"), num(s.Dashboard, "mine"))).TextColor(c.Theme().TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "我的审核").Disabled(a.busy).Clicked() {
					a.openEntryQueue("", true)
				}
				if ui.Button(c, "主播档案").Disabled(a.busy).Clicked() {
					a.navigate("streamer")
				}
				if a.admin() && ui.Button(c, "导入知识库").Disabled(a.busy).Clicked() {
					a.navigate("imports")
				}
			})
		})
	})
}

func (a *application) loadEntries() {
	client := a.client
	s := &a.catalog
	s.Page = max(1, s.Page)
	query := catalogParams(*s, a.page == "mine").Encode()
	a.run("正在读取知识库", func(ctx context.Context) (func(), error) {
		var result object
		if err := client.Get(ctx, "/entries?"+query, &result); err != nil {
			return nil, err
		}
		return func() {
			a.catalog.Rows, a.catalog.Total = objects(result, "items"), num(result, "total")
		}, nil
	})
}

func (a *application) entriesView(c *ui.Context) {
	s := &a.catalog
	mine := a.page == "mine"
	title := "知识库"
	if mine {
		title = "我的审核"
	}
	ui.Column(c).Fill().Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(26).Bold().Grow(1)
			if ui.Button(c, "刷新").Disabled(a.busy).Clicked() {
				a.loadEntries()
			}
			if ui.PrimaryButton(c, "开始审核").Disabled(a.busy || len(s.Rows) == 0).Clicked() {
				a.loadReview(str(s.Rows[0], "entry_id"), catalogParams(*s, mine).Encode())
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			submitted := ui.TextInput(c, &s.Query).Placeholder("搜索名称、别名或正文").Label("搜索词条").Grow(1).Disabled(a.busy).Submitted()
			if ui.Button(c, "查询").Disabled(a.busy).Clicked() || submitted {
				s.Page = 1
				a.loadEntries()
			}
			if ui.Button(c, "清除筛选").Disabled(a.busy).Clicked() {
				s.Query, s.Status, s.Category, s.Reviewed, s.BatchID, s.GroupID = "", "", "", "", "", ""
				s.Page = 1
				a.loadEntries()
			}
		})
		if catalogChoice(c.Key("categories"), &s.Category, catalogCategories, a.busy) {
			s.Page = 1
			a.loadEntries()
		}
		if catalogChoice(c.Key("statuses"), &s.Status, catalogStatuses, a.busy) {
			s.Page = 1
			a.loadEntries()
		}
		if catalogChoice(c.Key("reviewed"), &s.Reviewed, []catalogOption{{"", "全部审核记录"}, {"no", "我未审核"}, {"yes", "我已审核"}}, a.busy) {
			s.Page = 1
			a.loadEntries()
		}
		if s.BatchID != "" {
			ui.Text(c, "已限定为当前导入批次；清除筛选可返回全部知识。").TextColor(c.Theme().TextMuted)
		}
		if s.GroupID != "" {
			ui.Text(c, "已限定为当前关联变更组。").TextColor(c.Theme().TextMuted)
		}
		query := catalogParams(*s, mine).Encode()
		ui.List(c.Key("knowledge-list"), nil, len(s.Rows), func(i int) {
			entry := s.Rows[i]
			a.catalogEntryRow(c.Key(str(entry, "id")), entry, query)
		}).Grow(1).Gap(8).Children(func() {
			if len(s.Rows) == 0 {
				ui.Text(c, "没有找到匹配的知识。").Padding(20)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf("共 %d 条 · 第 %d / %d 页", s.Total, max(1, s.Page), max(1, (s.Total+29)/30))).Grow(1)
			if ui.Button(c, "上一页").Disabled(a.busy || s.Page <= 1).Clicked() {
				s.Page--
				a.loadEntries()
			}
			if ui.Button(c, "下一页").Disabled(a.busy || s.Page*30 >= s.Total).Clicked() {
				s.Page++
				a.loadEntries()
			}
		})
	})
}

func (a *application) catalogEntryRow(c *ui.Context, entry object, query string) {
	ui.Column(c).Gap(5).Padding(12).Border(1, c.Theme().Border).Radius(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, str(entry, "title")).Bold().Grow(1)
			ui.Text(c, nativeLabel(str(entry, "status"))).TextColor(c.Theme().Accent)
			if str(entry, "operation") == "delete" {
				ui.Text(c, "拟删除")
			}
			if ui.Button(c, "审核").Disabled(a.busy).Clicked() {
				a.loadReview(str(entry, "entry_id"), query)
			}
		})
		if summary := str(entry, "summary"); summary != "" {
			ui.Text(c, summary)
		}
		assignee := "未分配"
		for _, person := range a.people {
			if str(person, "id") == str(entry, "assignee_id") {
				assignee = str(person, "name")
				break
			}
		}
		meta := []string{nativeLabel(str(entry, "category")), str(entry, "kind"), fmt.Sprintf("v%d", num(entry, "number")), fmt.Sprintf("%d 条意见", num(entry, "review_count")), assignee, nativeDate(str(entry, "created_at"))}
		if flag(entry, "my_review") {
			meta = append(meta, "我已审核")
		}
		if flag(entry, "published") {
			meta = append(meta, "已发布")
		}
		ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (a *application) loadStreamers() {
	client := a.client
	s := &a.catalog
	s.StreamPage = max(1, s.StreamPage)
	p := url.Values{"category": {"streamer"}, "limit": {"30"}, "page": {strconv.Itoa(s.StreamPage)}}
	if s.StreamQuery != "" {
		p.Set("q", s.StreamQuery)
	}
	selected := s.StreamerID
	a.run("正在读取主播档案", func(ctx context.Context) (func(), error) {
		var list, profile, common, translation object
		if err := client.Get(ctx, "/entries?"+p.Encode(), &list); err != nil {
			return nil, err
		}
		rows := objects(list, "items")
		if selected == "" && len(rows) > 0 {
			selected = str(rows[0], "entry_id")
		}
		if selected != "" {
			if err := client.Get(ctx, "/entries/"+url.PathEscape(selected), &profile); err != nil {
				return nil, err
			}
		}
		if err := client.Get(ctx, "/entries?category=common&limit=100", &common); err != nil {
			return nil, err
		}
		if err := client.Get(ctx, "/entries?category=translation&limit=100", &translation); err != nil {
			return nil, err
		}
		knowledge := append(objects(common, "items"), objects(translation, "items")...)
		return func() {
			a.catalog.Streamers, a.catalog.StreamTotal = rows, num(list, "total")
			a.catalog.StreamerID, a.catalog.Profile = selected, profile
			a.catalog.Knowledge = knowledge
			a.catalog.KnowledgeTotal = num(common, "total") + num(translation, "total")
		}, nil
	})
}

func (a *application) loadStreamerProfile(id string) {
	client := a.client
	a.run("正在读取档案", func(ctx context.Context) (func(), error) {
		var profile object
		if err := client.Get(ctx, "/entries/"+url.PathEscape(id), &profile); err != nil {
			return nil, err
		}
		if str(obj(profile, "entry"), "category") != "streamer" {
			return nil, fmt.Errorf("未找到该主播档案")
		}
		return func() { a.catalog.StreamerID, a.catalog.Profile = id, profile }, nil
	})
}

func (a *application) streamerView(c *ui.Context) {
	s := &a.catalog
	ui.Column(c).Fill().Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "主播档案").FontSize(26).Bold().Grow(1)
			if ui.Button(c, "刷新").Disabled(a.busy).Clicked() {
				a.loadStreamers()
			}
		})
		ui.Row(c).Grow(1).Gap(16).Children(func() {
			ui.Column(c).Width(250).Gap(8).Children(func() {
				submitted := ui.TextInput(c, &s.StreamQuery).Label("搜索主播").Placeholder("搜索主播名称或别名").Disabled(a.busy).Submitted()
				if ui.Button(c, "搜索档案").Disabled(a.busy).Clicked() || submitted {
					s.StreamPage, s.StreamerID = 1, ""
					a.loadStreamers()
				}
				ui.List(c.Key("streamer-list"), nil, len(s.Streamers), func(i int) {
					entry := s.Streamers[i]
					button := ui.Button(c.Key(str(entry, "id")), str(entry, "title")).Disabled(a.busy).FillWidth()
					if str(entry, "entry_id") == s.StreamerID {
						button.TextColor(c.Theme().Accent)
					}
					if button.Clicked() {
						a.loadStreamerProfile(str(entry, "entry_id"))
					}
				}).Grow(1).Gap(6)
				ui.Text(c, fmt.Sprintf("%d 份档案 · 第 %d 页", s.StreamTotal, max(1, s.StreamPage)))
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "上一页").Disabled(a.busy || s.StreamPage <= 1).Clicked() {
						s.StreamPage--
						a.loadStreamers()
					}
					if ui.Button(c, "下一页").Disabled(a.busy || s.StreamPage*30 >= s.StreamTotal).Clicked() {
						s.StreamPage++
						a.loadStreamers()
					}
				})
			})
			ui.Scroll(c.Key("profile-detail")).Grow(1).Gap(12).Children(func() {
				entry := obj(s.Profile, "entry")
				if entry == nil {
					ui.Text(c, "暂无主播档案。导入主播知识后即可在此查看。")
					return
				}
				ui.Text(c, str(entry, "title")).FontSize(24).Bold()
				ui.Text(c, str(entry, "aliases")).TextColor(c.Theme().TextMuted)
				ui.Text(c, fmt.Sprintf("%s · v%d · %s", nativeLabel(str(entry, "status")), num(entry, "number"), nativeDate(str(entry, "created_at"))))
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "审核 / 修订档案").Disabled(a.busy).Clicked() {
						a.loadReview(str(entry, "entry_id"), "category=streamer")
					}
					label := "查看原文"
					if s.ProfileRaw {
						label = "阅读档案"
					}
					if ui.Button(c, label).Clicked() {
						s.ProfileRaw = !s.ProfileRaw
					}
				})
				content := str(entry, "content")
				if s.ProfileRaw {
					ui.TextArea(c.Key("raw-profile"), &content).ReadOnly(true).Label("档案 Markdown 原文").Height(420)
				} else {
					markdown.View(c.Key("profile-markdown"), profileComments.ReplaceAllString(content, ""), &s.Markdown)
				}
				ui.Text(c, "知识库词条").FontSize(18).Bold()
				ui.Text(c, fmt.Sprintf("最近 %d / %d 条通用知识与翻译纠错；全部内容可在知识库中分页查询。", len(s.Knowledge), s.KnowledgeTotal)).TextColor(c.Theme().TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "全部通用知识").Disabled(a.busy).Clicked() {
						a.openEntryQueue("category=common", false)
					}
					if ui.Button(c, "全部翻译纠错").Disabled(a.busy).Clicked() {
						a.openEntryQueue("category=translation", false)
					}
				})
				kinds := []string{"全部类型"}
				seen := map[string]bool{}
				for _, item := range s.Knowledge {
					kind := catalogKind(item)
					if !seen[kind] {
						seen[kind] = true
						kinds = append(kinds, kind)
					}
				}
				sort.Strings(kinds[1:])
				if s.KnowledgeKind == "" {
					s.KnowledgeKind = kinds[0]
				}
				ui.Select(c, &s.KnowledgeKind, kinds).Label("知识类型")
				for _, item := range s.Knowledge {
					if s.KnowledgeKind == "全部类型" || catalogKind(item) == s.KnowledgeKind {
						a.catalogEntryRow(c.Key("knowledge-"+str(item, "id")), item, "category="+url.QueryEscape(str(item, "category")))
					}
				}
			})
		})
	})
}

// The reader hides metadata comments as the existing profile page did; the
// raw view and every revision retain the exact source returned by the API.
var profileComments = regexp.MustCompile(`(?s)<!--.*?-->`)

func catalogKind(item object) string {
	if str(item, "category") == "translation" {
		return "翻译纠错"
	}
	if kind := str(item, "kind"); kind != "" {
		return kind
	}
	return "未标注类型"
}
