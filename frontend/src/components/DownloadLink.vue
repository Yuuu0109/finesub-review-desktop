<script setup lang="ts">
import { ref } from "vue";
import { call } from "mygo-runtime";
import { desktop } from "../transport";
import { apiResponse } from "../api";
import { downloadFilename } from "../server";
import { useApp } from "../context";
const props = defineProps<{ path: string; filename?: string }>();
const busy = ref(false);
const { toast } = useApp();
async function download(event: MouseEvent) {
  if (!desktop) return;
  event.preventDefault();
  if (busy.value) return;
  busy.value = true;
  try {
    const response = await apiResponse(props.path);
    const bytes = new Uint8Array(await response.arrayBuffer());
    // The name comes from the response header, which the Go-side fetch makes
    // readable; the Go side owns the save dialog and the write.
    const name = downloadFilename(response.headers.get("content-disposition"), props.filename || "download.zip");
    const saved = await call<string>("shell.SaveDownload", name, bytes);
    if (saved) toast("文件已保存");
  } catch (e) {
    toast(e instanceof Error ? e.message : String(e), true);
  } finally {
    busy.value = false;
  }
}
</script>
<template><a :href="'/api' + path" :aria-disabled="busy" @click="download"><slot />{{ busy ? " · 正在下载…" : "" }}</a></template>
