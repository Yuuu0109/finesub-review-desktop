package main

import (
	"os"
	"testing"

	"finesub-review-desktop/internal/client"
	"github.com/egoist/mygo/ui"
)

// This adapter only serves missing credentials. Constructing the HTTP client
// reads it but makes no request; these view tests never call a network action.
type settingsTestSecrets struct{}

func (settingsTestSecrets) Get(string) ([]byte, error) { return nil, os.ErrNotExist }
func (settingsTestSecrets) Set(string, []byte) error   { return nil }
func (settingsTestSecrets) Delete(string) error        { return nil }

func newSettingsTestApplication(t *testing.T, role string) *application {
	t.Helper()
	api, err := client.New("https://review.example.test", settingsTestSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	user := object{"id": "current-user", "username": "reviewer", "name": "当前审核成员", "role": role, "active": true, "passcode_member": role == "member"}
	return &application{
		page: "settings", client: api, user: user,
		people: []object{
			user,
			{"id": "other-member", "username": "visitor-other", "name": "另一个成员", "role": "member", "active": true, "passcode_member": true},
		},
		settings: object{
			"name": "测试知识库", "profile": "legacy-preserve",
			"profiles": object{
				"legacy-preserve": object{"name": "现有 Markdown · 保留分类"},
				"finesub-0.6.0":   object{"name": "FineSub 0.6.0 · schema 6"},
			},
		},
	}
}

func TestNativeSettingsMemberOnlySeesOwnAccount(t *testing.T) {
	app := newSettingsTestApplication(t, "member")
	view := ui.NewTester(app.settingsView, 1280, 860)
	for _, want := range []string{"我的账号", "当前审核成员", "审核署名", "保存署名"} {
		if !view.HasText(want) {
			t.Errorf("member account view omitted %q: %q", want, view.Texts())
		}
	}
	for _, forbidden := range []string{"管理设置", "成员管理", "知识库设置", "操作记录", "创建管理员账号", "修改密码并重新登录"} {
		if view.HasText(forbidden) {
			t.Errorf("ordinary member was offered %q", forbidden)
		}
	}
}

func TestNativeSettingsAdminCanSwitchMembersAndKnowledge(t *testing.T) {
	app := newSettingsTestApplication(t, "admin")
	view := ui.NewTester(app.settingsView, 1280, 860)
	for _, want := range []string{"管理设置", "创建管理员账号", "另一个成员"} {
		if !view.HasText(want) {
			t.Errorf("admin member view omitted %q: %q", want, view.Texts())
		}
	}
	if err := view.Click("知识库设置"); err != nil {
		t.Fatal(err)
	}
	if app.preferences.tab != "knowledge" || !view.HasText("知识库与兼容设置") || !view.HasText("保存知识库设置") {
		t.Fatalf("native tab did not show knowledge settings: tab=%q, texts=%q", app.preferences.tab, view.Texts())
	}
	if err := view.Click("成员管理"); err != nil {
		t.Fatal(err)
	}
	if app.preferences.tab != "members" || !view.HasText("创建管理员账号") {
		t.Fatalf("native tab did not return to members: %q", view.Texts())
	}
}

func TestSettingsEditsRemainUntilSavedOrDiscardConfirmed(t *testing.T) {
	app := newSettingsTestApplication(t, "admin")
	view := ui.NewTester(app.settingsView, 1280, 860)
	app.preferences.name = "尚未保存的名称"
	app.preferences.profile = "finesub-0.6.0"
	app.preferences.currentPass = "current password"
	app.preferences.newPass = "new password"
	app.preferences.newUsername = "new-admin"
	app.preferences.newName = "尚未创建的管理员"
	app.preferences.initialPass = "initial password"
	before := app.preferences

	// Rendering and tab changes are not consent to discard. In particular,
	// initPreferences must not overwrite edited fields with the server values.
	view.Frame()
	if err := view.Click("知识库设置"); err != nil {
		t.Fatal(err)
	}
	if !app.preferencesDirty() || app.preferences.name != before.name || app.preferences.profile != before.profile {
		t.Fatal("rendering or selecting a tab discarded unsaved knowledge settings")
	}
	if app.canExit() {
		t.Fatal("native close/relaunch was allowed with unconfirmed settings edits")
	}
	if !view.HasText("撤销未保存更改") {
		t.Fatalf("dirty inputs have no explicit discard entry: %q", view.Texts())
	}

	// A task in progress blocks the discard entry before a native confirmation
	// can be shown. This also avoids starting an OS dialog in a headless test.
	app.busy = true
	app.discardPreferenceEdits()
	if app.preferences.name != before.name || app.preferences.currentPass != before.currentPass ||
		app.preferences.newPass != before.newPass || app.preferences.initialPass != before.initialPass ||
		app.preferences.newUsername != before.newUsername || app.preferences.newName != before.newName {
		t.Fatal("blocked discard changed settings or account inputs before confirmation")
	}
}

func TestSettingsIdentityChangeClearsPreviousAccountInputs(t *testing.T) {
	app := newSettingsTestApplication(t, "admin")
	view := ui.NewTester(app.settingsView, 1280, 860)
	app.preferences.name = "上一个身份的未保存名称"
	app.preferences.currentPass, app.preferences.newPass, app.preferences.initialPass = "old secret", "new secret", "initial secret"
	app.user = object{"id": "next-member", "name": "新成员", "role": "member", "passcode_member": true}
	view.Frame()
	if app.preferences.currentPass != "" || app.preferences.newPass != "" || app.preferences.initialPass != "" {
		t.Fatal("password inputs leaked across authenticated identities")
	}
	if app.preferences.memberName != "新成员" || app.preferences.name != "测试知识库" || app.preferencesDirty() {
		t.Fatalf("new identity inherited old edits: %+v", app.preferences)
	}
	if !view.HasText("我的账号") || view.HasText("创建管理员账号") {
		t.Fatal("identity change left administrator controls visible")
	}
}
