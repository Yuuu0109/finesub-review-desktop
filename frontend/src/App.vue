<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref } from "vue";
import { api, post, setCsrf, type User, type Notice, date } from "./api";
import { request } from "./transport";
import Icon from "./components/Icon.vue";
import Brand from "./components/Brand.vue";
import WelcomePanel from "./components/WelcomePanel.vue";
import Modal from "./components/Modal.vue";
import Dashboard from "./pages/Dashboard.vue";
import Entries from "./pages/Entries.vue";
import ReviewPage from "./pages/ReviewPage.vue";
import Streamer from "./pages/Streamer.vue";
import Imports from "./pages/Imports.vue";
import Releases from "./pages/Releases.vue";
import Settings from "./pages/Settings.vue";
const user = ref<User | null>(null),
  people = ref<User[]>([]),
  settings = ref<any>({ name: "FineSub 知识库" }),
  notices = ref<Notice[]>([]);
const loading = ref(true),
  loginBusy = ref(false),
  loginError = ref(""),
  username = ref(""),
  password = ref(""),
  passphrase = ref(""),
  accessConfigured = ref(false),
  adminLoginOpen = ref(false),
  initialized = ref(true);
const route = ref(location.hash.slice(2) || "dashboard"),
  sidebar = ref(false),
  noticeOpen = ref(false),
  globalSearch = ref("");
const toastMessage = ref(""),
  toastError = ref(false);
let toastTimer: ReturnType<typeof setTimeout>,
  poll: ReturnType<typeof setInterval>;
const routePath = computed(() => route.value.split("?")[0] || "dashboard");
const routeQuery = computed(() => route.value.split("?").slice(1).join("?"));
const page = computed(() => routePath.value.split("/")[0] || "dashboard"),
  selectedId = computed(() => routePath.value.split("/")[1] || "");
const nav = [
  { id: "dashboard", label: "工作台", icon: "grid" },
  { id: "entries", label: "知识库", icon: "book" },
  { id: "streamer", label: "主播档案", icon: "users" },
  { id: "mine", label: "我的审核", icon: "check" },
  { id: "imports", label: "导入批次", icon: "upload" },
  { id: "releases", label: "发布版本", icon: "download" },
];
const title = computed(() =>
  page.value === "review"
    ? "词条审核"
    : page.value === "settings"
      ? "管理设置"
      : page.value === "account"
        ? "账号设置"
        : nav.find((n) => n.id === page.value)?.label || "工作台",
);
const unread = computed(() => notices.value.filter((n) => !n.read).length);
function toast(message: string, error = false) {
  toastMessage.value = message;
  toastError.value = error;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toastMessage.value = ""), 5500);
}
function go(target: string, id?: string, query?: string) {
  location.hash = "/" + target + (id ? "/" + id : "") + (query ? "?" + query : "");
  sidebar.value = false;
}
function hashChanged() {
  route.value = location.hash.slice(2) || "dashboard";
}
function closeSidebar(event: KeyboardEvent) {
  if (event.key === "Escape") sidebar.value = false;
}
async function refresh() {
  if (!user.value) return;
  const result = await Promise.all([
    api<User[]>("/users"),
    api("/settings"),
    api<Notice[]>("/notices"),
  ]);
  people.value = result[0];
  settings.value = result[1];
  notices.value = result[2];
}
provide("app", { user, people, settings, go, toast, refresh });
async function enter() {
  loginBusy.value = true;
  loginError.value = "";
  try {
    const result = await post("/auth/enter", { passphrase: passphrase.value });
    user.value = result.user;
    setCsrf(result.csrf);
    passphrase.value = "";
    await refresh();
  } catch (e) {
    loginError.value = (e as Error).message;
  } finally {
    loginBusy.value = false;
  }
}
function openAdminLogin() {
  loginError.value = "";
  password.value = "";
  adminLoginOpen.value = true;
}
function closeAdminLogin() {
  if (loginBusy.value) return;
  adminLoginOpen.value = false;
  password.value = "";
  loginError.value = "";
}
async function signIn() {
  loginBusy.value = true;
  loginError.value = "";
  try {
    const result = await post("/auth/login", {
      username: username.value,
      password: password.value,
    });
    user.value = result.user;
    setCsrf(result.csrf);
    password.value = "";
    adminLoginOpen.value = false;
    await refresh();
  } catch (e) {
    loginError.value = (e as Error).message;
  } finally {
    loginBusy.value = false;
  }
}
async function signOut() {
  try {
    if (user.value?.role === "admin") {
      const result = await post("/auth/logout");
      user.value = result.user;
      setCsrf(result.csrf);
      await refresh();
    } else {
      await post("/auth/leave");
      user.value = null;
      setCsrf("");
      notices.value = [];
    }
    go("dashboard");
  } catch (e) {
    toast((e as Error).message, true);
  }
}
async function expired() {
  adminLoginOpen.value = false;
  password.value = "";
  try {
    const result = await request("/auth/me");
    if (!result.ok) throw new Error();
    const body = await result.json();
    user.value = body.user;
    setCsrf(body.csrf);
    go("dashboard");
    await refresh();
  } catch {
    user.value = null;
    setCsrf("");
    notices.value = [];
  }
}
async function markRead() {
  try {
    await post("/notices/read");
    await refresh();
  } catch (e) {
    toast((e as Error).message, true);
  }
}
onMounted(async () => {
  window.addEventListener("hashchange", hashChanged);
  window.addEventListener("keydown", closeSidebar);
  window.addEventListener("session-expired", expired);
  try {
    const b = await api("/bootstrap");
    initialized.value = b.initialized;
    accessConfigured.value = b.access_configured;
    if (b.access_configured) {
      try {
        const result = await api("/auth/me");
        user.value = result.user;
        setCsrf(result.csrf);
        await refresh();
      } catch {}
    }
  } catch (e) {
    loginError.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
  poll = setInterval(() => {
    if (user.value)
      api<Notice[]>("/notices")
        .then((n) => (notices.value = n))
        .catch(() => {});
  }, 30000);
});
onUnmounted(() => {
  clearInterval(poll);
  clearTimeout(toastTimer);
  window.removeEventListener("hashchange", hashChanged);
  window.removeEventListener("keydown", closeSidebar);
  window.removeEventListener("session-expired", expired);
});
</script>
<template>
  <div v-if="loading" class="boot">
    <span class="spinner"></span>正在打开知识库
  </div>
  <main v-else-if="!user" class="entry-screen login-screen">
    <div class="entry-layout">
      <WelcomePanel />
      <section class="entry-form login-form">
        <div class="entry-form-content">
          <span class="entry-symbol"><Icon name="key" :size="24" /></span>
          <h2>进入知识库</h2>
          <form v-if="accessConfigured" @submit.prevent="enter">
            <label for="access-passphrase">访问口令</label>
            <div class="entry-input">
              <Icon name="key" :size="18" /><input
                id="access-passphrase"
                v-model="passphrase"
                type="password"
                autocomplete="off"
                required
                autofocus
                maxlength="256"
                placeholder="输入访问口令"
                :disabled="loginBusy"
              />
            </div>
            <p v-if="loginError" class="form-error" role="alert">
              {{ loginError }}
            </p>
            <button class="button primary full" :disabled="loginBusy">
              <span v-if="loginBusy" class="spinner"></span>
              {{ loginBusy ? "正在进入…" : "进入知识库"
              }}<Icon v-if="!loginBusy" name="arrow" :size="18" />
            </button>
          </form>
          <div v-else class="callout">
            {{ loginError || "访问口令尚未设置，请联系管理员。" }}
          </div>
        </div>
      </section>
    </div>
  </main>
  <div v-else class="app-shell">
    <div v-if="sidebar" class="sidebar-overlay" @click="sidebar = false"></div>
    <aside id="app-navigation" class="sidebar" :class="{ expanded: sidebar }">
      <button
        class="brand-home"
        aria-label="FineSub 工作台"
        @click="go('dashboard')"
      >
        <Brand />
      </button>
      <div class="workspace-label">{{ settings.name }}</div>
      <button
        class="icon-button mobile-only sidebar-close"
        aria-label="关闭导航"
        @click="sidebar = false"
      >
        <Icon name="close" :size="18" />
      </button>
      <nav aria-label="主导航">
        <button
          v-for="item in nav"
          :key="item.id"
          :class="{
            active:
              page === item.id || (page === 'review' && item.id === 'entries'),
          }"
          :aria-current="
            page === item.id || (page === 'review' && item.id === 'entries')
              ? 'page'
              : undefined
          "
          @click="go(item.id)"
        >
          <Icon :name="item.icon" /><span>{{ item.label }}</span>
        </button>
      </nav>
      <div class="sidebar-bottom">
        <button
          v-if="user.role === 'admin'"
          class="sidebar-action"
          :class="{ active: page === 'settings' }"
          @click="go('settings')"
        >
          <Icon name="settings" /><span>管理设置</span>
        </button>
        <div class="user-card">
          <button class="user-identity" @click="go('account')">
            <span class="avatar">{{ user.name.slice(0, 1) }}</span
            ><span
              ><strong>{{ user.name }}</strong
              ><small>{{
                user.role === "admin" ? "管理员" : "普通成员"
              }}</small></span
            ></button
          ><button
            class="icon-button"
            :aria-label="
              user.role === 'admin' ? '退出管理员登录' : '退出知识库'
            "
            :title="user.role === 'admin' ? '退出管理员登录' : '退出知识库'"
            @click="signOut"
          >
            <Icon name="logout" :size="17" />
          </button>
        </div>
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <div class="topbar-title">
          <button
            class="icon-button mobile-only"
            aria-label="打开导航"
            aria-controls="app-navigation"
            :aria-expanded="sidebar"
            @click="sidebar = true"
          >
            <Icon name="menu" />
          </button>
          <strong>{{ title }}</strong>
        </div>
        <div class="topbar-actions">
          <button
            v-if="user.role !== 'admin'"
            class="button small"
            @click="openAdminLogin"
          >
            管理员登录
          </button>
          <form
            class="global-search"
            @submit.prevent="
              go('entries');
              route = 'entries';
            "
          >
            <Icon name="search" :size="17" /><input
              v-model="globalSearch"
              aria-label="搜索知识库"
              placeholder="搜索词条、别名、正文"
              @keydown.enter="go('entries')"
            />
          </form>
          <button
            class="icon-button notification-button"
            aria-label="查看通知"
            @click="noticeOpen = true"
          >
            <Icon name="bell" /><span
              v-if="unread"
              class="notification-dot"
            ></span></button
          ><span class="role-chip">{{
            user.role === "admin" ? "管理员" : "普通成员"
          }}</span>
        </div>
      </header>
      <main
        :key="user.id"
        class="page"
        :class="{ 'review-layout': page === 'review' }"
      >
        <Dashboard v-if="page === 'dashboard'" /><Entries
          v-else-if="page === 'entries' || page === 'mine'"
          :key="route"
          :mine="page === 'mine'"
          :search="globalSearch"
          :initial-status="page === 'entries' ? selectedId : ''"
          :initial-query="routeQuery"
        /><ReviewPage
          v-else-if="page === 'review'"
          :key="selectedId"
          :entry-id="selectedId"
          :queue-query="routeQuery"
        /><Streamer
          v-else-if="page === 'streamer'"
          :key="selectedId"
          :entry-id="selectedId"
        /><Imports
          v-else-if="page === 'imports'"
          :batch-id="selectedId"
        /><Releases v-else-if="page === 'releases'" /><Settings
          v-else-if="
            (page === 'settings' && user.role === 'admin') || page === 'account'
          "
          :key="page"
          :account-only="page === 'account'"
        />
        <div v-else class="empty-state">
          <Icon name="file" :size="36" />
          <h2>页面不存在</h2>
          <button class="button" @click="go('dashboard')">返回工作台</button>
        </div>
      </main>
    </div>
    <Modal v-if="adminLoginOpen" title="管理员登录" @close="closeAdminLogin">
      <form v-if="initialized" @submit.prevent="signIn">
        <label
          >管理员账号<input
            v-model="username"
            autocomplete="username"
            autofocus
            required
            maxlength="80"
            placeholder="输入管理员账号"
        /></label>
        <label
          >密码<input
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            maxlength="256"
            placeholder="输入管理员密码"
        /></label>
        <p v-if="loginError" class="form-error" role="alert">
          {{ loginError }}
        </p>
        <div class="modal-actions">
          <button
            type="button"
            class="button"
            :disabled="loginBusy"
            @click="closeAdminLogin"
          >
            取消
          </button>
          <button class="button primary" :disabled="loginBusy">
            {{ loginBusy ? "正在登录…" : "登录" }}
          </button>
        </div>
      </form>
      <p v-else class="callout">管理员尚未初始化</p>
    </Modal>
    <Modal v-if="noticeOpen" title="通知" @close="noticeOpen = false"
      ><div class="toolbar">
        <span class="muted">{{ unread }} 条未读</span
        ><button class="text-button" @click="markRead">全部标为已读</button>
      </div>
      <div v-if="!notices.length" class="empty-small">暂时没有通知。</div>
      <button
        v-for="notice in notices"
        :key="notice.id"
        class="notice-row"
        :class="{ unread: !notice.read }"
        @click="
          () => {
            if (notice.entry_id) {
              go('review', notice.entry_id);
              noticeOpen = false;
            }
          }
        "
      >
        <span>{{ notice.text }}</span
        ><small>{{ date(notice.created_at) }}</small>
      </button></Modal
    >
  </div>
  <div
    v-if="toastMessage"
    class="toast"
    :class="{ error: toastError }"
    role="status"
  >
    <Icon :name="toastError ? 'message' : 'check'" :size="18" />{{
      toastMessage
    }}
  </div>
</template>
