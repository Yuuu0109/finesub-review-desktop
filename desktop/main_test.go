package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeLoginAndConnectionViews(t *testing.T) {
	m, err := readMetadata()
	if err != nil {
		t.Fatal(err)
	}
	a := &application{meta: m, server: defaultServer, page: "dashboard"}
	view := ui.NewTester(a.view, 1100, 760)
	if !view.HasText("连接审核服务器") {
		t.Fatal("native connection screen missing")
	}
	if err := view.Click("服务器地址"); err != nil {
		t.Fatal(err)
	}
	a.connected = true
	a.initialized = true
	a.accessConfigured = true
	view.Frame()
	if !view.HasText("进入知识库") {
		t.Fatal("native passphrase screen missing")
	}
	if !view.HasText("v" + m.Version) {
		t.Fatal("version is not from embedded application config")
	}
	a.user = object{"id": "member", "name": "测试成员", "role": "member"}
	a.adminLogin = true
	view.Frame()
	if !view.HasText("管理员登录") {
		t.Fatal("native admin login screen missing")
	}
}

func TestQuitGuardKeepsActiveRequestAndUnsavedSettings(t *testing.T) {
	a := &application{busy: true}
	if a.canExit() {
		t.Fatal("quit allowed while a mutation is in progress")
	}
	a.busy = false
	a.preferences = preferencesState{identity: "server:member", name: "new", baseName: "old"}
	if a.canExit() {
		t.Fatal("quit allowed with uncommitted settings")
	}
	if !strings.Contains(a.error, "未保存") {
		t.Fatalf("missing actionable warning: %s", a.error)
	}
	a.preferences = preferencesState{}
	if !a.canExit() {
		t.Fatalf("empty application cannot quit: %s", a.error)
	}
}

func TestNotificationIdentityAndDecodedCollections(t *testing.T) {
	rows := objects(object{"items": []any{object{"read": false}, object{"read": true}}}, "items")
	if unreadNotices(rows) != 1 {
		t.Fatal("unread count differs from backend state")
	}
	if len(objects(object{"items": rows}, "items")) != 2 {
		t.Fatal("typed and decoded collections differ")
	}
}
