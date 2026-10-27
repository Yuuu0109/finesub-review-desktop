<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { call } from "mygo-runtime";
import { scopedDraftKey } from "../transport";
import Modal from "./Modal.vue";
import Icon from "./Icon.vue";
type UpdateInfo = { version: string; notes: string; prepared: boolean };
const version = ref(""), open = ref(false), checking = ref(false), downloading = ref(false), applying = ref(false);
const error = ref(""), notice = ref(""), checked = ref(false), downloaded = ref(0), total = ref<number | null>(null);
const update = ref<UpdateInfo | null>(null);
const busy = computed(() => downloading.value || applying.value);
const percent = computed(() => total.value ? Math.min(100, Math.round(downloaded.value / total.value * 100)) : null);
function message(e: unknown) { return e instanceof Error ? e.message : String(e); }
/**
 * Ask for a newer version.
 *
 * `startup` reuses the answer of a check made in the last day: GitHub's API
 * allows 60 unauthenticated requests an hour per address, and the app checks at
 * every launch, so a restart-happy user would spend the quota on launches. The
 * user's own "重新检查" and the 下载并安装 button always ask the server, which is
 * why the Go side asks again before installing when nothing is pending.
 */
async function check(visible = true, startup = false) {
  if (visible) open.value = true;
  if (checking.value || busy.value) return;
  checking.value = true; error.value = ""; notice.value = ""; update.value = null; checked.value = false;
  try {
    update.value = await call<UpdateInfo>(startup ? "shell.CheckUpdateStartup" : "shell.CheckUpdate");
    checked.value = true;
  }
  catch (e) { error.value = message(e); }
  finally { checking.value = false; }
}
async function download() {
  if (!update.value || busy.value) return;
  downloading.value = true; error.value = ""; notice.value = ""; downloaded.value = 0; total.value = null;
  try {
    // This call installs: the Go side verifies the signature and then replaces
    // the files. It is not a download that a later click applies, so the
    // wording must not offer that.
    await call("shell.DownloadUpdate", update.value.version);
    update.value.prepared = true;
    notice.value = "已安装，重启后生效。关闭本窗口不会撤销安装，下次启动即为新版本。";
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
onMounted(async () => {
  try { version.value = await call<string>("shell.Version"); await check(false, true); }
  catch (e) { error.value = message(e); }
});
</script>
<template>
  <button type="button" class="desktop-update-button" @click="update || busy || checking ? open = true : check()">v{{ version || '…' }} · {{ update?.prepared ? '重启更新' : update ? '有新版本' : checking ? '检查中' : '检查更新' }}</button>
  <Teleport to="body"><Modal v-if="open" title="应用更新" @close="!busy && (open = false)">
    <div class="desktop-update-body" aria-live="polite">
      <div class="update-version"><span class="update-icon"><Icon name="download" :size="24" /></span><div><strong>FineSub Review</strong><p>当前 v{{ version }} · 免安装版</p></div></div>
      <p v-if="checking">正在检查更新…</p>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <p v-if="notice" class="update-notice" role="status">{{ notice }}</p>
      <template v-if="update">
        <h3>新版本 {{ update.version }}</h3>
        <p v-if="update.notes" class="desktop-release-notes">{{ update.notes }}</p>
        <p v-if="update.prepared" class="update-ready"><Icon name="shield" :size="17" />更新已通过签名校验并安装</p>
      </template>
      <p v-else-if="checked && !error && !checking">已是最新版本。</p>
      <template v-if="downloading">
        <progress aria-label="新版下载进度" :value="percent ?? undefined" max="100"></progress>
        <p>{{ percent === 100 ? '正在校验签名并安装…' : `正在下载并安装 ${percent === null ? (downloaded / 1048576).toFixed(1) + ' MB' : percent + '%'}` }}</p>
      </template>
      <p v-if="applying" role="status">正在重启…</p>
      <div class="modal-actions">
        <button class="button" :disabled="busy" @click="open = false">{{ update?.prepared ? '关闭' : '稍后' }}</button>
        <template v-if="update?.prepared">
          <button class="button primary" :disabled="busy" @click="apply">立即重启</button>
        </template>
        <button v-else-if="update" class="button primary" :disabled="checking || busy" @click="download">下载并安装</button>
        <button v-else class="button primary" :disabled="checking || busy" @click="check()">重新检查</button>
      </div>
    </div>
  </Modal></Teleport>
</template>
<style scoped>
.desktop-update-button { border: 0; background: transparent; color: var(--brand-600); font-size: var(--text-xs); white-space: nowrap; }
.desktop-update-body { display: grid; gap: 16px; }
.desktop-release-notes { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 240px; overflow: auto; line-height: 1.8; }
.update-version { display: flex; align-items: center; gap: 14px; }
.update-version p { font-size: 12px; color: var(--ink-3); margin-top: 4px; }
.update-icon { display: grid; place-items: center; width: 48px; height: 48px; border: 1px solid var(--line-1); border-radius: 12px; background: var(--surface-2); color: var(--brand-600); }
.update-ready { display: flex; align-items: center; gap: 8px; color: var(--green-700); }
.update-notice { padding: 10px 12px; border: 1px solid var(--line-1); border-radius: 10px; background: var(--surface-2); color: var(--ink-2); font-size: var(--text-xs); line-height: 1.7; }
progress { width: 100%; accent-color: var(--brand-600); }
</style>
