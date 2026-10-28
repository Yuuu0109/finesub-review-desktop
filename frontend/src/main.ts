import { createApp } from "vue";
import "./style.css";
import "./workflow.css";

// This entry is the standalone web client served by FastAPI.
import("./App.vue").then(({ default: App }) => {
  createApp(App).mount("#app");
}).catch(() => {
  const root = document.getElementById("app");
  if (root) {
    root.setAttribute("role", "alert");
    root.textContent = "页面资源加载失败，请刷新后重试。";
  }
});

