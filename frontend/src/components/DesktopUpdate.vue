<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { call, on } from "mygo-runtime";
import { scopedDraftKey } from "../transport";
import Modal from "./Modal.vue";
import Icon from "./Icon.vue";
type UpdateInfo = { version: string; notes: string; prepared: boolean };
type Progress = { downloaded: number; total: number };
const emit = defineEmits<{ busy: [value: boolean] }>();
const version = ref(""), open = ref(false), checking = ref(false), downloading = ref(false), applying = ref(false);
const error = ref(""), checked = ref(false), downloaded = ref(0), total = ref<number | null>(null);
const update = ref<UpdateInfo | null>(null);
const busy = computed(() => downloading.value || applying.value);
const percent = computed(() => total.value ? Math.min(100, Math.max(0, Math.round(downloaded.value / total.value * 100))) : null);
const status = computed(() => applying.value ? "正在重启" : downloading.value ? "正在更新" : update.value?.prepared ? "重启更新" : update.value ? "有新版本" : checking.value ? "检查中" : error.value ? "检查失败" : "检查更新");
watch(busy, value => emit("busy", value), { flush: "sync" });
let unsubscribe: (() => void) | undefined;
function message(e: unknown) { return e instanceof Error ? e.message : String(e); }
async function check(visible = true, startup = false) {
  if (visible) open.value = true;
  if (checking.value || busy.value || update.value?.prepared) return;
  checking.value = true; error.value = ""; update.value = null; checked.value = false;
  try {
    update.value = await call<UpdateInfo | null>(startup ? "shell.CheckUpdateStartup" : "shell.CheckUpdate");
    checked.value = true;
  } catch (e) { error.value = message(e); }
  finally { checking.value = false; }
}
async function download() {
  if (!update.value || busy.value) return;
  downloading.value = true; error.value = ""; downloaded.value = 0; total.value = null;
  try {
    await call("shell.DownloadUpdate", update.value.version);
    update.value.prepared = true;
  } catch (e) { error.value = message(e); update.value = null; }
  finally { downloading.value = false; }
}
async function apply() {
  if (busy.value || !update.value?.prepared) return;
  applying.value = true; error.value = "";
  try {
    if (!window.dispatchEvent(new Event("desktop-before-update", { cancelable: true })))
      throw new Error("操作未完成或草稿未能保存，暂不能重启更新。");
    localStorage.setItem(scopedDraftKey("finesub-update-route"), location.hash);
    await call("shell.ApplyUpdate");
  } catch (e) { error.value = message(e); applying.value = false; }
}
function beforeExit(event: Event) { if (downloading.value) event.preventDefault(); }
onMounted(async () => {
  window.addEventListener("desktop-before-update", beforeExit);
  try {
    unsubscribe = on<Progress>("desktop-update-progress", progress => {
      if (!downloading.value) return;
      downloaded.value = Math.max(0, progress.downloaded);
      // A failed delta download may restart at zero for the full archive.
      total.value = progress.total > 0 ? progress.total : null;
    });
    version.value = await call<string>("shell.Version");
    await check(false, true);
  } catch (e) { error.value = message(e); }
});
onUnmounted(() => {
  unsubscribe?.();
  window.removeEventListener("desktop-before-update", beforeExit);
});
</script>
<template>
  <button type="button" class="desktop-update-button" :class="{ available: update, failed: error }" :title="`v${version || '…'} · ${status}`" :aria-label="`v${version || '…'} · ${status}`" @click="update || busy || checking || error ? open = true : check()">
    <Icon :name="update?.prepared ? 'check' : 'download'" :size="14" />
    <span>v{{ version || '…' }}</span><span>{{ status }}</span>
  </button>
  <Teleport to="body"><Modal v-if="open" title="应用更新" :busy="busy" @close="open = false">
    <div class="desktop-update-body" :aria-busy="busy || checking">
      <div class="update-version"><span class="update-icon"><Icon name="download" :size="24" /></span><div><strong>FineSub Review</strong><p>v{{ version || '…' }}<template v-if="update"> <Icon name="arrow" :size="13" /> <b>v{{ update.version }}</b></template></p></div></div>
      <p v-if="checking" class="update-status" role="status"><span class="spinner"></span>正在检查更新</p>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <template v-if="update">
        <p v-if="update.prepared" class="update-ready" role="status"><Icon name="shield" :size="17" />已安装 · 重启生效</p>
        <details v-if="update.notes" class="update-notes"><summary>更新记录</summary><p class="desktop-release-notes">{{ update.notes }}</p></details>
      </template>
      <p v-else-if="checked && !error && !checking" class="update-ready" role="status"><Icon name="check" :size="17" />已是最新版本</p>
      <div v-if="downloading" class="update-download">
        <progress aria-label="新版下载进度" :value="percent ?? undefined" max="100"></progress>
        <div class="update-transfer" role="status"><span>{{ percent === 100 ? '正在校验并安装' : '正在下载' }}</span><span>{{ percent === null ? '…' : percent + '%' }}</span></div>
        <small>{{ (downloaded / 1048576).toFixed(1) }} MB<template v-if="total"> / {{ (total / 1048576).toFixed(1) }} MB</template></small>
      </div>
      <p v-if="applying" role="status">正在重启…</p>
      <div class="update-footer">
        <a href="https://github.com/Yuuu0109/finesub-review-desktop/releases" target="_blank" rel="noopener noreferrer">发布页 <Icon name="arrow" :size="14" /></a>
        <div class="modal-actions">
          <button class="button" :disabled="busy" @click="open = false">{{ update?.prepared ? '稍后重启' : '关闭' }}</button>
          <button v-if="update?.prepared" class="button primary" :disabled="busy" @click="apply">立即重启</button>
          <button v-else-if="update" class="button primary" :disabled="checking || busy" @click="download">下载并安装</button>
          <button v-else class="button primary" :disabled="checking || busy" @click="check()">重新检查</button>
        </div>
      </div>
    </div>
  </Modal></Teleport>
</template>
<style scoped>
.desktop-update-button { display: inline-flex; align-items: center; gap: 8px; white-space: nowrap; }
.desktop-update-button.available { color: var(--brand-600); }
.desktop-update-button.failed { color: var(--amber-600); }
.desktop-update-body { display: grid; gap: 20px; }
.desktop-release-notes { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 220px; overflow: auto; padding-top: 12px; font-size: var(--text-sm); }
.update-version { display: flex; align-items: center; gap: 14px; }
.update-version p { display: flex; align-items: center; gap: 8px; font: 12px var(--font-mono); color: var(--ink-3); margin-top: 7px; }
.update-version b { color: var(--brand-600); font-weight: 500; }
.update-icon { display: grid; place-items: center; width: 48px; height: 48px; border: 1px solid var(--brand-100); border-radius: var(--r-lg); background: var(--brand-50); color: var(--brand-600); }
.update-status, .update-ready { display: flex; align-items: center; gap: 8px; }
.update-ready { color: var(--green-700); }
.update-notes { border-top: 1px solid var(--line-1); border-bottom: 1px solid var(--line-1); padding: 12px 0; }
.update-notes summary { cursor: pointer; color: var(--ink-2); font-size: var(--text-sm); }
.update-transfer, .update-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.update-download small { color: var(--ink-3); font: 11px var(--font-mono); }
.update-transfer { font-size: var(--text-sm); margin: 8px 0; }
.update-footer { flex-wrap: wrap; padding-top: 4px; }
.update-footer a { display: inline-flex; align-items: center; gap: 6px; font-size: var(--text-xs); }
.update-footer .modal-actions { margin: 0; padding: 0; border: 0; }
progress { display: block; width: 100%; height: 6px; accent-color: var(--brand-500); }
</style>
