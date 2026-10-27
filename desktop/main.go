// Command desktop is the desktop shell of the FineSub review app.
//
// It replaces the Tauri shell: the same frontend build is served at
// mygo://localhost/, and every HTTP request to the review server leaves from
// Go, so the app keeps a shared cookie jar, is not subject to CORS, and can
// read every response header.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/fetch"
)

const (
	appName = "FineSub Review"
	// version is what the page shows and what an update compares against. It
	// must match mygo.json's "version": `mygo build` stamps its own copy into
	// the binary for the updater, and this one is what the UI reads. Keep the
	// CHANGELOG section in step with it too — `mygo build` takes the release
	// notes from there, and package_desktop.mjs fails when it is missing.
	version = "0.4.0"
)

//go:embed all:ui
var uiFiles embed.FS

// embeddedFrontend returns the frontend copied into ui/ by the build, or nil
// when nothing was copied.
//
// ui/ is empty in a checkout (frontend/dist is the source there), and `go:embed`
// fails to compile when its pattern matches nothing, so ui/.gitkeep keeps a file
// in it and the index.html check below decides whether a build is embedded.
func embeddedFrontend() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// serveFrontend serves the app's pages at mygo://localhost/ with the CSP.
//
// This has to serve them itself rather than leave it to mygo.SetFrontend: a
// registered mygo:// handler takes precedence over the frontend handler, and
// it is the only way to attach the policy: MyGo has no CSP setting of its own.
// Serving them is therefore also why the frontend is embedded here, in ui/,
// instead of relying on `mygo build`'s embedding: a portable copy has no files
// beside it, and without a source of pages the handler would answer 404.
func serveFrontend(source fs.FS) {
	files := mygo.FileServer(source)
	handler := withSecurityHeaders(files)
	if err := mygo.Protocol.Handle("mygo", handler); err != nil {
		log.Fatalf("serving the frontend: %v", err)
	}
	// Keep the window's plain path loads ("/") pointing at the same files.
	mygo.SetFrontend(source)
}

func main() {
	distFlag := flag.String("dist", "", "directory of the built frontend (defaults to frontend/dist beside the executable or the repository)")
	flag.Parse()

	mygo.App.SetName(appName)

	client, err := newReviewClient()
	if err != nil {
		log.Fatalf("cookie jar: %v", err)
	}
	mygo.Use(fetch.New(fetch.Options{Client: client}))

	// Links that leave the app open in the system browser, as the opener
	// plugin did: a navigation inside the webview cannot reach the internet
	// page, and the whitelist above refuses it.
	mygo.BindAs("shell", &shellService{})

	// A built app carries its pages; a checkout reads them from frontend/dist
	// so a rebuild of the frontend shows up without recompiling Go.
	if dist := frontendDir(*distFlag); dist != "" {
		log.Printf("serving frontend from %s", dist)
		serveFrontend(os.DirFS(dist))
	} else if embedded := embeddedFrontend(); embedded != nil {
		log.Print("serving the embedded frontend")
		serveFrontend(embedded)
	} else {
		log.Fatal("找不到前端：请先运行 pnpm build（或用 -dist 指定目录），或用 mygo build 构建内嵌版本")
	}

	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:     "FineSub 知识库审核",
			URL:       "/",
			Width:     1280,
			Height:    860,
			MinWidth:  900,
			MinHeight: 640,
		})
		// The page is an app, not a browser: a navigation away from the app's
		// own pages is refused, as Tauri's on_navigation whitelist did. Links
		// to the outside are opened by the system browser from main.ts, which
		// cancels the click before it becomes a navigation.
		window.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			if !allowedNavigation(e.URL) {
				e.PreventDefault()
			}
		})
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// contentSecurityPolicy is the policy every page of the app is served with.
//
// It is the Tauri configuration's policy, adapted to this shell:
//   - the Tauri bridge was reached over the `ipc:` scheme and
//     `http://ipc.localhost`, while MyGo's bridge is
//     `window.chrome.webview.postMessage`, which is not a network fetch and so
//     needs no connect-src entry;
//   - requests to the review server leave from Go (the fetch plugin) and never
//     from the page, so connect-src stays 'self' as it was;
//   - style-src no longer allows 'unsafe-inline': the production build links its
//     stylesheet, no component sets an inline style, and Vue's v-show and
//     transitions go through the CSSOM, which the policy does not govern;
//   - frame-ancestors 'none' is added to match the server deployment, which
//     refuses to be framed at all.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; " +
	"frame-ancestors 'none'"

// withSecurityHeaders adds the policy and the framing headers to a file server.
func withSecurityHeaders(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Framing and sniffing, as the server deployment sets them.
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		files.ServeHTTP(w, r)
	})
}

// newReviewClient is the HTTP client every request to the review server uses.
//
// It must keep the four properties the Tauri HTTP plugin gave the frontend:
//
//   - a cookie jar: `credentials: "include"` only keeps a session because Go
//     sends the cookie of the previous response. http.DefaultClient has no
//     jar, so this is not optional.
//   - no redirects: following one would carry the session cookie to another
//     location and return a 200 for a request the UI expects to reject.
//   - readable response headers: the page reads content-disposition.
//   - a timeout, so a stalled server cannot hold a request forever.
func newReviewClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		// The frontend also asks for redirect: "manual" per request; this
		// refuses them even if a call forgets to.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// shellService is bound to pages as "shell".
type shellService struct{}

// OpenExternal opens an http or https URL with the system browser. A scheme
// a page may not choose on its own, such as file:, is refused.
func (shellService) OpenExternal(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("不支持的链接协议：%s", u.Scheme)
	}
	return mygo.Shell.OpenExternal(raw)
}

// Version returns the app version, which the frontend used to read from the
// Tauri app metadata.
func (shellService) Version() string { return version }

// SaveDownload asks where to save a file and writes bytes there, replacing
// the dialog and fs plugins: the bytes arrive from the page as the response
// body it already read, so no path of the app is exposed to the page.
func (shellService) SaveDownload(name string, bytes []byte) (string, error) {
	if name == "" {
		name = "download.zip"
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{DefaultPath: name, Title: "保存文件"})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// errUpdatesDisabled explains why no update is offered in this build.
//
// mygo.Updater reads the update feed and the public key that `mygo build`
// stamps into the executable from mygo.json's "updates"; a plain `go build`
// carries neither, so Updater.Check reports ErrUpdatesDisabled. That is not a
// failure to hide: the UI says which build it is instead of claiming an update
// was checked.
var errUpdatesDisabled = errors.New("当前为开发构建，应用内更新只在发布版可用；请到发布页下载新版")

// pendingUpdate is the update CheckUpdate found, kept for DownloadUpdate.
//
// It wraps mygo.Update rather than holding it directly: mygo's Update has no
// way to be built in a test, and the signature verification lives inside
// Install. Wrapping keeps the version (a plain string) and the install call
// (a func) separable, so a test can drive the failure path where a forged or
// tampered download is refused, which is the one that must never be reported
// as success.
type pendingUpdate struct {
	version string
	install func(ctx context.Context) error
}

// updateMu serializes the update calls: a check and an install share the state
// below, and two downloads at once would fight over the same file.
var updateMu sync.Mutex

// pending is the update found by CheckUpdate, kept for DownloadUpdate. It is
// nil when the app is up to date, and replaced on every check.
var pending *pendingUpdate

// installed records that DownloadUpdate finished: the files on disk are the new
// version, so ApplyUpdate only has to restart into them.
var installed bool

// UpdateInfo describes an available update, as the page shows it.
type UpdateInfo struct {
	Version  string `json:"version"`
	Notes    string `json:"notes"`
	Prepared bool   `json:"prepared"`
}

// startupCheckInterval is how long a successful check is reused at launch.
//
// GitHub's API allows 60 unauthenticated requests an hour per address, and one
// check costs two (the release list, then the manifest). The app checks once at
// every launch, so a user who restarts often — or several users behind one
// address — would spend the quota on launches. Reusing the last answer for a day
// keeps that to one check a day per machine; the user's own "重新检查" always
// asks the server, and so does installing from a cached answer.
var startupCheckInterval = 24 * time.Hour

// Backoffs for a failed check. They matter more than the success interval: once
// GitHub has answered 403, the quota is spent for the rest of the hour, so a
// launch that checks again only burns what is already gone. A plain network
// failure gets a shorter pause — the cause may be gone in a minute, and the
// user can always retry by hand.
var (
	rateLimitBackoff        = time.Hour
	transientFailureBackoff = 15 * time.Minute
)

// updateCheckCache is the last check, kept in the user data directory.
//
// A failure is cached as well as a success. Caching only successes would leave
// exactly the case that motivated the cache — a rate-limited launch — asking
// again at every start.
type updateCheckCache struct {
	CheckedAt string `json:"checkedAt"`
	ExpiresAt string `json:"expiresAt"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	// Error holds the already-localized message of a failed check.
	Error string `json:"error,omitempty"`
}

// updateChecker is mygo.Updater.Check. It is a variable so a test can drive the
// paths around it: mygo.Update has unexported fields and cannot be built here.
var updateChecker = func(ctx context.Context) (*mygo.Update, error) {
	return mygo.Updater.Check(ctx)
}

// updateCacheDir overrides where the last check is remembered. Empty means the
// app's user data directory; tests point it at a temporary one.
var updateCacheDir string

// updateCachePath is where the last check is remembered.
func updateCachePath() (string, error) {
	if updateCacheDir != "" {
		return filepath.Join(updateCacheDir, "update-check.json"), nil
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "update-check.json"), nil
}

// readUpdateCache returns the last check when it is still valid. A missing,
// unreadable or expired cache means "check now": the file only saves quota, it
// must never be a reason to hide an update for longer than it says.
func readUpdateCache(now time.Time) (updateCheckCache, bool) {
	path, err := updateCachePath()
	if err != nil {
		return updateCheckCache{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCheckCache{}, false
	}
	var cache updateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return updateCheckCache{}, false
	}
	checked, err := time.Parse(time.RFC3339, cache.CheckedAt)
	if err != nil {
		return updateCheckCache{}, false
	}
	expires, err := time.Parse(time.RFC3339, cache.ExpiresAt)
	if err != nil {
		return updateCheckCache{}, false
	}
	// A clock that moved backwards must not pin an old answer: treat a check
	// dated in the future as expired.
	if now.Before(checked) || !now.Before(expires) {
		return updateCheckCache{}, false
	}
	return cache, true
}

// writeUpdateCache records a check, successful or not.
func writeUpdateCache(info UpdateInfo, checkErr error, now time.Time) {
	path, err := updateCachePath()
	if err != nil {
		return
	}
	lifetime := startupCheckInterval
	entry := updateCheckCache{
		CheckedAt: now.UTC().Format(time.RFC3339),
		Version:   info.Version,
		Notes:     info.Notes,
	}
	if checkErr != nil {
		entry.Error = checkErr.Error()
		lifetime = transientFailureBackoff
		if matchesAny(strings.ToLower(checkErr.Error()), rateLimitMarkers) {
			lifetime = rateLimitBackoff
		}
	}
	entry.ExpiresAt = now.Add(lifetime).UTC().Format(time.RFC3339)
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	// Best effort: failing to remember a check costs quota, not correctness.
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// describeUpdateError turns mygo's error into something a user can act on.
//
// The three failures need different actions: a network problem is theirs to fix,
// a 403 means waiting out GitHub's hourly limit, and "no release published" means
// there is nothing to install yet. mygo wraps these without exported sentinels
// (only ErrUpdatesDisabled and ErrNotPublished exist, and ErrNotPublished lives in
// an internal package), so the classification reads the error text. That is
// fragile against a mygo upgrade — the wording is matched here, and
// TestDescribeUpdateError pins the samples taken from updater.go — so anything
// unrecognized keeps the original message instead of being mislabelled.
func describeUpdateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, mygo.ErrUpdatesDisabled) {
		return errUpdatesDisabled
	}
	message := err.Error()
	lower := strings.ToLower(message)
	switch {
	case matchesAny(lower, rateLimitMarkers):
		return fmt.Errorf("更新源暂时拒绝访问：GitHub 接口对同一地址每小时限 60 次，本次请求已用尽。请约一小时后重试；当前版本可正常使用: %w", err)
	case matchesAny(lower, notPublishedMarkers):
		return fmt.Errorf("更新源还没有发布新版本，暂无可安装的更新；当前版本可正常使用: %w", err)
	case matchesAny(lower, networkFailureMarkers):
		return fmt.Errorf("无法连接更新源，请检查网络后重试；如使用代理或企业网络，可能无法访问 api.github.com: %w", err)
	}
	return fmt.Errorf("检查更新失败：%s: %w", message, err)
}

// describeInstallError is describeUpdateError for the download-and-install step,
// where the consequence is different: the app is unchanged, and the user needs
// to know that rather than being told the check failed.
func describeInstallError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not signed with the app's key"),
		strings.Contains(lower, "signature"):
		return fmt.Errorf("更新包签名校验未通过，未安装任何内容，当前版本未改变。请重试；若反复失败请到发布页手动下载: %w", err)
	case strings.Contains(lower, "downloaded ") && strings.Contains(lower, "want "),
		strings.Contains(lower, "invalid size"):
		return fmt.Errorf("更新包大小与清单不符，未安装任何内容，当前版本未改变。请重试: %w", err)
	case matchesAny(lower, networkFailureMarkers):
		return fmt.Errorf("下载更新时连接中断，未安装任何内容，当前版本未改变。请检查网络后重试: %w", err)
	case strings.Contains(lower, "cannot write next to the app"):
		return fmt.Errorf("无法写入程序所在目录（可能没有权限，或程序位于受保护位置）。请把程序移到用户可写目录后重试: %w", err)
	}
	return fmt.Errorf("安装更新失败，当前版本未改变：%s: %w", message, err)
}

// matchesAny reports whether s contains one of the markers.
func matchesAny(s string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// rateLimitMarkers identify GitHub's hourly limit rather than a bad request.
var rateLimitMarkers = []string{"403", "429", "rate limit", "api rate"}

// notPublishedMarkers identify "there is nothing to install yet".
var notPublishedMarkers = []string{"no release is published", "404"}

// networkFailureMarkers are the fragments Go's HTTP stack puts in transport
// errors, as opposed to an answer from the server.
var networkFailureMarkers = []string{
	"dial tcp", "no such host", "connection refused", "connection reset",
	"network is unreachable", "i/o timeout", "deadline exceeded",
	"tls handshake", "proxyconnect", "unexpected eof", "server misbehaving",
}

// CheckUpdate asks the update feed for a newer version, ignoring any cache.
//
// It returns a zero UpdateInfo with no error when the app is up to date, which
// the page distinguishes from an error: "已是最新版本" is not a failure.
func (shellService) CheckUpdate() (UpdateInfo, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	return checkLocked(time.Now())
}

// CheckUpdateStartup is CheckUpdate for the check the app runs at launch: it
// reuses the last answer while that answer is still valid.
//
// A cached answer for a newer version cannot install on its own, because the
// update object mygo returns is not serializable; DownloadUpdate therefore checks
// again before installing when nothing is pending. A cached failure is returned
// as the same message, so a rate-limited launch does not ask the server again
// until the hour is up — and the user still has "重新检查" to ask now.
func (shellService) CheckUpdateStartup() (UpdateInfo, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	now := time.Now()
	if cache, ok := readUpdateCache(now); ok {
		if cache.Error != "" {
			return UpdateInfo{}, errors.New(cache.Error)
		}
		if cache.Version != "" {
			// The page will show this; installing asks the server again.
			return UpdateInfo{Version: cache.Version, Notes: cache.Notes, Prepared: false}, nil
		}
		return UpdateInfo{}, nil
	}
	return checkLocked(now)
}

// checkLocked performs a check and records it. Callers hold updateMu.
func checkLocked(now time.Time) (UpdateInfo, error) {
	// Ten seconds: a check that hangs must not hold the UI.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found, err := updateChecker(ctx)
	if err != nil {
		described := describeUpdateError(err)
		// A development build has no feed at all; remembering that would only
		// hide the message behind a stale file, so it is not cached.
		if !errors.Is(described, errUpdatesDisabled) {
			writeUpdateCache(UpdateInfo{}, described, now)
		}
		return UpdateInfo{}, described
	}
	installed = false
	if found == nil {
		pending = nil
		writeUpdateCache(UpdateInfo{}, nil, now)
		return UpdateInfo{}, nil
	}
	// Install is called later by DownloadUpdate; the closure keeps the update
	// mygo returned without this package depending on its unexported state.
	up := found
	pending = &pendingUpdate{
		version: up.Version,
		install: func(ctx context.Context) error { return up.Install(ctx, nil) },
	}
	info := UpdateInfo{Version: up.Version, Notes: up.Notes, Prepared: false}
	writeUpdateCache(info, nil, now)
	return info, nil
}

// DownloadUpdate downloads the update CheckUpdate found, verifies its
// signature, and replaces the files on disk.
//
// The replacement happens here, not in ApplyUpdate: on Windows mygo's Install
// unpacks the archive and renames it over the installed executable right away
// (only macOS defers the swap to a helper at exit). So closing the dialog after
// this point does not cancel anything — the new version runs at the next
// launch. That is why the UI says "下载并安装" rather than "下载": offering a
// cancel that cancels nothing is worse than saying what happens. A genuinely
// cancellable two-step would mean not using mygo's Install and staging the swap
// ourselves; that is a separate change, together with the leftover .old-<pid>
// files mygo can leave on Windows.
func (shellService) DownloadUpdate(version string) error {
	updateMu.Lock()
	defer updateMu.Unlock()
	if pending == nil || pending.version != version {
		// The version can come from a cached startup check, whose update object
		// mygo does not let us keep across launches. Ask again before installing:
		// never install on the strength of a remembered answer, and give a clear
		// message if the newer version has gone away since.
		info, err := checkLocked(time.Now())
		if err != nil {
			return err
		}
		if pending == nil || pending.version != version {
			if info.Version == "" {
				return fmt.Errorf("%s 已不可用：更新源当前没有更新的版本，可能已撤回，请重新检查", version)
			}
			return fmt.Errorf("更新源提供的是 %s，不是 %s，请重新检查", info.Version, version)
		}
	}
	// Downloads of a desktop build are tens of megabytes; twenty minutes is
	// generous for a slow link while still bounded.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	// Install verifies the signature before touching the installed files: a
	// forged, tampered or wrong-sized download returns here with the files
	// untouched. The error must reach the page — a silent failure would tell the
	// user the update is ready when nothing was installed.
	if err := pending.install(ctx); err != nil {
		pending = nil
		installed = false
		return describeInstallError(err)
	}
	installed = true
	return nil
}

// ApplyUpdate restarts the app into the version DownloadUpdate installed.
//
// mygo.App.Relaunch starts the executable now on disk and does not return.
func (shellService) ApplyUpdate() error {
	updateMu.Lock()
	ready := installed
	updateMu.Unlock()
	if !ready {
		return errors.New("没有已下载的更新，请先下载")
	}
	mygo.App.Relaunch()
	return nil
}

// SaveUpdateCopy keeps the downloaded version where the user chooses.
//
// mygo downloads the update into its own staging directory and swaps it in
// place, so there is no separate archive to hand over. The user wants "save a
// copy and keep using this version", which the release page provides; saying so
// beats returning a path that does not exist.
func (shellService) SaveUpdateCopy() (string, error) {
	return "", errors.New("请到发布页下载新版保存，应用内更新会直接替换当前版本")
}

// allowedNavigation reports whether the app may navigate to raw.
//
// mygo://localhost/ is the app's own frontend; WebView2 serves custom schemes
// at http://mygo.localhost/, which is the origin the page actually has, and
// the development server is an http URL on the loopback interface.
func allowedNavigation(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "mygo":
		return true
	case "http", "https":
		host := strings.ToLower(u.Hostname())
		if host == "mygo.localhost" || strings.HasSuffix(host, ".mygo.localhost") {
			return true
		}
		return host == "127.0.0.1" || host == "localhost"
	default:
		// about:blank while the webview starts, and blob:/data: for downloads
		// the page builds itself.
		return u.Scheme == "about" || u.Scheme == "blob" || u.Scheme == "data"
	}
}

// frontendDir returns the directory holding index.html.
//
// Without the mygo CLI the files are not embedded, so the build is looked up
// beside the executable first (a portable copy next to the CSS and JS it
// ships), then in the repository layout used during development.
func frontendDir(override string) string {
	candidates := []string{override}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "frontend", "dist"), filepath.Join(dir, "dist"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "..", "frontend", "dist"),
			filepath.Join(wd, "frontend", "dist"))
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
			return dir
		}
	}
	return ""
}
