package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// The session cookie the access gate sets must come back on the next request,
// which is what the page asks for with `credentials: "include"`; the whole
// login flow depends on it.
func TestReviewClientKeepsSessionCookie(t *testing.T) {
	var gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/enter" {
			http.SetCookie(w, &http.Cookie{Name: "review_access", Value: "session-token", Path: "/", HttpOnly: true})
			w.WriteHeader(http.StatusOK)
			return
		}
		cookie, err := r.Cookie("review_access")
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotCookie = cookie.Value
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := newReviewClient()
	if err != nil {
		t.Fatalf("newReviewClient: %v", err)
	}
	post, err := client.Post(server.URL+"/api/auth/enter", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	post.Body.Close()

	get, err := client.Get(server.URL + "/api/entries")
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	get.Body.Close()
	if get.StatusCode != http.StatusOK {
		t.Fatalf("the session cookie was not sent: status %d", get.StatusCode)
	}
	if gotCookie != "session-token" {
		t.Fatalf("cookie = %q, want session-token", gotCookie)
	}
}

// A redirect must not be followed: the UI expects the 3xx itself, and a
// followed redirect would turn a rejected request into a 200.
func TestReviewClientRefusesRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/enter" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := newReviewClient()
	if err != nil {
		t.Fatalf("newReviewClient: %v", err)
	}
	response, err := client.Post(server.URL+"/api/auth/enter", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the redirect handed back (%d)", response.StatusCode, http.StatusFound)
	}
	if location := response.Header.Get("Location"); location != "/elsewhere" {
		t.Fatalf("Location = %q, want /elsewhere", location)
	}
}

// A page must not be able to make the app launch an arbitrary program: only
// http and https reach the system opener. The accepted branch needs a running
// app, so only the refusals are exercised here.
func TestOpenExternalRefusesNonWebSchemes(t *testing.T) {
	for _, raw := range []string{
		"file:///C:/Windows/System32/calc.exe",
		"javascript:alert(1)",
		"ms-settings:privacy",
		"cmd:/c calc",
	} {
		if err := (shellService{}).OpenExternal(raw); err == nil {
			t.Errorf("OpenExternal(%q) = nil, want a refusal", raw)
		}
	}
}

// The frontend must be served with the app's policy, and that policy must
// still let the app's own bundle and styles through.
func TestFrontendServesSecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>t</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := mygo.FileServer(os.DirFS(dir))
	recorder := httptest.NewRecorder()
	withSecurityHeaders(files).ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))

	if got := recorder.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, contentSecurityPolicy)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	// The policy must not block the app's own script and styles.
	for _, directive := range []string{"script-src 'self'", "style-src 'self'",
		"connect-src 'self'", "object-src 'none'", "base-uri 'self'"} {
		if !strings.Contains(contentSecurityPolicy, directive) {
			t.Errorf("policy lacks %q", directive)
		}
	}
	// The bridge is window.chrome.webview.postMessage, which is not a network
	// fetch, so the policy needs no ipc: origin.
	if strings.Contains(contentSecurityPolicy, "ipc:") {
		t.Error("policy allows an ipc: origin, but the bridge does not use one")
	}
}

// A development build carries no update feed, and the UI must say so rather
// than claim it checked. mygo.Updater reads the feed and key that `mygo build`
// stamps in, so a plain `go build` reports ErrUpdatesDisabled.
func TestCheckUpdateSaysUpdatesNeedAReleaseBuild(t *testing.T) {
	info, err := (shellService{}).CheckUpdate()
	if err == nil {
		t.Fatalf("CheckUpdate returned %+v with no error in a development build", info)
	}
	if !errors.Is(err, errUpdatesDisabled) {
		t.Fatalf("CheckUpdate error = %v, want errUpdatesDisabled", err)
	}
}

// TestManifestSignatureContract mirrors the signature check mygo's updater
// performs. It is a CONTRACT MIRROR, not a test of the app's verification path.
//
// What it does cover: the manifest format and the arithmetic of the check —
// SHA-256 of the bytes, the size the download read, then an Ed25519 signature
// over that digest. A forged, zeroed, truncated or size-mismatched manifest is
// refused by that arithmetic.
//
// What it does NOT cover, and must not be read as covering: whether the app
// actually verifies a download. That happens inside mygo's Update.Install
// (updater.go:288-310 for download+verify, then installUpdate -> swapFiles at
// :388-423), which this package cannot reach — the types are unexported. The
// real answer to "can a refused signature leave the installed executable half
// written?" is therefore a source-level one: the archive is verified in
// download() before the swap runs, so a refusal returns with the target
// untouched. There is no app-side test for it, and this one must not stand in
// for that.
//
// It builds its own key and archive so it runs everywhere, instead of depending
// on a signed build that .gitignore hides and CI never produces.
func TestManifestSignatureContractMirrorsMygo(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	archive := []byte("pretend this is finesub-review-0.3.1-windows-amd64.tar.gz")
	digest := sha256.Sum256(archive)
	good := ed25519.Sign(privateKey, digest[:])

	// The check mygo performs, in the same order, as a pure function of the
	// bytes a download produced. Pure on purpose: it makes the refusals below
	// unambiguous, and it is also why this test cannot speak to what Install
	// does to the file system.
	verify := func(onDisk []byte, size int64, signature []byte) error {
		if int64(len(onDisk)) != size {
			return errors.New("size mismatch")
		}
		sum := sha256.Sum256(onDisk)
		if !ed25519.Verify(publicKey, sum[:], signature) {
			return errors.New("the update is not signed with the app's key")
		}
		return nil
	}

	if err := verify(archive, int64(len(archive)), good); err != nil {
		t.Fatalf("the correctly signed archive was refused: %v", err)
	}

	forged := append([]byte(nil), good...)
	forged[0] ^= 0xFF
	zeroed := make([]byte, ed25519.SignatureSize)
	tampered := append([]byte(nil), archive...)
	tampered[len(tampered)/2] ^= 0xFF
	cases := []struct {
		name string
		body []byte
		size int64
		sig  []byte
	}{
		{"forged signature", archive, int64(len(archive)), forged},
		{"zeroed signature", archive, int64(len(archive)), zeroed},
		{"truncated signature", archive, int64(len(archive)), good[:32]},
		{"wrong archive size", archive, int64(len(archive)) + 1, good},
		// The substituted-download case: intact signature, altered payload.
		{"tampered archive", tampered, int64(len(tampered)), good},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := verify(c.body, c.size, c.sig); err == nil {
				t.Fatalf("%s was accepted", c.name)
			}
		})
	}
}

// A refused install must reach the page as an error and must not leave the app
// thinking the update is ready. This is the regression that matters most: a
// swallowed error would tell the user to restart into a version that never
// verified.
func TestDownloadUpdatePropagatesInstallFailure(t *testing.T) {
	resetUpdateState(t)

	refused := errors.New("mygo: installing the update: the update is not signed with the app's key")
	pending = &pendingUpdate{version: "0.3.1", install: func(context.Context) error { return refused }}

	err := (shellService{}).DownloadUpdate("0.3.1")
	if !errors.Is(err, refused) {
		t.Fatalf("DownloadUpdate error = %v, want the install refusal", err)
	}
	if installed {
		t.Fatal("a refused install was recorded as installed")
	}
	if pending != nil {
		t.Fatal("a refused install left the update pending")
	}
	// ApplyUpdate must not offer to restart into something that was refused.
	if err := (shellService{}).ApplyUpdate(); err == nil {
		t.Fatal("ApplyUpdate succeeded after a refused install")
	}
}

// A successful install is recorded, and only for the version that was checked.
func TestDownloadUpdateOnlyInstallsTheCheckedVersion(t *testing.T) {
	resetUpdateState(t)

	calls := 0
	pending = &pendingUpdate{version: "0.3.1", install: func(context.Context) error {
		calls++
		return nil
	}}

	if err := (shellService{}).DownloadUpdate("0.3.1"); err != nil {
		t.Fatalf("installing the checked version failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("install ran %d times, want 1", calls)
	}
	if !installed {
		t.Fatal("a successful install was not recorded")
	}
}

// A version that came from a cached startup check has no update object to
// install, so DownloadUpdate asks again. When the feed no longer offers that
// version, it must refuse rather than install something unexpected.
func TestDownloadUpdateAsksAgainWhenNothingIsPending(t *testing.T) {
	resetUpdateState(t)
	useFakeChecker(t, func(context.Context) (*mygo.Update, error) {
		return nil, nil // the feed reports nothing newer now
	})

	installs := 0
	// A cached answer left the page showing 0.3.1, but pending is empty.
	if err := (shellService{}).DownloadUpdate("0.3.1"); err == nil {
		t.Fatal("installing a version the feed no longer offers succeeded")
	}
	if installs != 0 {
		t.Fatalf("install ran %d times, want 0", installs)
	}
}

// A check that fails must not leave the app thinking an update is pending.
func TestCheckUpdateFailureClearsPending(t *testing.T) {
	resetUpdateState(t)
	useFakeChecker(t, func(context.Context) (*mygo.Update, error) {
		return nil, errors.New(`mygo: checking for updates: GET https://api.github.com/x: 403 Forbidden`)
	})

	info, err := (shellService{}).CheckUpdate()
	if err == nil {
		t.Fatalf("CheckUpdate returned %+v with no error", info)
	}
	if pending != nil {
		t.Fatal("a failed check left an update pending")
	}
	if installed {
		t.Fatal("a failed check left the app ready to restart")
	}
}

// The three failures a user can meet need different advice, so they must not
// collapse into one message. The samples are the shapes mygo's updater.go
// produces for each; anything unrecognized keeps its original text.
func TestDescribeUpdateError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "GitHub rate limit",
			err:      errors.New(`mygo: checking for updates: GET https://api.github.com/repos/x/y/releases?per_page=100&page=1: 403 Forbidden`),
			contains: "每小时限 60 次",
		},
		{
			name:     "no release published",
			err:      errors.New(`mygo: checking for updates: no release is published in x/y tagged desktop-v`),
			contains: "还没有发布新版本",
		},
		{
			name:     "manifest missing (404)",
			err:      errors.New(`mygo: checking for updates: GET https://github.com/x/y/releases/download/desktop-v9/update-windows-amd64.json: 404 Not Found`),
			contains: "还没有发布新版本",
		},
		{
			name:     "DNS failure",
			err:      errors.New(`mygo: checking for updates: Get "https://api.github.com/": dial tcp: lookup api.github.com: no such host`),
			contains: "无法连接更新源",
		},
		{
			name:     "connection refused through a proxy",
			err:      errors.New(`mygo: checking for updates: proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused`),
			contains: "无法连接更新源",
		},
		{
			name:     "timeout",
			err:      errors.New(`mygo: checking for updates: Get "https://api.github.com/": context deadline exceeded`),
			contains: "无法连接更新源",
		},
		{
			name:     "unrecognized keeps its text",
			err:      errors.New(`mygo: checking for updates: incomplete manifest`),
			contains: "检查更新失败",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := describeUpdateError(c.err)
			if got == nil {
				t.Fatal("describeUpdateError returned nil")
			}
			if !strings.Contains(got.Error(), c.contains) {
				t.Fatalf("message = %q, want it to contain %q", got.Error(), c.contains)
			}
			// Unrecognized errors must still carry the original text, so a
			// support report can identify what happened.
			if c.name == "unrecognized keeps its text" && !strings.Contains(got.Error(), "incomplete manifest") {
				t.Fatalf("the original message was dropped: %q", got.Error())
			}
		})
	}

	// The disabled-build message must survive unchanged.
	if got := describeUpdateError(mygo.ErrUpdatesDisabled); !errors.Is(got, errUpdatesDisabled) {
		t.Fatalf("a disabled build reported %v, want errUpdatesDisabled", got)
	}
	if got := describeUpdateError(nil); got != nil {
		t.Fatalf("describeUpdateError(nil) = %v, want nil", got)
	}
}

// The startup check reuses an answer younger than the interval, and checks
// again once it is older: this is what keeps a restart from spending GitHub's
// hourly quota.
func TestUpdateCacheReuse(t *testing.T) {
	resetUpdateState(t)
	cacheDir := t.TempDir()
	updateCacheDir = cacheDir
	t.Cleanup(func() { updateCacheDir = "" })

	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	writeUpdateCache(UpdateInfo{Version: "0.5.0", Notes: "notes"}, nil, now)

	cache, fresh := readUpdateCache(now.Add(1 * time.Hour))
	if !fresh {
		t.Fatal("an hour-old answer was not reused")
	}
	if cache.Version != "0.5.0" || cache.Notes != "notes" {
		t.Fatalf("cache = %+v, want version 0.5.0 and its notes", cache)
	}

	if _, fresh := readUpdateCache(now.Add(25 * time.Hour)); fresh {
		t.Fatal("a 25-hour-old answer was reused")
	}
	// A clock that moved backwards must not pin a stale answer.
	if _, fresh := readUpdateCache(now.Add(-48 * time.Hour)); fresh {
		t.Fatal("an answer from the future was reused")
	}

	// A corrupt or missing cache means "check now", never "hide the update".
	writeFileForTest(t, filepath.Join(cacheDir, "update-check.json"), "{not json")
	if _, fresh := readUpdateCache(now); fresh {
		t.Fatal("a corrupt cache was treated as fresh")
	}
	updateCacheDir = filepath.Join(cacheDir, "absent")
	if _, fresh := readUpdateCache(now); fresh {
		t.Fatal("a missing cache was treated as fresh")
	}
}

// A rate-limited check is remembered for the rest of the hour: the quota is
// already spent, so asking again at the next launch only wastes what is gone.
// A plain network failure is remembered for a shorter time, because the cause
// may pass.
func TestFailedCheckIsCachedWithABackoff(t *testing.T) {
	resetUpdateState(t)
	updateCacheDir = t.TempDir()
	t.Cleanup(func() { updateCacheDir = "" })

	cases := []struct {
		name    string
		err     error
		expired time.Duration // must be expired after this long
		alive   time.Duration // must still be valid after this long
	}{
		{
			name:    "rate limit keeps the hour",
			err:     errors.New("mygo: checking for updates: GET https://api.github.com/x: 403 Forbidden"),
			alive:   30 * time.Minute,
			expired: 61 * time.Minute,
		},
		{
			name:    "network failure is shorter",
			err:     errors.New("mygo: checking for updates: dial tcp: lookup api.github.com: no such host"),
			alive:   5 * time.Minute,
			expired: 20 * time.Minute,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := time.Now()
			described := describeUpdateError(c.err)
			writeUpdateCache(UpdateInfo{}, described, now)

			cache, ok := readUpdateCache(now.Add(c.alive))
			if !ok {
				t.Fatalf("the failure was not remembered after %s", c.alive)
			}
			if cache.Error == "" {
				t.Fatal("the cached failure carries no message")
			}
			if _, ok := readUpdateCache(now.Add(c.expired)); ok {
				t.Fatalf("the failure was still remembered after %s", c.expired)
			}
		})
	}
}

// A rate-limited launch must not ask the server again until the backoff ends,
// and must still show the same explanation. The user's own check always asks.
func TestRateLimitedStartupDoesNotAskAgain(t *testing.T) {
	resetUpdateState(t)
	updateCacheDir = t.TempDir()
	t.Cleanup(func() { updateCacheDir = "" })

	rateLimited := errors.New("mygo: checking for updates: GET https://api.github.com/x: 403 Forbidden")
	calls := 0
	useFakeChecker(t, func(context.Context) (*mygo.Update, error) {
		calls++
		return nil, rateLimited
	})

	// The first launch hits the network and remembers the failure.
	if _, err := (shellService{}).CheckUpdateStartup(); err == nil {
		t.Fatal("a rate-limited check reported success")
	}
	if calls != 1 {
		t.Fatalf("the first launch made %d calls, want 1", calls)
	}

	// The next launch reuses it instead of spending more quota.
	_, err := (shellService{}).CheckUpdateStartup()
	if err == nil {
		t.Fatal("the cached rate-limit failure was reported as success")
	}
	if !strings.Contains(err.Error(), "每小时限 60 次") {
		t.Fatalf("the cached message lost its explanation: %q", err)
	}
	if calls != 1 {
		t.Fatalf("a second launch made %d calls, want it to reuse the cache", calls)
	}

	// "重新检查" is the escape hatch and must always ask.
	if _, err := (shellService{}).CheckUpdate(); err == nil {
		t.Fatal("a manual check reported success while rate limited")
	}
	if calls != 2 {
		t.Fatalf("a manual check made %d calls in total, want 2", calls)
	}
}

// The startup check must consult the cache, and a cached hit must not call the
// network at all.
func TestCheckUpdateStartupUsesTheCache(t *testing.T) {
	resetUpdateState(t)
	updateCacheDir = t.TempDir()
	t.Cleanup(func() { updateCacheDir = "" })

	now := time.Now()
	writeUpdateCache(UpdateInfo{Version: "0.5.0", Notes: "remembered"}, nil, now)

	networkCalls := 0
	useFakeChecker(t, func(context.Context) (*mygo.Update, error) {
		networkCalls++
		return nil, nil
	})

	info, err := (shellService{}).CheckUpdateStartup()
	if err != nil {
		t.Fatalf("CheckUpdateStartup: %v", err)
	}
	if networkCalls != 0 {
		t.Fatalf("the cached answer still made %d network calls", networkCalls)
	}
	if info.Version != "0.5.0" || info.Notes != "remembered" {
		t.Fatalf("info = %+v, want the cached version and notes", info)
	}

	// The user's own "重新检查" ignores the cache.
	if _, err := (shellService{}).CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if networkCalls != 1 {
		t.Fatalf("a manual check made %d network calls, want 1", networkCalls)
	}
}

// An install failure must say the app is unchanged, and the message must differ
// by cause: a bad signature, a truncated download and a read-only directory need
// different actions from the user.
func TestDescribeInstallError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		contains []string
	}{
		{
			name:     "bad signature",
			err:      errors.New("mygo: installing the update: the update is not signed with the app's key"),
			contains: []string{"签名校验未通过", "未安装任何内容"},
		},
		{
			name:     "truncated download",
			err:      errors.New("mygo: installing the update: downloading: downloaded 12346 bytes, want 12345"),
			contains: []string{"大小与清单不符", "当前版本未改变"},
		},
		{
			name:     "network dropped mid-download",
			err:      errors.New("mygo: installing the update: downloading: Get \"https://x\": dial tcp: i/o timeout"),
			contains: []string{"连接中断", "请检查网络"},
		},
		{
			name:     "read-only install directory",
			err:      errors.New("mygo: installing the update: cannot write next to the app: access is denied"),
			contains: []string{"无法写入程序所在目录"},
		},
		{
			name:     "unrecognized still says the app is unchanged",
			err:      errors.New("mygo: installing the update: the update holds no app bundle"),
			contains: []string{"当前版本未改变"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := describeInstallError(c.err)
			if got == nil {
				t.Fatal("describeInstallError returned nil")
			}
			for _, want := range c.contains {
				if !strings.Contains(got.Error(), want) {
					t.Fatalf("message = %q, want it to contain %q", got.Error(), want)
				}
			}
			// The cause stays in the chain, so a support report can identify it.
			if !errors.Is(got, c.err) {
				t.Fatalf("the original error was dropped from the chain: %v", got)
			}
		})
	}
	if got := describeInstallError(nil); got != nil {
		t.Fatalf("describeInstallError(nil) = %v, want nil", got)
	}
}

// A failed install must reach the page with the install wording, not the
// check wording: telling the user "检查更新失败" after a download is wrong.
func TestRefusedInstallIsNotReportedAsACheckFailure(t *testing.T) {
	resetUpdateState(t)
	refused := errors.New("mygo: installing the update: the update is not signed with the app's key")
	pending = &pendingUpdate{version: "0.4.1", install: func(context.Context) error { return refused }}

	err := (shellService{}).DownloadUpdate("0.4.1")
	if err == nil {
		t.Fatal("a refused install reported success")
	}
	if strings.Contains(err.Error(), "检查更新失败") {
		t.Fatalf("install failure used the check wording: %q", err)
	}
	if !strings.Contains(err.Error(), "未安装任何内容") {
		t.Fatalf("install failure did not say the app is unchanged: %q", err)
	}
}

// DownloadUpdate without a check first must not panic or install anything.
func TestDownloadUpdateWithoutACheckFails(t *testing.T) {
	resetUpdateState(t)
	useFakeChecker(t, func(context.Context) (*mygo.Update, error) {
		return nil, mygo.ErrUpdatesDisabled
	})
	if err := (shellService{}).DownloadUpdate("0.3.1"); err == nil {
		t.Fatal("DownloadUpdate succeeded with no pending update")
	}
}

// useFakeChecker replaces the update source for one test.
func useFakeChecker(t *testing.T, fn func(context.Context) (*mygo.Update, error)) {
	t.Helper()
	original := updateChecker
	updateChecker = fn
	t.Cleanup(func() { updateChecker = original })
}

// writeFileForTest writes a file, failing the test rather than the process.
func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resetUpdateState clears the package-level update state between tests, which
// the shell keeps for the one update a session installs.
func resetUpdateState(t *testing.T) {
	t.Helper()
	updateMu.Lock()
	pending = nil
	installed = false
	updateMu.Unlock()
	t.Cleanup(func() {
		updateMu.Lock()
		pending = nil
		installed = false
		updateMu.Unlock()
	})
}

func TestAllowedNavigation(t *testing.T) {
	for _, url := range []string{
		"mygo://localhost/",
		"http://mygo.localhost/",
		"http://localhost:5173/",
		"http://127.0.0.1:5173/#/review",
		"about:blank",
	} {
		if !allowedNavigation(url) {
			t.Errorf("allowedNavigation(%q) = false, want true", url)
		}
	}
	for _, url := range []string{
		"https://example.com/",
		"http://example.com/phish",
		"file:///C:/Windows/System32/calc.exe",
		"javascript:alert(1)",
	} {
		if allowedNavigation(url) {
			t.Errorf("allowedNavigation(%q) = true, want false", url)
		}
	}
}
