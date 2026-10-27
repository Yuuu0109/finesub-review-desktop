<script setup lang="ts">
import { onMounted, ref } from "vue";
import App from "./App.vue";
import Icon from "./components/Icon.vue";
import { probeServer, savedServer, selectServer, scopedDraftKey } from "./transport";
import { normalizeServer } from "./server";
import DesktopUpdate from "./components/DesktopUpdate.vue";
import WelcomePanel from "./components/WelcomePanel.vue";
const address = ref(savedServer());
const serverSetupKey = "finesub-review.server-setup";
const connected = ref(false),
  checking = ref(true),
  busy = ref(false),
  error = ref("");
async function connect() {
  busy.value = true;
  error.value = "";
  try {
    const value = normalizeServer(address.value);
    await probeServer(value);
    selectServer(value);
    const resumeKey = scopedDraftKey("finesub-update-route");
    const resume = localStorage.getItem(resumeKey);
    if (resume && /^#\/(dashboard|entries|mine|review|imports|releases|streamer)([/?]|$)/.test(resume)) location.hash = resume;
    localStorage.removeItem(resumeKey);
    address.value = value;
    connected.value = true;
  } catch (e) {
    error.value =
      e instanceof Error ? e.message : "连接失败，请检查服务器地址和网络";
  } finally {
    busy.value = false;
  }
}
function switchServer() {
  if (window.confirm("返回服务器设置？请先保存当前编辑内容。")) {
    sessionStorage.setItem(serverSetupKey, "true");
    location.reload();
  }
}
onMounted(async () => {
  document.title = "FineSub 知识库审核";
  if (sessionStorage.getItem(serverSetupKey)) {
    sessionStorage.removeItem(serverSetupKey);
  } else {
    await connect();
  }
  checking.value = false;
});
</script>
<template>
  <div class="desktop-bar">
    <div class="desktop-bar-actions">
      <template v-if="connected">
        <span class="server-status"><span></span>已连接</span>
        <button class="server-switch" type="button" :title="address" @click="switchServer"><Icon name="settings" :size="14" />更换服务器</button>
      </template>
      <DesktopUpdate />
    </div>
  </div>
  <div v-if="checking" class="boot" role="status">
    <span class="spinner"></span>正在连接知识库
  </div>
  <div v-else-if="connected" class="desktop-connected">
    <App />
  </div>
  <main v-else class="entry-screen desktop-connect">
    <div class="entry-layout">
      <WelcomePanel />
      <section class="entry-form desktop-card">
        <div class="entry-form-content">
          <span class="entry-symbol"><Icon name="link" :size="24" /></span>
          <h2>连接审核服务器</h2>
          <form @submit.prevent="connect">
            <label for="server-address">服务器地址</label>
            <div class="entry-input">
              <Icon name="link" :size="18" /><input
                id="server-address"
                v-model="address"
                type="url"
                required
                autofocus
                :disabled="busy"
                placeholder="https://review.example.com"
                autocomplete="url"
                spellcheck="false"
              />
            </div>
            <p v-if="error" class="form-error" role="alert">{{ error }}</p>
            <button class="button primary full" :disabled="busy">
              <span v-if="busy" class="spinner"></span
              >{{ busy ? "正在连接…" : "连接"
              }}<Icon v-if="!busy" name="arrow" :size="18" />
            </button>
          </form>
        </div>
        <div class="entry-form-footer">
          <span>桌面客户端 · 免安装运行</span>
        </div>
      </section>
    </div>
  </main>
</template>
