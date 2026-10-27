<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, post, date, labels, type Batch } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import Modal from "../components/Modal.vue";
import Diff from "../components/Diff.vue";
import DownloadLink from "../components/DownloadLink.vue";
import { toggleGroup } from "../workflow";
const props = defineProps<{ batchId?: string }>();
const { user, go, toast, refresh } = useApp();
const batches = ref<Batch[]>([]),
  active = ref<Batch | null>(null),
  uploadOpen = ref(false),
  busy = ref(false),
  files = ref<File[]>([]);
const batchName = ref(""),
  source = ref(""),
  category = ref("common"),
  chosen = ref<string[]>([]),
  preview = ref<any>(null),
  dragging = ref(false),
  confirmOpen = ref(false),
  batchPage = ref(1);
const admin = computed(() => user.value?.role === "admin");
const groupBlocked = computed(() => active.value?.items?.some(i => i.grouped && !!i.error));
const selectable = computed(
  () =>
    active.value?.items?.filter(
      (i) => !i.error && i.classification !== "duplicate" && !(i.grouped && groupBlocked.value),
    ) || [],
);
function chooseItem(id: string, checked: boolean) {
  const items = selectable.value.map(i => ({ ...i, group_id: i.grouped ? active.value!.id : undefined }));
  const item = items.find(i => i.id === id);
  if (item) chosen.value = toggleGroup(items, chosen.value, item, checked);
}
let timer: ReturnType<typeof setInterval>;
async function load() {
  try {
    batches.value = await api<Batch[]>("/imports?page=" + batchPage.value);
    if (props.batchId) await open(props.batchId);
  } catch (e) {
    toast((e as Error).message, true);
  }
}
async function open(id: string) {
  try {
    const old = active.value;
    active.value = await api<Batch>("/imports/" + id);
    if (old?.id !== id || old.status !== active.value.status)
      chosen.value = selectable.value.map((i) => i.id);
  } catch (e) {
    toast((e as Error).message, true);
  }
}
function pick(list: FileList | null) {
  files.value = Array.from(list || []);
  if (!batchName.value && files.value.length)
    batchName.value =
      files.value.length === 1
        ? files.value[0]!.name.replace(/\.[^.]+$/, "")
        : `知识库导入 · ${new Date().toLocaleDateString("zh-CN")}`;
}
async function upload() {
  busy.value = true;
  try {
    const data = new FormData();
    data.append("name", batchName.value);
    data.append("source", source.value);
    data.append("category", category.value);
    files.value.forEach((f) =>
      data.append("files", f, f.webkitRelativePath || f.name),
    );
    const b = await api("/imports", { method: "POST", body: data });
    uploadOpen.value = false;
    files.value = [];
    batchName.value = "";
    source.value = "";
    go("imports", b.id);
    await load();
    toast("文件已上传，正在执行预检");
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
async function confirm() {
  busy.value = true;
  try {
    const result = await post("/imports/" + active.value!.id + "/confirm", {
      item_ids: chosen.value,
    });
    confirmOpen.value = false;
    await load();
    await refresh();
    toast(`已生成 ${result.created} 个待裁决项`);
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
async function showItem(id: string) {
  try {
    preview.value = await api("/import-items/" + id);
  } catch (e) {
    toast((e as Error).message, true);
  }
}
async function retry() {
  busy.value = true;
  try {
    await post("/jobs/" + active.value!.job!.id + "/retry");
    await load();
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
watch(
  () => props.batchId,
  (id) => {
    if (id) open(id);
    else active.value = null;
  },
);
onMounted(() => {
  load();
  timer = setInterval(() => {
    if (active.value && ["queued", "processing"].includes(active.value.status))
      open(active.value.id);
  }, 2000);
});
onUnmounted(() => clearInterval(timer));
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>导入批次</h1>
    </div>
    <button v-if="admin" class="button primary" @click="uploadOpen = true">
      <Icon name="upload" :size="18" />新建导入
    </button>
  </div>
  <div class="imports-grid">
    <section class="panel batch-list">
      <div class="panel-heading">
        <h2>导入批次</h2>
        <button class="icon-button" aria-label="刷新导入批次" @click="load">
          <Icon name="refresh" :size="17" />
        </button>
      </div>
      <div v-if="!batches.length" class="empty-small">暂无导入批次。</div>
      <button
        v-for="batch in batches"
        :key="batch.id"
        class="batch-list-item"
        :class="{ selected: active?.id === batch.id }"
        @click="go('imports', batch.id)"
      >
        <strong>{{ batch.name }}</strong>
        <p>{{ batch.source || "未填写来源" }}</p>
        <div>
          <span class="badge" :class="batch.status">{{
            labels[batch.status]
          }}</span
          ><small>{{ date(batch.created_at) }}</small>
        </div>
      </button>
      <div class="pagination">
        <button
          class="button small"
          :disabled="batchPage === 1"
          @click="
            batchPage--;
            load();
          "
        >
          上一页</button
        ><button
          class="button small"
          :disabled="batches.length < 30"
          @click="
            batchPage++;
            load();
          "
        >
          下一页
        </button>
      </div>
    </section>
    <section class="panel import-detail">
      <template v-if="active"
        ><div class="panel-heading">
          <div>
            <h2>{{ active.name }}</h2>
            <p>{{ active.source || "未填写来源" }}</p>
          </div>
          <span class="badge" :class="active.status">{{
            labels[active.status]
          }}</span>
        </div>
        <div v-if="active.status === 'confirmed'" class="import-next">
          <span><Icon name="check" :size="18" />已生成审核项</span>
          <button class="button primary small" @click="go('entries', '', 'batch_id=' + active.id + '&status=pending')">审核这一批 <Icon name="arrow" :size="15" /></button>
        </div>
        <div
          v-if="['queued', 'processing'].includes(active.status)"
          class="loading-state"
        >
          <span class="spinner"></span>
          <h3>正在执行导入预检</h3>
        </div>
        <div v-if="active.status === 'failed'" class="content-pad">
          <p class="form-error">{{ active.job?.error }}</p>
          <button v-if="admin" class="button" :disabled="busy" @click="retry">
            重试导入
          </button>
        </div>
        <template v-if="active.items?.length"
          ><div class="import-summary">
            <div
              v-for="kind in [
                'new',
                'changed',
                'duplicate',
                'invalid',
                'renamed',
                'deleted',
              ]"
              :key="kind"
            >
              <strong>{{
                active.items.filter((i) => i.classification === kind).length
              }}</strong
              ><span>{{ labels[kind] }}</span>
            </div>
          </div>
          <details v-if="active.warnings?.length" class="validation-box">
            <summary>{{ active.warnings.length }} 条文件提示</summary>
            <p v-for="warning in active.warnings" :key="warning">
              {{ warning }}
            </p>
          </details>
          <p v-if="groupBlocked && active.status === 'preview'" class="validation-box error">关联变更组校验失败，请修正后重新上传。</p>
          <div
            v-if="active.status === 'preview' && admin"
            class="toolbar content-pad"
          >
            <label class="checkbox-label"
              ><input
                type="checkbox"
                :checked="
                  chosen.length === selectable.length && selectable.length > 0
                "
                @change="
                  chosen = ($event.target as HTMLInputElement).checked
                    ? selectable.map((i) => i.id)
                    : []
                "
              />选择全部有效项</label
            ><span class="muted">已选 {{ chosen.length }} 项</span
            ><button
              class="button primary"
              :disabled="!chosen.length || busy"
              @click="confirmOpen = true"
            >
              确认导入
            </button>
          </div>
          <div class="table-scroll">
            <table class="import-table">
              <thead>
                <tr>
                  <th v-if="admin && active.status === 'preview'"></th>
                  <th>文件 / 审核项</th>
                  <th>预检结果</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in active.items" :key="item.id">
                  <td v-if="admin && active.status === 'preview'">
                    <input
                      :checked="chosen.includes(item.id)"
                      @change="chooseItem(item.id, ($event.target as HTMLInputElement).checked)"
                      type="checkbox"
                      :aria-label="'选择 ' + item.path"
                      :disabled="
                        !!item.error || item.classification === 'duplicate' || (item.grouped && groupBlocked)
                      "
                    />
                  </td>
                  <td>
                    <strong class="path-label">{{ item.path }}</strong
                    ><small v-if="item.grouped" class="cell-note"
                      >关联变更组</small
                    >
                    <p v-if="item.error" class="form-error">{{ item.error }}</p>
                    <small
                      v-for="warning in item.warnings"
                      :key="warning"
                      class="cell-note"
                      >{{ warning }}</small
                    >
                  </td>
                  <td>
                    <span class="badge" :class="item.classification">{{
                      labels[item.classification]
                    }}</span>
                  </td>
                  <td>
                    <button
                      class="text-button"
                      @click="showItem(item.id)"
                    >
                      预览
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div></template
        >
        <div v-else-if="active.status === 'preview'" class="empty-state">
          <Icon name="file" :size="36" />
          <h3>没有可导入的知识正文</h3>
        </div>
        <div v-if="active.assets?.length && admin" class="source-assets">
          <h3>原始上传文件</h3>
          <DownloadLink
            v-for="asset in active.assets"
            :key="asset.id"
            :path="'/assets/' + asset.id + '/download'"
            :filename="asset.filename"
            >{{ asset.filename }}<Icon name="download" :size="15"
          /></DownloadLink></div
      ></template>
      <div v-else class="empty-state import-empty">
        <Icon name="upload" :size="36" />
        <h2>选择一个导入批次</h2>
        <button v-if="admin" class="button" @click="uploadOpen = true">
          上传待审核知识
        </button>
      </div>
    </section>
  </div>
  <Modal v-if="uploadOpen" title="导入待审核知识" @close="uploadOpen = false"
    ><form @submit.prevent="upload">
      <label
        >批次名称<input
          v-model="batchName"
          required
          maxlength="160"
          placeholder="批次名称"
      /></label>
      <div
        class="drop-zone"
        :class="{ dragging }"
        @dragover.prevent="dragging = true"
        @dragleave.prevent="dragging = false"
        @drop.prevent="
          dragging = false;
          pick($event.dataTransfer?.files || null);
        "
      >
        <Icon name="upload" :size="30" /><strong>{{
          files.length
            ? `已选择 ${files.length} 个文件`
            : "拖入知识库包或 Markdown 文件"
        }}</strong
        ><span>ZIP / Markdown / JSONL · 每批最多 30 MB</span>
        <div class="upload-options">
          <label class="button small"
            >选择文件<input
              type="file"
              multiple
              accept=".zip,.md,.jsonl"
              @change="
                pick(($event.target as HTMLInputElement).files)
              " /></label
          ><label class="button small"
            >选择目录<input
              type="file"
              webkitdirectory
              multiple
              @change="pick(($event.target as HTMLInputElement).files)"
          /></label>
        </div>
      </div>
      <p v-if="files.length" class="muted small-text">
        {{
          files
            .slice(0, 3)
            .map((f) => f.name)
            .join("、")
        }}{{ files.length > 3 ? " 等" : "" }}
      </p>
      <label
        >单独 Markdown 文件所属分类<select v-model="category">
          <option value="common">通用知识</option>
          <option value="streamer">主播档案</option>
          <option value="style">翻译风格</option>
          <option value="translation">翻译纠错</option></select
        ></label
      ><label
        >来源与备注<textarea
          v-model="source"
          rows="3"
          placeholder="节目期数 / 字幕文件 / 来源"
        ></textarea>
      </label>
      <div class="modal-actions">
        <button type="button" class="button" @click="uploadOpen = false">
          取消</button
        ><button class="button primary" :disabled="busy || !files.length">
          {{ busy ? "正在上传…" : "上传并预检" }}
        </button>
      </div>
    </form></Modal
  >
  <Modal v-if="preview" :title="preview.path" wide @close="preview = null"
    ><Diff :lines="preview.diff"
  /></Modal>
  <Modal v-if="confirmOpen" title="确认导入" @close="confirmOpen = false"
    ><p>
      导入 {{ chosen.length }} 项
    </p>
    <ul class="confirmation-list">
      <li
        v-for="item in active?.items?.filter((i) => chosen.includes(i.id))"
        :key="item.id"
      >
        {{ item.path
        }}<span class="badge" :class="item.classification">{{
          labels[item.classification]
        }}</span>
      </li>
    </ul>
    <div class="modal-actions">
      <button class="button" @click="confirmOpen = false">返回预检</button
      ><button class="button primary" :disabled="busy" @click="confirm">
        确认导入
      </button>
    </div></Modal
  >
</template>
