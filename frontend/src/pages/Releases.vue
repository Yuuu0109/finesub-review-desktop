<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { api, post, date, labels, type ReleaseCandidate, type Release } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import Modal from "../components/Modal.vue";
import DownloadLink from "../components/DownloadLink.vue";
import { toggleGroup } from "../workflow";
const { user, go, toast, refresh } = useApp();
const rows = ref<Release[]>([]),
  candidates = ref<ReleaseCandidate[]>([]),
  selected = ref<string[]>([]),
  busy = ref(false),
  publishOpen = ref(false),
  rollback = ref<Release | null>(null),
  name = ref(""),
  notes = ref("");
const admin = computed(() => user.value?.role === "admin");
const eligible = computed(() => candidates.value.filter(c => !c.blockers.length));
const processing = computed(() => rows.value.some(r => ['queued', 'processing'].includes(r.status)));
const loading = ref(false), error = ref('');
function selectCandidate(item: ReleaseCandidate, checked: boolean) {
  selected.value = toggleGroup(eligible.value, selected.value, item, checked);
}
let timer: ReturnType<typeof setInterval>;
async function load() {
  if (loading.value) return;
  loading.value = true; error.value = '';
  try {
    rows.value = await api("/releases");
    candidates.value = admin.value ? await api<ReleaseCandidate[]>("/releases/candidates") : [];
    selected.value = selected.value.filter((id) =>
      eligible.value.some((c) => c.id === id),
    );
  } catch (e) {
    error.value = (e as Error).message;
    toast(error.value, true);
  } finally { loading.value = false; }
}
function startPublish() {
  rollback.value = null;
  name.value = `知识库 · ${new Date().toLocaleDateString("zh-CN")}`;
  notes.value = "";
  publishOpen.value = true;
}
function startRollback(row: Release) {
  rollback.value = row;
  name.value = `回退至 ${row.name}`;
  notes.value = "";
  publishOpen.value = true;
}
async function publish() {
  busy.value = true;
  try {
    await post("/releases", {
      name: name.value,
      notes: notes.value,
      revision_ids: rollback.value ? [] : selected.value,
      rollback_id: rollback.value?.id || null,
    });
    publishOpen.value = false;
    selected.value = [];
    await load();
    await refresh();
    toast("发布任务已创建，正在生成并校验完整知识库");
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
async function retry(id: string | null) {
  if (!id) return;
  busy.value = true;
  try {
    await post("/jobs/" + id + "/retry");
    await load();
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  load();
  timer = setInterval(() => {
    if (rows.value.some((r) => ["queued", "processing"].includes(r.status)))
      load();
  }, 2500);
});
onUnmounted(() => clearInterval(timer));
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>发布版本</h1>
    </div>
    <button class="button" @click="load">
      <Icon name="refresh" :size="17" />刷新
    </button>
  </div>
  <p v-if="error" class="form-error" role="alert">{{ error }}</p>
  <p v-if="processing" class="callout" role="status">正在生成发布包…</p>
  <section v-if="admin" class="panel release-candidates">
    <div class="panel-heading">
      <div>
        <h2>
          待发布变更 <span class="count-chip">{{ candidates.length }}</span>
        </h2>
      </div>
      <button
        class="button primary"
        :disabled="!selected.length || busy || loading || processing || !!error"
        @click="startPublish"
      >
        <Icon name="plus" :size="17" />创建发布 · {{ selected.length }}
      </button>
    </div>
    <div v-if="candidates.length" class="content-pad">
      <label class="checkbox-label"
        ><input
          type="checkbox"
          :checked="eligible.length > 0 && selected.length === eligible.length"
          :disabled="!eligible.length || busy || loading"
          @change="
            selected = ($event.target as HTMLInputElement).checked
              ? eligible.map((c) => c.id)
              : []
          "
        />选择全部可发布变更（{{ eligible.length }}）</label
      >
      <div class="candidate-grid">
        <div v-for="item in candidates" :key="item.id" class="candidate-item" :class="{ blocked: item.blockers.length }"
          ><input :checked="selected.includes(item.id)" :disabled="!!item.blockers.length || busy || loading" :aria-label="'选择 ' + item.title" @change="selectCandidate(item, ($event.target as HTMLInputElement).checked)" type="checkbox" /><span
            ><strong>{{ item.title }}</strong
            ><small
              >{{ labels[item.category] }} · v{{ item.number
              }}<span v-if="item.group_id"> · 关联变更组</span></small
            ></span
          ><span
            class="badge"
            :class="item.operation === 'delete' ? 'rejected' : 'approved'"
            >{{ item.operation === "delete" ? "拟删除" : "已通过" }}</span
          ><div v-if="item.blockers.length" class="candidate-blockers"><p v-for="reason in item.blockers" :key="reason">{{ reason }}</p>
            <button class="text-button" @click="go('review', item.entry_id, item.group_id ? 'group_id=' + item.group_id : '')">处理此项 <Icon name="arrow" :size="14" /></button>
            <button v-for="related in item.related.filter(r => r.status !== 'approved')" :key="related.id" class="text-button" @click="go('review', related.entry_id, 'group_id=' + item.group_id)">审核 {{ related.title }}</button>
          </div></div
        >
      </div>
    </div>
    <div v-else class="empty-small">
      {{ loading ? '正在检查发布条件…' : '暂无待发布变更' }}
      <button v-if="!loading" class="text-button" @click="go('entries', 'pending')">前往审核</button>
    </div>
  </section>
  <div class="section-heading">
    <h2>历史发布</h2>
    <span class="muted">{{ rows.length }} 个版本</span>
  </div>
  <div v-if="!rows.length" class="panel empty-state">
    <Icon name="download" :size="36" />
    <h3>还没有发布版本</h3>
  </div>
  <article v-for="row in rows" :key="row.id" class="panel release-card">
    <div class="release-icon">
      <Icon :name="row.rollback_of ? 'refresh' : 'book'" :size="24" />
    </div>
    <div class="release-body">
      <div class="release-title">
        <h2>{{ row.name }}</h2>
        <span class="badge" :class="row.status">{{ labels[row.status] }}</span
        ><span v-if="row.current" class="tag current">当前正式版本</span>
      </div>
      <p v-if="row.notes">{{ row.notes }}</p>
      <div class="release-meta">
        <span>{{ date(row.created_at) }}</span
        ><span>{{ row.count }} 个知识记录</span
        ><span v-if="row.report.files">{{ row.report.files }} 个导出文件</span
        ><code>{{ row.id.slice(0, 8) }}</code>
      </div>
      <div v-if="row.report.errors?.length" class="form-error">
        <p v-for="error in row.report.errors" :key="error">{{ error }}</p>
      </div>
      <p v-if="row.report.native_import_requires_compat_tool" class="small-text muted">
        导入方式：专用兼容工具
      </p>
      <details v-if="row.report.warnings?.length" class="release-warnings">
        <summary>{{ row.report.warnings.length }} 项兼容提示</summary>
        <p v-for="warning in row.report.warnings" :key="warning">
          {{ warning }}
        </p>
      </details>
    </div>
    <div v-if="admin" class="release-actions">
      <template v-if="row.status === 'ready'"
        ><DownloadLink
          class="button primary small"
          :path="'/releases/' + row.id + '/download'"
          ><Icon name="download" :size="15" />{{
            row.report.native_import_requires_compat_tool
              ? "兼容知识库包"
              : "知识库包"
          }}</DownloadLink
        ><DownloadLink
          class="button small"
          :path="'/releases/' + row.id + '/download?archive=true'"
          >审核归档</DownloadLink
        ><button
          v-if="!row.current"
          class="text-button"
          @click="startRollback(row)"
        >
          回退到此版本
        </button></template
      ><template v-else-if="row.status === 'failed'"
        ><button
          class="button"
          :disabled="busy || !row.job_id"
          @click="retry(row.job_id)"
        >
          重试生成</button
        ><span v-if="!row.job_id" class="muted small-text">任务记录缺失</span></template
      ><span v-else class="muted">正在生成…</span>
    </div>
  </article>
  <Modal
    v-if="publishOpen"
    :title="rollback ? '回退正式知识库' : '创建发布版本'"
    @close="!busy && (publishOpen = false)"
    ><form @submit.prevent="publish">
      <div v-if="rollback" class="callout">
        正式知识库将恢复为「{{ rollback.name }}」的完整内容。
      </div>
      <label>版本名称<input v-model="name" required maxlength="120" /></label
      ><label
        >发布说明<textarea
          v-model="notes"
          :required="!!rollback"
          rows="3"
          :placeholder="rollback ? '回退原因' : '变更内容'"
        ></textarea>
      </label>
      <ul v-if="!rollback" class="confirmation-list">
        <li
          v-for="item in candidates.filter((c) => selected.includes(c.id))"
          :key="item.id"
        >
          {{ item.path }}<span>v{{ item.number }}</span>
        </li>
      </ul>
      <div class="modal-actions">
        <button type="button" class="button" @click="publishOpen = false">
          取消</button
        ><button class="button primary" :disabled="busy">
          {{ rollback ? "确认回退并生成版本" : "确认并生成发布包" }}
        </button>
      </div>
    </form></Modal
  >
</template>
