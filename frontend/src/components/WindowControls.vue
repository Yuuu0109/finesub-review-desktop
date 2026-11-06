<script setup lang="ts">
// 无边框窗口没有系统标题栏，最小化、最大化与关闭由页面自己画。
import { onMounted, onUnmounted, ref } from "vue";

type Controls = {
  minimize(): Promise<void>;
  toggleMaximize(): Promise<void>;
  isMaximized(): Promise<boolean>;
  close(): Promise<void>;
};

const props = defineProps<{ closeDisabled?: boolean }>();
const controls = ref<Controls | null>(null);
const maximized = ref(false);

async function sync() {
  if (!controls.value) return;
  try {
    maximized.value = await controls.value.isMaximized();
  } catch {
    // 窗口正在关闭时查询会失败，保持上一次状态即可。
  }
}

onMounted(async () => {
  try {
    const { currentWindow } = await import("mygo-runtime");
    controls.value = currentWindow;
    await sync();
    window.addEventListener("resize", sync);
  } catch {
    controls.value = null;
  }
});

onUnmounted(() => window.removeEventListener("resize", sync));

async function minimize() {
  try {
    await controls.value?.minimize();
  } catch {
    // 忽略：窗口已不可用。
  }
}

async function toggle() {
  try {
    await controls.value?.toggleMaximize();
  } catch {
    // 忽略：窗口已不可用。
  }
  await sync();
}

async function close() {
  if (props.closeDisabled) return;
  if (!window.dispatchEvent(new Event("desktop-before-update", { cancelable: true }))) {
    window.alert("操作未完成或草稿未能保存");
    return;
  }
  try {
    await controls.value?.close();
  } catch {
    // 忽略：窗口已不可用。
  }
}
</script>

<template>
  <div v-if="controls" class="window-controls">
    <button type="button" title="最小化" aria-label="最小化" @click="minimize">
      <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 5h6" /></svg>
    </button>
    <button
      type="button"
      :title="maximized ? '还原' : '最大化'"
      :aria-label="maximized ? '还原' : '最大化'"
      @click="toggle"
    >
      <svg v-if="!maximized" viewBox="0 0 10 10" aria-hidden="true">
        <rect x="2.5" y="2.5" width="5" height="5" />
      </svg>
      <svg v-else viewBox="0 0 10 10" aria-hidden="true">
        <rect x="2" y="3.5" width="4.5" height="4.5" />
        <path d="M3.5 3.5V2h4.5v4.5H6.5" />
      </svg>
    </button>
    <button type="button" class="window-close" :disabled="closeDisabled" title="关闭" aria-label="关闭" @click="close">
      <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2.5 2.5l5 5M7.5 2.5l-5 5" /></svg>
    </button>
  </div>
</template>

