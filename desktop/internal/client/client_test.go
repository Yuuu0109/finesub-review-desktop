package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memorySecrets struct {
	mu sync.Mutex
	values map[string][]byte
	fail bool
}

func (s *memorySecrets) Get(key string) ([]byte,error) {
	s.mu.Lock(); defer s.mu.Unlock()
	if s.fail { return nil, errors.New("credential store locked") }
	data, ok := s.values[key]
	if !ok { return nil, os.ErrNotExist }
	return append([]byte(nil),data...),nil
}
func (s *memorySecrets) Set(key string,data []byte) error {
	s.mu.Lock(); defer s.mu.Unlock()
	if s.fail { return errors.New("credential store locked") }
	if s.values == nil { s.values = make(map[string][]byte) }
	s.values[key] = append([]byte(nil),data...)
	return nil
}
func (s *memorySecrets) Delete(key string) error {
	s.mu.Lock(); defer s.mu.Unlock()
	if s.fail { return errors.New("credential store locked") }
	delete(s.values,key)
	return nil
}

func mustClient(t *testing.T,server string,secrets Secrets) *Client {
	t.Helper()
	c,err := New(server,secrets)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ c.Close() })
	return c
}

func TestOriginAndPathValidation(t *testing.T) {
	for _, raw := range []string{"http://public.example", "https://u:p@example.com", "https://example.com/api", "https://example.com?token=x", "https://example.com/#x", "file:///tmp", "example.com", "https://example.com:0", "https://example.com:99999"} {
		if _,err := NormalizeServer(raw); err == nil { t.Errorf("accepted invalid origin %q",raw) }
	}
	for raw,want := range map[string]string{" https://EXAMPLE.com:443/ ":"https://example.com", "http://localhost:80/":"http://localhost", "http://[::1]:8765":"http://[::1]:8765"} {
		got,err := NormalizeServer(raw)
		if err != nil || got != want { t.Errorf("NormalizeServer(%q) = %q, %v",raw,got,err) }
	}
	c := mustClient(t,"https://example.com",&memorySecrets{})
	for _,path := range []string{"//evil.example/x","/../admin","/%2e%2e/admin","/%252e%252e/admin","/x/../../admin","/\\evil","/entries#x","/entries bad","https://evil.example"} {
		if _,err := c.apiURL(path); err == nil { t.Errorf("accepted invalid path %q",path) }
	}
	if got,err := c.apiURL("/entries?q=%E7%BE%8E%E9%A6%99&page=2"); err != nil || got != "https://example.com/api/entries?q=%E7%BE%8E%E9%A6%99&page=2" { t.Fatalf("valid query: %q %v",got,err) }
}

func TestMemberIdentityAndCSRFSurviveClientRestart(t *testing.T) {
	var entered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Content-Type","application/json")
		switch r.URL.Path {
		case "/api/auth/enter":
			entered.Add(1)
			http.SetCookie(w,&http.Cookie{Name:"review_access",Value:"same-member-token",Path:"/",MaxAge:3600,HttpOnly:true})
			fmt.Fprint(w,`{"user":{"id":"member-1"},"csrf":"member-csrf"}`)
		case "/api/auth/me":
			cookie,err := r.Cookie("review_access")
			if err != nil || cookie.Value != "same-member-token" { w.WriteHeader(401); fmt.Fprint(w,`{"detail":"请先进入"}`); return }
			fmt.Fprint(w,`{"user":{"id":"member-1"},"csrf":"member-csrf"}`)
		case "/api/entries/e/reviews":
			if r.Header.Get("X-CSRF-Token") != "member-csrf" { t.Error("missing restored csrf") }
			if _,err := r.Cookie("review_access"); err != nil { t.Error("missing member cookie") }
			fmt.Fprint(w,`{"id":"review-1"}`)
		default: t.Errorf("unexpected request %s",r.URL.Path)
		}
	}))
	defer server.Close()
	secrets := &memorySecrets{}
	first := mustClient(t,server.URL,secrets)
	var auth struct{User struct{ID string `json:"id"`} `json:"user"`}
	if err := first.Post(context.Background(),"/auth/enter",map[string]string{"passphrase":"not-persisted"},&auth); err != nil { t.Fatal(err) }
	first.Close()
	second := mustClient(t,server.URL,secrets)
	if err := second.Get(context.Background(),"/auth/me",&auth); err != nil { t.Fatal(err) }
	if auth.User.ID != "member-1" || entered.Load() != 1 { t.Fatalf("identity not restored; id=%q enters=%d",auth.User.ID,entered.Load()) }
	if err := second.Post(context.Background(),"/entries/e/reviews",map[string]string{"body":"原文"},nil); err != nil { t.Fatal(err) }
	for _, value := range secrets.values { if strings.Contains(string(value),"not-persisted") { t.Fatal("passphrase persisted") } }
}

func TestLogoutKeepsMemberAndLeaveClearsPersistedCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		switch r.URL.Path {
		case "/api/auth/enter":
			http.SetCookie(w,&http.Cookie{Name:"review_access",Value:"member",Path:"/",MaxAge:3600})
			fmt.Fprint(w,`{"csrf":"member-csrf"}`)
		case "/api/auth/login":
			if r.Header.Get("X-CSRF-Token")!="member-csrf" { t.Error("member csrf not used") }
			http.SetCookie(w,&http.Cookie{Name:"review_session",Value:"admin",Path:"/",MaxAge:3600})
			fmt.Fprint(w,`{"csrf":"admin-csrf"}`)
		case "/api/auth/logout":
			if r.Header.Get("X-CSRF-Token")!="admin-csrf" { t.Error("admin csrf not used") }
			http.SetCookie(w,&http.Cookie{Name:"review_session",Path:"/",MaxAge:-1})
			fmt.Fprint(w,`{"csrf":"member-csrf"}`)
		case "/api/auth/leave":
			if r.Header.Get("X-CSRF-Token")!="member-csrf" { t.Error("logout csrf not refreshed") }
			http.SetCookie(w,&http.Cookie{Name:"review_access",Path:"/",MaxAge:-1})
			http.SetCookie(w,&http.Cookie{Name:"review_session",Path:"/",MaxAge:-1})
			fmt.Fprint(w,`{"ok":true}`)
		}
	}))
	defer server.Close()
	secrets := &memorySecrets{}
	c := mustClient(t,server.URL,secrets)
	for _, path := range []string{"/auth/enter","/auth/login","/auth/logout"} {
		if err:=c.Post(context.Background(),path,nil,nil); err!=nil { t.Fatal(err) }
	}
	restored:=mustClient(t,server.URL,secrets)
	if restored.cookies["review_access"].Value!="member" || restored.cookies["review_session"].Value!="" { t.Fatal("admin logout must preserve only member identity") }
	if err:=c.Post(context.Background(),"/auth/leave",nil,nil); err!=nil { t.Fatal(err) }
	afterLeave:=mustClient(t,server.URL,secrets)
	if len(afterLeave.cookies)!=0 || c.csrf!="" { t.Fatal("leave did not clear authentication") }
}

func TestSessionsAreScopedToExactOriginAndExpiration(t *testing.T) {
	secrets:=&memorySecrets{}
	a:=mustClient(t,"https://one.example:8443",secrets)
	if err:=a.receiveCookies([]*http.Cookie{{Name:"review_access",Value:"only-a",Path:"/",MaxAge:3600,Secure:true}});err!=nil {t.Fatal(err)}
	b:=mustClient(t,"https://one.example:9443",secrets)
	if len(b.cookies)!=0 {t.Fatal("session leaked across origins")}
	entry:=sessionCookie{Name:"review_access",Value:"expired",Expires:time.Now().Add(-time.Hour)}
	data,_:=json.Marshal(entry)
	secrets.Set(a.secretKey("review_access"),data)
	restored:=mustClient(t,a.Server(),secrets)
	if len(restored.cookies)!=0 {t.Fatal("expired session restored")}
	if _,err:=secrets.Get(a.secretKey("review_access"));!errors.Is(err,os.ErrNotExist){t.Fatal("expired secret not removed")}
}

func TestSessionOnlyCookiesDoNotBecomePersistent(t *testing.T) {
	s:=&memorySecrets{}
	c:=mustClient(t,"https://example.com",s)
	if err:=c.receiveCookies([]*http.Cookie{{Name:"review_access",Value:"temporary",Path:"/"}});err!=nil {t.Fatal(err)}
	if c.cookies["review_access"].Value!="temporary" {t.Fatal("session cookie should work in this process")}
	if len(mustClient(t,c.Server(),s).cookies)!=0 {t.Fatal("session-only cookie survived restart")}
}

func TestSecretsFailureIsReportedAndPasswordNeverSaved(t *testing.T) {
	s:=&memorySecrets{}
	c:=mustClient(t,"https://example.com",s)
	s.fail=true
	err:=c.receiveCookies([]*http.Cookie{{Name:"review_access",Value:"sensitive-token",Path:"/",MaxAge:3600}})
	if err==nil {t.Fatal("credential failure hidden")}
	if strings.Contains(err.Error(),"sensitive-token") {t.Fatal("token leaked in error")}
	if _,err:=New(c.Server(),s);err==nil {t.Fatal("credential read failure treated as absence")}
}

func TestRedirectNotFollowedAndMutationNotRetried(t *testing.T) {
	var hits atomic.Int32
	target:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){hits.Add(1);fmt.Fprint(w,`{}`)}))
	defer target.Close()
	var attempts atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){attempts.Add(1);http.Redirect(w,r,target.URL,http.StatusTemporaryRedirect)}))
	defer server.Close()
	c:=mustClient(t,server.URL,&memorySecrets{})
	err:=c.Post(context.Background(),"/entries/e/decision",map[string]string{"status":"approved"},nil)
	var apiErr *APIError
	if !errors.As(err,&apiErr)||apiErr.Status!=307 {t.Fatalf("expected redirect error, got %v",err)}
	if hits.Load()!=0||attempts.Load()!=1 {t.Fatalf("followed or retried a mutation: hits=%d attempts=%d",hits.Load(),attempts.Load())}
}

func TestAPIErrorKeepsUsefulValidationMessage(t *testing.T) {
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(409);fmt.Fprint(w,`{"detail":"正文已有新版本，请保留草稿并重新比较"}`)}))
	defer server.Close()
	c:=mustClient(t,server.URL,&memorySecrets{})
	err:=c.Post(context.Background(),"/entries/e/revisions",map[string]string{"content":"draft"},nil)
	var apiErr *APIError
	if !errors.As(err,&apiErr)||apiErr.Status!=409||!strings.Contains(err.Error(),"保留草稿") {t.Fatalf("unexpected error: %v",err)}
}

func TestTruncatedMutationResponseWarnsBeforeManualRetry(t *testing.T) {
	var attempts atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		attempts.Add(1)
		w.Header().Set("Content-Length","100")
		fmt.Fprint(w,`{"id":"committed"}`)
	}))
	defer server.Close()
	c:=mustClient(t,server.URL,&memorySecrets{})
	err:=c.Post(context.Background(),"/entries/e/reviews",map[string]string{"body":"draft"},nil)
	if err==nil||!strings.Contains(err.Error(),"结果尚未确认") {t.Fatalf("ambiguous mutation was not explained: %v",err)}
	if attempts.Load()!=1 {t.Fatal("uncertain mutation automatically retried")}
}

func TestMultipartPreservesDirectoryNamesAndContentLength(t *testing.T) {
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.ContentLength<=0 {t.Error("missing Content-Length")}
		reader,err:=r.MultipartReader();if err!=nil {t.Error(err);return}
		fields:=map[string]string{}
		for {part,err:=reader.NextPart();if err==io.EOF {break};if err!=nil {t.Error(err);return};data,_:=io.ReadAll(part)
			if part.FormName()=="files" {
				if !strings.Contains(part.Header.Get("Content-Disposition"),`filename="knowledge/common/美香.md"`) {t.Errorf("relative filename lost: %s",part.Header.Get("Content-Disposition"))}
				if string(data)!="# 美香\r\n原文  \n" {t.Error("file content changed")}
			} else {fields[part.FormName()]=string(data)}
		}
		if fields["name"]!="测试"||fields["category"]!="common" {t.Errorf("wrong fields %v",fields)}
		fmt.Fprint(w,`{"id":"batch-1"}`)
	}))
	defer server.Close()
	path:=filepath.Join(t.TempDir(),"美香.md")
	os.WriteFile(path,[]byte("# 美香\r\n原文  \n"),0o600)
	c:=mustClient(t,server.URL,&memorySecrets{})
	var result map[string]any
	if err:=c.Upload(context.Background(),"/imports",[]UploadFile{{Path:path,Name:"knowledge/common/美香.md"}},map[string]string{"name":"测试","category":"common"},&result);err!=nil {t.Fatal(err)}
	if result["id"]!="batch-1" {t.Fatal(result)}
}

func TestUploadEnforcesActualByteLimitBeforeSending(t *testing.T) {
	var hits atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){hits.Add(1);fmt.Fprint(w,`{}`)}))
	defer server.Close()
	path:=filepath.Join(t.TempDir(),"large.zip")
	f,err:=os.Create(path);if err!=nil {t.Fatal(err)}
	if err:=f.Truncate((30<<20)+1);err!=nil {t.Fatal(err)}
	f.Close()
	c:=mustClient(t,server.URL,&memorySecrets{})
	if err:=c.Upload(context.Background(),"/imports",[]UploadFile{{Path:path}},nil,nil);err==nil {t.Fatal("oversized upload accepted")}
	if hits.Load()!=0 {t.Fatal("oversized upload sent to server")}
}

func TestDownloadPreservesExistingFileOnFailureAndReplacesOnSuccess(t *testing.T) {
	for _,mode:=range []string{"error","truncated","success"} {
		t.Run(mode,func(t *testing.T){
			server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
				switch mode {
				case "error":w.WriteHeader(403);fmt.Fprint(w,`{"detail":"仅限管理员"}`)
				case "truncated":w.Header().Set("Content-Length","100");fmt.Fprint(w,"partial")
				case "success":w.Write([]byte{80,75,0,255})
				}
			}))
			defer server.Close()
			dir:=t.TempDir();destination:=filepath.Join(dir,"existing.zip")
			os.WriteFile(destination,[]byte("original"),0o600)
			c:=mustClient(t,server.URL,&memorySecrets{})
			err:=c.Download(context.Background(),"/releases/r/download",destination)
			data,_:=os.ReadFile(destination)
			if mode=="success" {if err!=nil||string(data)!=string([]byte{80,75,0,255}) {t.Fatalf("success: %v %v",data,err)}} else if err==nil||string(data)!="original" {t.Fatalf("original overwritten: %q %v",data,err)}
			entries,_:=os.ReadDir(dir);if len(entries)!=1 {t.Fatal("temporary download leaked")}
		})
	}
}
