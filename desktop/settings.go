package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// preferencesState belongs to the current server and authenticated identity.
// Passwords live only here and are cleared after use or an identity change.
type preferencesState struct {
	identity       string
	tab            string
	name           string
	profile        string
	baseName       string
	baseProfile    string
	memberName     string
	baseMemberName string
	currentPass    string
	newPass        string
	createOpen     bool
	newUsername    string
	newName        string
	initialPass    string
	memberSearch   string
	audit          []object
	auditPage      int
	auditLoaded    bool
	openAudit      string
}

func (a *application) preferenceIdentity() string {
	if a.client == nil {
		return ""
	}
	return a.client.Server() + ":" + str(a.user, "id")
}

func (a *application) initPreferences() {
	p := &a.preferences
	identity := a.preferenceIdentity()
	if p.identity == identity && p.identity != "" {
		return
	}
	*p = preferencesState{
		identity: identity, tab: "members", auditPage: 1,
		name: str(a.settings, "name"), profile: str(a.settings, "profile"),
		baseName: str(a.settings, "name"), baseProfile: str(a.settings, "profile"),
		memberName: str(a.user, "name"), baseMemberName: str(a.user, "name"),
	}
}

// preferencesDirty is used by the application's native close/switch guard.
// Merely refreshing lists must not discard a name or password being edited.
func (a *application) preferencesDirty() bool {
	p := &a.preferences
	return p.identity != "" && (p.name != p.baseName || p.profile != p.baseProfile ||
		p.memberName != p.baseMemberName || p.currentPass != "" || p.newPass != "" ||
		p.newUsername != "" || p.newName != "" || p.initialPass != "")
}

func (a *application) clearPreferenceSecrets() {
	a.preferences.currentPass = ""
	a.preferences.newPass = ""
	a.preferences.initialPass = ""
}

func (a *application) loadSettings() {
	a.initPreferences()
	client := a.client
	identity := a.preferenceIdentity()
	a.run("读取设置", func(ctx context.Context) (func(), error) {
		var settings object
		var people []object
		if err := client.Get(ctx, "/settings", &settings); err != nil {
			return nil, err
		}
		if err := client.Get(ctx, "/users", &people); err != nil {
			return nil, err
		}
		return func() {
			if identity != a.preferenceIdentity() {
				return
			}
			p := &a.preferences
			// Update clean inputs from the server; keep unsaved edits intact.
			if p.name == p.baseName {
				p.name = str(settings, "name")
			}
			if p.profile == p.baseProfile {
				p.profile = str(settings, "profile")
			}
			p.baseName, p.baseProfile = str(settings, "name"), str(settings, "profile")
			a.settings, a.people = settings, people
		}, nil
	})
}

func (a *application) settingsView(c *ui.Context) {
	a.initPreferences()
	p := &a.preferences
	accountOnly := a.page == "account" || !a.admin()
	ui.Column(c).Fill().Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			title := "管理设置"
			if accountOnly {
				title = "我的账号"
			}
			ui.Text(c, title).FontSize(26).Bold().Grow(1)
			if ui.Button(c, "刷新").Disabled(a.busy).Clicked() {
				a.loadSettings()
			}
		})
		if a.preferencesDirty() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "有未保存的设置或账号输入，退出前请保存或撤销。").Grow(1)
				if ui.Button(c, "撤销未保存更改").Disabled(a.busy).Clicked() {
					a.discardPreferenceEdits()
				}
			})
		}
		if !accountOnly {
			ui.Row(c).Gap(8).Children(func() {
				for _, tab := range []struct{ id, label string }{{"members", "成员管理"}, {"knowledge", "知识库设置"}, {"audit", "操作记录"}, {"account", "我的账号"}} {
					label := tab.label
					if p.tab == tab.id {
						label = "● " + label
					}
					if ui.Button(c, label).Disabled(a.busy).Clicked() {
						p.tab = tab.id
						if p.tab == "audit" && !p.auditLoaded {
							a.loadAuditPage(1)
						}
					}
				}
			})
		}
		ui.Scroll(c).Grow(1).Gap(16).Padding(4).Children(func() {
			if accountOnly || p.tab == "account" {
				a.accountSettingsView(c)
			} else {
				switch p.tab {
				case "knowledge":
					a.knowledgeSettingsView(c)
				case "audit":
					a.auditSettingsView(c)
				default:
					a.memberSettingsView(c)
				}
			}
		})
	})
}

func (a *application) discardPreferenceEdits() {
	a.run("撤销未保存更改", func(ctx context.Context) (func(), error) {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.window, Type: mygo.MessageQuestion, Title: "撤销未保存更改",
			Message: "恢复已保存的设置，并清空尚未提交的账号和密码输入？",
			Buttons: []string{"保留输入", "撤销更改"}, DefaultButton: 0, CancelButton: 0,
		})
		if err != nil {
			return nil, err
		}
		if answer.Button != 1 {
			return func() { a.status = "已保留未保存输入" }, nil
		}
		return func() {
			p := &a.preferences
			p.name, p.profile, p.memberName = p.baseName, p.baseProfile, p.baseMemberName
			p.currentPass, p.newPass, p.initialPass = "", "", ""
			p.newUsername, p.newName, p.createOpen = "", "", false
			a.status = "已撤销未保存更改"
		}, nil
	})
}

func (a *application) accountSettingsView(c *ui.Context) {
	p := &a.preferences
	ui.Text(c, str(a.user, "name")).FontSize(20).Bold()
	ui.Text(c, str(a.user, "username") + " · " + nativeLabel(str(a.user, "role")))
	if flag(a.user, "passcode_member") {
		ui.Text(c, "审核署名")
		ui.TextInput(c, &p.memberName).Label("审核署名").Placeholder("昵称，最多 80 个字符").Disabled(a.busy)
		ui.Text(c, "署名用于显示审核记录，不会改变成员身份。")
		if ui.PrimaryButton(c, "保存署名").Disabled(a.busy || p.memberName == p.baseMemberName).Clicked() {
			name := strings.TrimSpace(p.memberName)
			if count := utf8.RuneCountInString(name); count < 1 || count > 80 {
				a.error = "审核署名需为 1–80 个字符"
				return
			}
			client := a.client
			a.run("保存审核署名", func(ctx context.Context) (func(), error) {
				var user object
				if err := client.Post(ctx, "/auth/name", object{"name": name}, &user); err != nil {
					return nil, err
				}
				return func() {
					a.user = user
					p.memberName, p.baseMemberName = str(user, "name"), str(user, "name")
					for i, person := range a.people {
						if str(person, "id") == str(user, "id") {
							a.people[i] = user
						}
					}
					a.status = "审核署名已保存"
				}, nil
			})
		}
		return
	}
	if !a.admin() {
		ui.Text(c, "此账号不提供密码修改入口，请联系管理员。")
		return
	}
	ui.Text(c, "修改密码").FontSize(18).Bold()
	ui.TextInput(c, &p.currentPass).Label("当前密码").Placeholder("当前密码").Password().Disabled(a.busy)
	ui.TextInput(c, &p.newPass).Label("新密码").Placeholder("新密码，至少 10 个字符").Password().Disabled(a.busy)
	ui.Text(c, "修改密码会退出此管理员账号的全部登录会话。")
	if ui.PrimaryButton(c, "修改密码并重新登录").Disabled(a.busy || p.currentPass == "" || p.newPass == "").Clicked() {
		if count := utf8.RuneCountInString(p.newPass); count < 10 || count > 256 {
			a.error = "新密码需为 10–256 个字符"
			return
		}
		current, next := p.currentPass, p.newPass
		client := a.client
		a.run("修改密码", func(ctx context.Context) (func(), error) {
			var result object
			if err := client.Post(ctx, "/auth/password", object{"current": current, "password": next}, &result); err != nil {
				return nil, err
			}
			return func() {
				a.clearPreferenceSecrets()
				a.status = "密码已更新，请重新登录管理员账号"
				a.refreshIdentity()
			}, nil
		})
	}
}

func (a *application) knowledgeSettingsView(c *ui.Context) {
	p := &a.preferences
	ui.Text(c, "知识库与兼容设置").FontSize(20).Bold()
	ui.TextInput(c, &p.name).Label("知识库名称").Placeholder("知识库名称").Disabled(a.busy)
	profiles := obj(a.settings, "profiles")
	keys := make([]string, 0, len(profiles))
	for key := range profiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ui.Select(c, &p.profile, keys).Label("导出兼容规则").Disabled(a.busy)
	if profile := obj(profiles, p.profile); profile != nil {
		ui.Text(c, str(profile, "name"))
		if description := str(profile, "description"); description != "" {
			ui.Text(c, description)
		}
	}
	ui.Text(c, "切换规则不会自动修改知识正文或升级原生知识库。后续导入、裁决和发布按所选规则重新校验。")
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "保存知识库设置").Disabled(a.busy || (p.name == p.baseName && p.profile == p.baseProfile)).Clicked() {
			name, profile := strings.TrimSpace(p.name), p.profile
			if count := utf8.RuneCountInString(name); count < 1 || count > 100 {
				a.error = "知识库名称需为 1–100 个字符"
				return
			}
			if _, ok := profiles[profile]; !ok {
				a.error = "请选择服务器支持的兼容规则"
				return
			}
			client := a.client
			a.run("保存知识库设置", func(ctx context.Context) (func(), error) {
				var result object
				if err := client.Put(ctx, "/settings", object{"name": name, "profile": profile}, &result); err != nil {
					return nil, err
				}
				return func() {
					p.name, p.baseName, p.profile, p.baseProfile = name, name, profile, profile
					a.settings["name"], a.settings["profile"] = name, profile
					a.status = "知识库设置已保存"
				}, nil
			})
		}
		if ui.Button(c, "恢复已保存设置").Disabled(a.busy).Clicked() {
			p.name, p.profile = p.baseName, p.baseProfile
		}
	})
}

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,80}$`)

func (a *application) memberSettingsView(c *ui.Context) {
	p := &a.preferences
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c, &p.memberSearch).Label("筛选成员").Placeholder("按署名或账号筛选").Grow(1)
		if ui.Button(c, "创建管理员账号").Disabled(a.busy).Clicked() {
			p.createOpen = true
		}
	})
	if p.createOpen {
		ui.Column(c).Gap(8).Padding(12).Border(1, c.Theme().Border).Radius(10).Children(func() {
			ui.Text(c, "创建管理员账号").FontSize(18).Bold()
			ui.TextInput(c, &p.newUsername).Label("管理员账号").Placeholder("3–80 位字母、数字、点、下划线或连字符").Disabled(a.busy)
			ui.TextInput(c, &p.newName).Label("显示名称").Placeholder("显示名称，最多 80 个字符").Disabled(a.busy)
			ui.TextInput(c, &p.initialPass).Label("初始密码").Placeholder("初始密码，至少 10 个字符").Password().Disabled(a.busy)
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "创建账号").Disabled(a.busy).Clicked() {
					a.createAdmin()
				}
				if ui.Button(c, "取消").Disabled(a.busy).Clicked() {
					p.createOpen = false
					p.newUsername, p.newName, p.initialPass = "", "", ""
				}
			})
		})
	}
	count := 0
	query := strings.ToLower(strings.TrimSpace(p.memberSearch))
	for _, person := range a.people {
		if query != "" && !strings.Contains(strings.ToLower(str(person, "name")+" "+str(person, "username")), query) {
			continue
		}
		count++
		person := person
		self := str(person, "id") == str(a.user, "id")
		ui.Column(c).Key("member:"+str(person, "id")).Gap(6).Padding(12).Border(1, c.Theme().Border).Radius(10).Children(func() {
			name := str(person, "name")
			if self {
				name += "（我）"
			}
			ui.Text(c, name).Bold()
			account := str(person, "username")
			if flag(person, "passcode_member") {
				account = "口令进入"
			}
			state := "已停用"
			if flag(person, "active") {
				state = "正常"
			}
			ui.Text(c, account+" · "+nativeLabel(str(person, "role"))+" · "+state)
			ui.Row(c).Gap(8).Children(func() {
				if !flag(person, "passcode_member") && !self {
					label, role := "设为管理员", "admin"
					if str(person, "role") == "admin" {
						label, role = "设为普通成员", "member"
					}
					if ui.Button(c, label).Disabled(a.busy).Clicked() {
						a.changeMember(person, flag(person, "active"), role)
					}
				}
				if !self {
					label := "启用账号"
					if flag(person, "active") {
						label = "停用账号"
					}
					if ui.Button(c, label).Disabled(a.busy).Clicked() {
						a.changeMember(person, !flag(person, "active"), str(person, "role"))
					}
				}
			})
		})
	}
	if count == 0 {
		ui.Text(c, "没有匹配的成员")
	}
}

func (a *application) createAdmin() {
	p := &a.preferences
	username, name, password := strings.TrimSpace(p.newUsername), strings.TrimSpace(p.newName), p.initialPass
	if !adminUsernamePattern.MatchString(username) {
		a.error = "账号需为 3–80 位字母、数字、点、下划线或连字符"
		return
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		a.error = "显示名称需为 1–80 个字符"
		return
	}
	if n := utf8.RuneCountInString(password); n < 10 || n > 256 {
		a.error = "初始密码需为 10–256 个字符"
		return
	}
	client := a.client
	a.run("创建管理员账号", func(ctx context.Context) (func(), error) {
		var person object
		if err := client.Post(ctx, "/users", object{"username": username, "name": name, "password": password, "role": "admin"}, &person); err != nil {
			return nil, err
		}
		return func() {
			p.createOpen = false
			p.newUsername, p.newName, p.initialPass = "", "", ""
			a.people = append(a.people, person)
			a.status = "管理员账号已创建"
		}, nil
	})
}

func (a *application) changeMember(person object, active bool, role string) {
	id, name := str(person, "id"), str(person, "name")
	if id == str(a.user, "id") || (flag(person, "passcode_member") && role != "member") {
		a.error = "不能执行此账号调整"
		return
	}
	client := a.client
	a.run("调整账号", func(ctx context.Context) (func(), error) {
		state := "停用"
		if active {
			state = "启用"
		}
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.window, Type: mygo.MessageWarning, Title: "调整账号",
			Message: fmt.Sprintf("将「%s」设为%s，角色为%s？", name, state, nativeLabel(role)),
			Detail: "修改后该账号的管理员登录会话会失效。", Buttons: []string{"取消", "确认"},
			DefaultButton: 0, CancelButton: 0,
		})
		if err != nil {
			return nil, err
		}
		if answer.Button != 1 {
			return func() { a.status = "已取消账号调整" }, nil
		}
		var changed object
		if err := client.Patch(ctx, "/users/"+url.PathEscape(id), object{"active": active, "role": role}, &changed); err != nil {
			return nil, err
		}
		return func() {
			for i, existing := range a.people {
				if str(existing, "id") == id {
					a.people[i] = changed
				}
			}
			a.status = "账号已更新"
		}, nil
	})
}

var auditActionNames = map[string]string{
	"auth.login": "登录", "auth.password": "修改密码", "access.enter": "口令进入",
	"access.name": "修改审核署名", "user.create": "创建账号", "user.update": "调整账号",
	"settings.update": "更新设置", "import.upload": "上传知识", "import.confirm": "确认导入",
	"entry.revise": "修订正文", "entry.assign": "分配审核", "review.record": "提交审核记录",
	"review.resolve": "处理成员意见", "review.decide": "管理员裁决", "release.request": "创建发布任务",
	"release.complete": "完成发布", "release.download": "下载发布包", "job.retry": "重试后台任务",
}

func (a *application) loadAuditPage(page int) {
	if !a.admin() || page < 1 {
		return
	}
	client := a.client
	a.run("读取操作记录", func(ctx context.Context) (func(), error) {
		var logs []object
		if err := client.Get(ctx, fmt.Sprintf("/audit?page=%d", page), &logs); err != nil {
			return nil, err
		}
		return func() {
			a.preferences.audit, a.preferences.auditPage = logs, page
			a.preferences.auditLoaded, a.preferences.openAudit = true, ""
		}, nil
	})
}

func (a *application) auditSettingsView(c *ui.Context) {
	p := &a.preferences
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf("第 %d 页 · 每页最多 50 条", p.auditPage)).Grow(1)
		if ui.Button(c, "刷新操作记录").Disabled(a.busy).Clicked() {
			a.loadAuditPage(p.auditPage)
		}
		if ui.Button(c, "上一页").Disabled(a.busy || p.auditPage <= 1).Clicked() {
			a.loadAuditPage(p.auditPage - 1)
		}
		if ui.Button(c, "下一页").Disabled(a.busy || len(p.audit) < 50).Clicked() {
			a.loadAuditPage(p.auditPage + 1)
		}
	})
	if len(p.audit) == 0 {
		ui.Text(c, "本页暂无操作记录")
	}
	for _, record := range p.audit {
		id := str(record, "id")
		action := str(record, "action")
		if label := auditActionNames[action]; label != "" {
			action = label
		}
		ui.Column(c).Key("audit:"+id).Gap(6).Padding(12).Border(1, c.Theme().Border).Radius(10).Children(func() {
			ui.Text(c, nativeDate(str(record, "created_at"))+" · "+str(record, "actor")+" · "+action)
			if ui.Button(c, "查看详情").Clicked() {
				if p.openAudit == id {
					p.openAudit = ""
				} else {
					p.openAudit = id
				}
			}
			if p.openAudit == id {
				data, _ := json.MarshalIndent(record["detail"], "", "  ")
				detail := string(data)
				ui.Text(c, "对象："+str(record, "target_id"))
				ui.TextArea(c, &detail).Label("操作详情").ReadOnly(true).Height(180)
			}
		})
	}
}
