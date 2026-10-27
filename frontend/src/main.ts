import { createApp } from "vue";
import "./style.css";
import "./workflow.css";
import { call } from "mygo-runtime";
import { desktop } from "./transport";
if (desktop) {
  document.addEventListener("click", (event) => {
    const link = (event.target as Element).closest<HTMLAnchorElement>("a[href]");
    if (!link || event.defaultPrevented) return;
    const url = new URL(link.href);
    if (url.origin === location.origin) return;
    event.preventDefault();
    if (["https:", "http:"].includes(url.protocol)) {
      // The Go side opens it with the system browser; the webview's own
      // navigation whitelist refuses anything that is not the app.
      void call("shell.OpenExternal", url.href)
        .catch(() => window.alert("无法打开链接，请复制地址到浏览器。"));
    }
  });
}
async function mountApp() {
  // Separate branches keep Vite's CSS preload attached to the correct entry.
  if (desktop) {
    const { default: DesktopRoot } = await import("./DesktopRoot.vue");
    createApp(DesktopRoot).mount("#app");
  } else {
    const { default: App } = await import("./App.vue");
    createApp(App).mount("#app");
  }
}
mountApp().catch(() => {
    const root = document.getElementById("app");
    if (root) {
      root.setAttribute("role", "alert");
      root.textContent = "页面资源加载失败，请按 Ctrl+F5 刷新后重试。";
    }
});
