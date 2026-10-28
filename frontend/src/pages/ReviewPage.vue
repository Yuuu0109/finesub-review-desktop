<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, post, date, labels, type Detail, type RecordItem } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import Markdown from "../components/Markdown.vue";
import Diff from "../components/Diff.vue";
import Modal from "../components/Modal.vue";
import { nextParams, queueParams, readDraft, writeDraft, removeDraft } from "../workflow";
const props = defineProps<{ entryId: string; queueQuery?: string }>();
const { user, people, go, toast, refresh } = useApp();
const detail = ref<Detail | null>(null),
  loading = ref(true),
  busy = ref(false),
  tab = ref("read"),
  showHistory = ref(false);
const compareTo = ref<"previous" | "published">("previous");
const queueFinished = ref(false), nextBusy = ref(false), continueReview = ref(true), draftWarning = ref(false);
const queue = queueParams(props.queueQuery || "status=pending");
let queueAnchor = "";
const form = ref({
    verdict: "changes",
    body: "",
    quote: "",
    suggestion: "",
    evidence: "",
  }),
  decisionOpen = ref(false),
  decisionStatus = ref("approved"),
  decisionReason = ref("");
const editContent = ref(""),
  editBaseRevision = ref(""),
  editSource = ref(""),
  deleteEntry = ref(false),
  replyTo = ref<RecordItem | null>(null),
  replyText = ref("");
const resolveTarget = ref<RecordItem | null>(null),
  resolution = ref("accepted"),
  resolutionNote = ref("");
const admin = computed(() => user.value?.role === "admin"),
  isCurrent = computed(
    () => detail.value?.entry.id === detail.value?.entry.current_revision,
  );
const visibleRecords = computed(
  () =>
    detail.value?.reviews.filter(
      (r) => showHistory.value || r.revision_id === detail.value?.entry.id,
    ) || [],
);
const roots = computed(() => visibleRecords.value.filter((r) => !r.parent_id));
const currentRecords = computed(
  () =>
    detail.value?.reviews.filter(
      (r) => r.revision_id === detail.value?.entry.id && r.verdict !== "reply",
    ) || [],
);
const latestDecision = computed(() =>
  detail.value?.decisions.find((d) => d.revision_id === detail.value?.entry.id),
);
const draftKey = computed(
  () => `finesub-draft:${user.value?.id}:${detail.value?.entry.id}`,
);
const editDraftKey = computed(
  () => `finesub-edit:${user.value?.id}:${props.entryId}`,
);
const editStale = computed(() => editBaseRevision.value !== detail.value?.entry.id);
const editDirty = computed(() => !!detail.value && isCurrent.value && (
  editContent.value !== detail.value.entry.content || editSource.value !== detail.value.entry.source ||
  deleteEntry.value !== (detail.value.entry.operation === "delete")
));
const diffCounts = computed(() => ({
  added: detail.value?.diff.filter(l => l.startsWith('+') && !l.startsWith('+++')).length || 0,
  removed: detail.value?.diff.filter(l => l.startsWith('-') && !l.startsWith('---')).length || 0,
}));
const decisionDraftKey = computed(() => `finesub-decision:${user.value?.id}:${detail.value?.entry.id}`);
// 草稿按 400ms 合并写入：大词条一次按键就 JSON.stringify 2MB 并同步写 localStorage 会卡顿。
const pendingDrafts = new Map<string, unknown>();
const writtenDrafts = new Map<string, string>();
let draftTimer: ReturnType<typeof setTimeout> | null = null;
function writeNow(key: string, value: unknown): boolean {
  pendingDrafts.delete(key);
  if (!pendingDrafts.size && draftTimer) { clearTimeout(draftTimer); draftTimer = null; }
  const serialized = JSON.stringify(value);
  if (writtenDrafts.get(key) === serialized) return true;
  const ok = writeDraft(key, value);
  if (ok) writtenDrafts.set(key, serialized);
  else writtenDrafts.delete(key);
  return ok;
}
function flushDrafts() {
  if (draftTimer) { clearTimeout(draftTimer); draftTimer = null; }
  if (!pendingDrafts.size) return;
  const queued = [...pendingDrafts];
  pendingDrafts.clear();
  for (const [key, value] of queued) if (!writeNow(key, value)) draftWarning.value = true;
}
function persist(key: string, value: unknown, immediate = false) {
  if (immediate) { if (!writeNow(key, value)) draftWarning.value = true; return; }
  pendingDrafts.set(key, value);
  if (draftTimer) clearTimeout(draftTimer);
  draftTimer = setTimeout(flushDrafts, 400);
}
function dropDraft(key: string) {
  pendingDrafts.delete(key);
  writtenDrafts.delete(key);
  removeDraft(key);
}
function backToQueue() { go(queue.get("mine") === "true" ? "mine" : "entries", "", queue.toString()); }
function discardEdit() {
  if (!detail.value || !window.confirm("丢弃未保存的正文修订，恢复当前版本？")) return;
  restoring = true;
  editContent.value = detail.value.entry.content; editSource.value = detail.value.entry.source;
  editBaseRevision.value = detail.value.entry.id; deleteEntry.value = detail.value.entry.operation === "delete";
  dropDraft(editDraftKey.value);
  setTimeout(() => (restoring = false), 0);
}
let restoring = false;
let sequence = 0;
async function load(version = "") {
  const request = ++sequence;
  loading.value = true;
  try {
    const d = await api<Detail>(
      `/entries/${props.entryId}?${new URLSearchParams({ revision_id: version, compare_to: compareTo.value })}`,
    );
    if (request !== sequence) return;
    if (!queueAnchor) { queueAnchor = d.entry.id; tab.value = d.entry.number > 1 ? "diff" : "read"; }
    restoring = true;
    detail.value = d;
    editContent.value = d.entry.content;
    editBaseRevision.value = d.entry.id;
    editSource.value = d.entry.source;
    deleteEntry.value = d.entry.operation === "delete";
    const saved = d.entry.id === d.entry.current_revision ? readDraft<{ content: string; baseRevision: string; source: string; deleteEntry: boolean }>(editDraftKey.value) : null;
    if (saved && typeof saved.content === "string" && typeof saved.source === "string") {
        editContent.value = saved.content;
        editBaseRevision.value = saved.baseRevision || d.entry.id;
        editSource.value = saved.source;
        deleteEntry.value = !!saved.deleteEntry;
        // Preserve older tab-session drafts in browser persistent storage.
        persist(editDraftKey.value, saved, true);
    }
    const draft = readDraft<typeof form.value>(draftKey.value);
    form.value = draft
      && ["approve", "changes", "reject", "evidence"].includes(draft.verdict)
      && [draft.body, draft.quote, draft.suggestion, draft.evidence].every(v => typeof v === "string")
      ? draft
      : {
          verdict: "changes",
          body: "",
          quote: "",
          suggestion: "",
          evidence: "",
        };
    if (draft) persist(draftKey.value, form.value, true);
    const decisionDraft = readDraft<{ status: string; reason: string }>(decisionDraftKey.value);
    decisionReason.value = typeof decisionDraft?.reason === "string" ? decisionDraft.reason : "";
    decisionStatus.value = ["approved", "needs_changes", "rejected", "pending"].includes(decisionDraft?.status || "") ? decisionDraft!.status : "approved";
    setTimeout(() => (restoring = false), 0);
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    if (request === sequence) loading.value = false;
  }
}
watch(
  [editContent, editSource, deleteEntry, editBaseRevision],
  () => {
    if (!restoring && detail.value && isCurrent.value)
      persist(editDraftKey.value, {
        content: editContent.value, baseRevision: editBaseRevision.value, source: editSource.value, deleteEntry: deleteEntry.value,
      });
  },
);
watch(
  form,
  () => {
    if (!restoring && detail.value)
      persist(draftKey.value, form.value);
  },
  { deep: true },
);
watch([decisionReason, decisionStatus], () => {
  if (!restoring && detail.value && admin.value) persist(decisionDraftKey.value, { reason: decisionReason.value, status: decisionStatus.value });
});
async function act(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
    await refresh();
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
function submitRecord(andNext = false) {
  if (!detail.value) return;
  act(async () => {
    const rid = detail.value!.entry.id;
    await post(`/entries/${props.entryId}/reviews`, {
      ...form.value,
      revision_id: rid,
    });
    dropDraft(draftKey.value);
    await load(rid);
    toast("审核记录已提交");
    if (andNext) await next();
  });
}
function submitDecision() {
  if (editDirty.value || !isCurrent.value) return;
  act(async () => {
    await post(`/entries/${props.entryId}/decision`, {
      revision_id: detail.value!.entry.id,
      status: decisionStatus.value,
      reason: decisionReason.value,
    });
    decisionOpen.value = false;
    dropDraft(decisionDraftKey.value);
    await load();
    toast("管理员决定已保存");
    if (continueReview.value) await next();
  });
}
function saveRevision() {
  act(async () => {
    await post(`/entries/${props.entryId}/revisions`, {
      base_revision: editBaseRevision.value,
      content: editContent.value,
      source: editSource.value,
      operation: deleteEntry.value ? "delete" : "upsert",
    });
    dropDraft(editDraftKey.value);
    tab.value = "diff";
    await load();
    toast("新版本已保存");
  });
}
function submitReply() {
  act(async () => {
    await post(`/entries/${props.entryId}/reviews`, {
      revision_id: replyTo.value!.revision_id,
      parent_id: replyTo.value!.id,
      verdict: "reply",
      body: replyText.value,
    });
    replyTo.value = null;
    replyText.value = "";
    await load(detail.value!.entry.id);
    toast("回复已保存");
  });
}
function resolve() {
  act(async () => {
    await api(`/reviews/${resolveTarget.value!.id}`, {
      method: "PATCH",
      body: JSON.stringify({
        resolution: resolution.value,
        note: resolutionNote.value,
      }),
    });
    resolveTarget.value = null;
    resolutionNote.value = "";
    await load(detail.value!.entry.id);
    toast("意见处理结果已保存");
  });
}
function assign(id: string | null) {
  act(async () => {
    await api(`/entries/${props.entryId}/assignee`, {
      method: "PUT",
      body: JSON.stringify({ user_id: id }),
    });
    await load(detail.value!.entry.id);
    toast(id ? "审核任务已分配" : "已取消认领");
  });
}
function quoteSelection() {
  const selection = window.getSelection()?.toString().trim();
  if (selection && detail.value?.entry.content.includes(selection)) {
    form.value.quote = selection;
    toast("已引用所选文字");
    document.getElementById("review-body")?.focus();
  } else toast("请先选中原文", true);
}
async function next() {
  if (nextBusy.value) return;
  nextBusy.value = true;
  try {
    const data = await api<{ items: { entry_id: string }[] }>("/entries?" + nextParams(queue.toString(), queueAnchor));
    const other = data.items[0];
    if (other) go("review", other.entry_id, queue.toString());
    else queueFinished.value = true;
  } catch (e) {
    toast((e as Error).message, true);
  } finally { nextBusy.value = false; }
}
async function changeComparison() {
  if (!detail.value) return;
  // Refresh only the diff: comparison must never reset an in-progress draft.
  const id = detail.value.entry.id;
  const requested = compareTo.value;
  try {
    const d = await api<Detail>(`/entries/${props.entryId}?revision_id=${id}&compare_to=${requested}`);
    if (detail.value?.entry.id === id && compareTo.value === requested) {
      detail.value.diff = d.diff; detail.value.comparison = d.comparison;
    }
  } catch (e) { toast((e as Error).message, true); }
}
function saveBeforeUnload() {
  flushDrafts();
  if (!detail.value) return;
  // Save synchronously on tab close/reload, without requesting a leave prompt.
  const ok = writeNow(draftKey.value, form.value);
  const editOk = !editDirty.value || writeNow(editDraftKey.value, { content: editContent.value, source: editSource.value, baseRevision: editBaseRevision.value, deleteEntry: deleteEntry.value });
  const decisionOk = !admin.value || writeNow(decisionDraftKey.value, { reason: decisionReason.value, status: decisionStatus.value });
  if (!ok || !editOk || !decisionOk) draftWarning.value = true;
}
onMounted(() => { load(); window.addEventListener('beforeunload', saveBeforeUnload); });
onUnmounted(() => {
  flushDrafts();
  sequence++;
  window.removeEventListener('beforeunload', saveBeforeUnload);
});
</script>
<template>
  <div v-if="loading && !detail" class="loading-state">
    <span class="spinner"></span>正在读取词条
  </div>
  <template v-if="detail"
    ><div class="review-top">
      <button class="text-button muted" @click="backToQueue">
        <Icon name="back" :size="16" />返回审核队列
      </button>
      <div class="review-top-actions">
        <span class="muted"
          >{{ labels[detail.entry.category] }} /
          {{ detail.entry.path.split("/").pop() }}</span
        ><button class="button small" :disabled="loading || nextBusy || busy" @click="next">
          下一条<Icon name="arrow" :size="15" />
        </button>
      </div>
    </div>
    <div class="review-heading">
      <div>
        <div class="inline-meta">
          <span class="eyebrow">{{ labels[detail.entry.category] }}</span
          ><span v-if="detail.entry.kind" class="tag">{{
            detail.entry.kind
          }}</span
          ><span class="tag">v{{ detail.entry.number }}</span
          ><span v-if="!isCurrent" class="badge needs_changes">历史版本</span
          ><span
            v-if="detail.entry.operation === 'delete'"
            class="badge rejected"
            >拟删除</span
          >
        </div>
        <h1>{{ detail.entry.title }}</h1>
        <p>{{ detail.entry.summary }}</p>
      </div>
      <span class="badge large" :class="detail.entry.status">{{
        labels[detail.entry.status]
      }}</span>
    </div>
    <div v-if="queueFinished" class="queue-complete" role="status">
      <Icon name="check" :size="20" /><strong>已到当前队列末尾</strong>
      <button class="button small" @click="backToQueue">返回队列</button>
      <button v-if="admin" class="button primary small" @click="go('releases')">前往发布</button>
    </div>
    <p v-if="draftWarning" class="form-error" role="alert">草稿保存失败，请复制未保存内容。</p>
    <div class="review-stage" aria-label="当前版本进度">
      <span><i></i>候选 v{{ detail.entry.number }}</span>
      <Icon name="chevron" :size="14" />
      <span :class="detail.entry.status"><i></i>{{ labels[detail.entry.status] }}</span>
      <Icon name="chevron" :size="14" />
      <span :class="{ approved: detail.entry.published }"><i></i>{{ detail.entry.published ? '已发布' : '尚未发布' }}</span>
      <span v-if="editDirty" class="draft-indicator">{{ draftWarning ? '修订草稿未保存' : '修订草稿已保留' }}</span>
    </div>
      <details class="review-metadata"><summary>来源、负责人和版本历史</summary><div class="review-directory">
        <div class="directory-section">
          <h3>词条信息</h3>
          <dl>
            <dt>当前版本</dt>
            <dd>v{{ detail.entry.number }}</dd>
            <dt>更新时间</dt>
            <dd>{{ date(detail.entry.created_at) }}</dd>
            <dt>发布状态</dt>
            <dd>
              {{ detail.entry.published ? "已进入当前发布" : "尚未发布" }}
            </dd>
          </dl>
        </div>
        <div class="directory-section">
          <h3>审核负责人</h3>
          <select
            v-if="admin"
            :value="detail.entry.assignee_id || ''"
            aria-label="分配审核负责人"
            :disabled="busy"
            @change="assign(($event.target as HTMLSelectElement).value || null)"
          >
            <option value="">未分配</option>
            <option
              v-for="person in people.filter((p) => p.active)"
              :key="person.id"
              :value="person.id"
            >
              {{ person.name }}
            </option></select
          ><template v-else
            ><p>
              {{
                people.find((p) => p.id === detail?.entry.assignee_id)?.name ||
                "未分配"
              }}
            </p>
            <button
              v-if="
                !detail.entry.assignee_id ||
                detail.entry.assignee_id === user?.id
              "
              class="button small full"
              :disabled="busy"
              @click="assign(detail.entry.assignee_id ? null : user!.id)"
            >
              {{ detail.entry.assignee_id ? "取消认领" : "认领此词条" }}
            </button></template
          >
        </div>
        <div class="directory-section">
          <h3>版本历史</h3>
          <button
            v-for="version in detail.versions"
            :key="version.id"
            class="version-item"
            :class="{ selected: version.id === detail.entry.id }"
            @click="
              tab = 'read';
              load(version.id);
            "
          >
            <span
              >v{{ version.number
              }}<small>{{ date(version.created_at) }}</small></span
            ><span class="version-dot" :class="version.status"></span>
          </button>
        </div>
        <div class="directory-section">
          <h3>来源</h3>
          <p class="source-text">{{ detail.entry.source || "尚未补充来源" }}</p>
        </div>
      </div></details>
    <div class="review-grid">

      <section class="document-panel panel">
        <div class="document-toolbar">
          <div class="tabs">
            <button :class="{ active: tab === 'read' }" @click="tab = 'read'">
              正文</button
            ><button :class="{ active: tab === 'diff' }" @click="tab = 'diff'">
              版本差异</button
            ><button :class="{ active: tab === 'raw' }" @click="tab = 'raw'">
              原文</button
            ><button
              v-if="admin && isCurrent"
              :class="{ active: tab === 'edit' }"
              @click="tab = 'edit'"
            >
              修订
            </button>
          </div>
          <button
            v-if="tab === 'read'"
            class="icon-button"
            aria-label="引用所选文字"
            title="引用所选文字"
            @mousedown.prevent
            @click="quoteSelection"
          >
            <Icon name="message" :size="17" />
          </button>
        </div>
        <div v-if="tab === 'diff'" class="diff-summary">
          <label>对比<select v-model="compareTo" aria-label="对比版本" :disabled="loading" @change="changeComparison"><option value="previous">上一版本</option><option value="published">当前正式版本</option></select></label>
          <span>{{ detail.comparison.number ? `v${detail.comparison.number}` : '无基线' }} → v{{ detail.entry.number }}</span>
          <span class="diff-totals"><b class="green-text">+{{ diffCounts.added }}</b><b class="red-text">−{{ diffCounts.removed }}</b></span>
        </div>
        <div v-if="editDirty && tab !== 'edit'" class="draft-banner">
          <span>正文修订未保存</span><button class="text-button" @click="tab = 'edit'">继续修订</button>
        </div>
        <div v-if="detail.errors.length" class="validation-box error">
          <strong>格式错误</strong>
          <p v-for="item in detail.errors" :key="item">{{ item }}</p>
        </div>
        <details v-if="detail.warnings.length" class="validation-box">
          <summary>{{ detail.warnings.length }} 项核实提示</summary>
          <p v-for="item in detail.warnings" :key="item">{{ item }}</p>
        </details>
        <Markdown v-if="tab === 'read'" :text="detail.entry.content" /><Diff
          v-else-if="tab === 'diff'"
          :lines="detail.diff"
        />
        <pre v-else-if="tab === 'raw'" class="raw-document">{{
          detail.entry.content
        }}</pre>
        <form v-else class="edit-form" @submit.prevent="saveRevision">
          <div v-if="editStale" class="callout">
            草稿版本已过期，请合并最新正文。
            <button type="button" class="text-button" @click="editBaseRevision = detail.entry.id">
              已合并，继续修订
            </button>
          </div>
          <label
            >Markdown 正文<textarea
              v-model="editContent"
              class="code-editor"
              spellcheck="false"
              rows="24"
            ></textarea></label
          ><label
            >来源说明<textarea
              v-model="editSource"
              rows="2"
              placeholder="节目期数、时间码或参考来源"
            ></textarea></label
          ><label class="checkbox-label"
            ><input
              v-model="deleteEntry"
              type="checkbox"
            />删除提案</label
          >
          <p v-if="deleteEntry" class="form-error">
            通过并发布后删除此词条。
          </p>
          <div class="heading-actions"><button class="button primary" :disabled="busy || editStale || !editDirty">保存为新版本</button>
          <button type="button" class="button" :disabled="busy" @click="discardEdit">丢弃草稿</button></div>
        </form>
      </section>
      <aside class="review-sidebar">
        <section class="decision-panel">
          <div class="section-label">
            <Icon name="shield" :size="17" />
            <h2>{{ admin ? '版本裁决' : '管理员决定' }}</h2>
          </div>
          <div class="decision-status">
            <span class="badge" :class="detail.entry.status">{{
              labels[detail.entry.status]
            }}</span
            ><span v-if="detail.entry.published" class="muted">已发布</span>
          </div>
          <p v-if="latestDecision" class="decision-reason">
            {{ latestDecision.reason
            }}<small
              >{{ latestDecision.author }} ·
              {{ date(latestDecision.created_at) }}</small
            >
          </p>
          <button
            v-if="admin && isCurrent && !detail.entry.published"
            class="button primary full"
            :disabled="busy || loading || editDirty"
            @click="decisionOpen = true"
          >
            作出决定<Icon name="arrow" :size="16" />
          </button>
          <p v-if="admin && editDirty" class="small-text muted">正文修订未保存</p>
          <button v-if="admin && detail.entry.status === 'approved' && !detail.entry.published" class="button full" @click="go('releases')">前往发布 <Icon name="arrow" :size="16" /></button>
          <div v-if="detail.related.length" class="related-entries">
            <h3>关联变更 · {{ detail.related.length + 1 }}</h3>
            <button v-for="item in detail.related" :key="item.id" class="text-button" @click="go('review', item.entry_id, 'group_id=' + detail.entry.group_id)"><span>{{ item.title }}</span><small>{{ labels[item.status] }}</small></button>
          </div>
        </section>
        <section class="records-panel">
          <div class="section-label">
            <Icon name="message" :size="17" />
            <h2>成员审核记录</h2>
            <span class="count-chip">{{ currentRecords.length }}</span>
          </div>
          <label class="checkbox-label history-toggle"
            ><input
              v-model="showHistory"
              type="checkbox"
            />包含其他版本的记录</label
          >
          <div v-if="!roots.length" class="record-empty">
            暂无审核记录
          </div>
          <article v-for="record in roots" :key="record.id" class="record">
            <div class="record-header">
              <span class="avatar small">{{ record.author.slice(0, 1) }}</span
              ><strong>{{ record.author }}</strong
              ><small>{{ date(record.created_at) }}</small>
            </div>
            <div class="record-tags">
              <span class="opinion-label" :class="record.verdict">{{
                labels[record.verdict]
              }}</span
              ><span v-if="record.stale" class="tag">历史版本</span
              ><span v-if="record.resolution !== 'open'" class="tag">{{
                labels[record.resolution]
              }}</span>
            </div>
            <blockquote v-if="record.quote">{{ record.quote }}</blockquote>
            <p class="preserve-lines">{{ record.body }}</p>
            <div v-if="record.suggestion" class="record-evidence">
              <strong>建议文本</strong>
              <p>{{ record.suggestion }}</p>
            </div>
            <div v-if="record.evidence" class="record-evidence">
              <strong>依据</strong>
              <p>{{ record.evidence }}</p>
            </div>
            <div v-if="record.resolution_note" class="resolution-note">
              <strong>管理员处理说明</strong>
              <p>{{ record.resolution_note }}</p>
            </div>
            <div class="record-actions">
              <button class="text-button" @click="replyTo = record">回复</button
              ><button
                v-if="admin"
                class="text-button"
                @click="
                  resolveTarget = record;
                  resolution =
                    record.resolution === 'open'
                      ? 'accepted'
                      : record.resolution;
                  resolutionNote = record.resolution_note;
                "
              >
                处理意见
              </button>
            </div>
            <div
              v-for="reply in visibleRecords.filter(
                (r) => r.parent_id === record.id,
              )"
              :key="reply.id"
              class="reply"
            >
              <strong>{{ reply.author }}</strong>
              <p>{{ reply.body }}</p>
              <small>{{ date(reply.created_at) }}</small>
            </div>
          </article>
          <form class="record-form" @submit.prevent="submitRecord()">
            <h3>提交审核记录</h3>
            <label
              >你的判断<select v-model="form.verdict">
                <option value="approve">建议通过</option>
                <option value="changes">建议修改</option>
                <option value="reject">建议驳回</option>
                <option value="evidence">证据不足</option>
              </select></label
            ><label
              >意见说明<textarea
                id="review-body"
                v-model="form.body"
                rows="4"
                required
                maxlength="15000"
                placeholder="审核意见"
              ></textarea>
            </label>
            <details :open="!!form.quote">
              <summary>引用原文与建议文本</summary>
              <label
                >引用原文<textarea
                  v-model="form.quote"
                  rows="2"
                  placeholder="引用原文"
                ></textarea></label
              ><label
                >建议替换为<textarea
                  v-model="form.suggestion"
                  rows="3"
                  placeholder="建议文本"
                ></textarea>
              </label>
            </details>
            <label
              >依据（选填）<textarea
                v-model="form.evidence"
                rows="2"
                placeholder="节目期数 / 时间码 / 来源链接"
              ></textarea>
            </label>
            <div class="record-submit">
              <small>v{{ detail.entry.number }}</small>
              <button class="button" :disabled="busy || !form.body.trim()">提交记录</button>
              <button type="button" class="button primary"
                :disabled="busy || !form.body.trim()"
                @click="submitRecord(true)"
              >
                提交并继续
              </button>
            </div>
          </form>
        </section>
      </aside>
    </div></template
  >
  <div v-else-if="!loading" class="empty-state">
    <Icon name="file" :size="36" />
    <h2>无法读取词条</h2>
    <button class="button" @click="load()">重试</button>
  </div>
  <Modal
    v-if="decisionOpen"
    title="管理员最终决定"
    @close="!busy && (decisionOpen = false)"
    ><form @submit.prevent="submitDecision">
      <p class="callout">
        {{ detail?.entry.title }} · v{{ detail?.entry.number }}
      </p>
      <label
        >决定<select v-model="decisionStatus">
          <option value="approved" :disabled="!!detail?.errors.length">通过，等待发布</option>
          <option value="needs_changes">待补充 / 需修改</option>
          <option value="rejected">驳回</option>
          <option value="pending">重新进入待裁决</option>
        </select></label
      ><label
        >决定理由<textarea
          v-model="decisionReason"
          required
          rows="4"
          placeholder="决定理由"
        ></textarea>
      </label>
      <p v-if="detail?.errors.length" class="form-error">格式校验未通过</p>
      <label class="checkbox-label"><input v-model="continueReview" type="checkbox" />继续审核下一条</label>
      <div class="modal-actions">
        <button type="button" class="button" @click="decisionOpen = false">
          取消</button
        ><button class="button primary" :disabled="busy || !decisionReason.trim() || (decisionStatus === 'approved' && !!detail?.errors.length)">{{ continueReview ? '确认并继续' : '确认决定' }}</button>
      </div>
    </form></Modal
  >
  <Modal v-if="replyTo" title="回复审核记录" @close="replyTo = null"
    ><form @submit.prevent="submitReply">
      <blockquote>{{ replyTo.body }}</blockquote>
      <label
        >回复内容<textarea
          v-model="replyText"
          required
          rows="4"
          placeholder="回复内容"
        ></textarea>
      </label>
      <div class="modal-actions">
        <button class="button primary" :disabled="busy">保存回复</button>
      </div>
    </form></Modal
  >
  <Modal v-if="resolveTarget" title="处理成员意见" @close="resolveTarget = null"
    ><form @submit.prevent="resolve">
      <blockquote>{{ resolveTarget.body }}</blockquote>
      <label
        >处理结果<select v-model="resolution">
          <option value="accepted">已采纳</option>
          <option value="partial">部分采纳</option>
          <option value="declined">不采纳</option>
          <option value="more_evidence">待补充依据</option>
          <option value="open">重新打开</option>
        </select></label
      ><label
        >处理说明<textarea
          v-model="resolutionNote"
          required
          rows="4"
          placeholder="处理理由"
        ></textarea>
      </label>
      <div class="modal-actions">
        <button class="button primary" :disabled="busy">保存处理结果</button>
      </div>
    </form></Modal
  >
</template>

