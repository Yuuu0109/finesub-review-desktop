<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import { api, date, labels, type Entry } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import { queueParams } from "../workflow";
const props = defineProps<{
  mine?: boolean;
  search?: string;
  initialStatus?: string;
  initialQuery?: string;
}>();
const { go, toast, people, user } = useApp();
const initial = queueParams(props.initialQuery);
const rows = ref<Entry[]>([]),
  query = ref(initial.get("q") || props.search || ""),
  status = ref(
    ["pending", "needs_changes", "approved", "rejected"].includes(
      initial.get("status") || props.initialStatus || "",
    )
      ? initial.get("status") || props.initialStatus!
      : "",
  ),
  category = ref(initial.get("category") || ""),
  reviewed = ref(initial.get("reviewed") || ""),
  page = ref(Math.max(1, Number(initial.get("page")) || 1)),
  total = ref(0),
  loading = ref(false);
let sequence = 0;
function params() {
  const p = new URLSearchParams(initial);
  for (const [key, value] of Object.entries({ q: query.value, status: status.value,
    category: category.value, reviewed: reviewed.value, mine: props.mine || initial.get("mine") === "true" ? "true" : "", page: String(page.value) })) {
    if (value) p.set(key, value); else p.delete(key);
  }
  return p;
}
function openEntry(entry: Entry) { go("review", entry.entry_id, params().toString()); }
function resetFilters() {
  query.value = ""; status.value = ""; category.value = ""; reviewed.value = "";
  for (const key of ["batch_id", "group_id", "mine"]) initial.delete(key);
  filter();
}
async function load() {
  const current = ++sequence;
  loading.value = true;
  try {
    const p = params();
    const data = await api("/entries?" + p);
    if (current === sequence) {
      rows.value = data.items;
      total.value = data.total;
      history.replaceState(null, "", `#/${props.mine ? "mine" : "entries"}?${p}`);
    }
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    if (current === sequence) loading.value = false;
  }
}
function filter() {
  page.value = 1;
  load();
}
watch(
  () => props.search,
  (v) => {
    query.value = v || "";
    filter();
  },
);
onMounted(load);
onUnmounted(() => { sequence++; });
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>{{ mine ? "我的审核" : "知识库" }}</h1>
      <p v-if="initial.get('batch_id')">当前导入批次</p>
      <p v-else-if="initial.get('group_id')">关联变更组</p>
    </div>
    <div class="heading-actions">
    <button v-if="rows.length" class="button primary" :disabled="loading" @click="openEntry(rows[0]!)">
      <Icon name="arrow" :size="17" />开始审核
    </button>
    <button class="button" :disabled="loading" @click="load">
      <Icon name="refresh" :size="17" />刷新
    </button>
    </div>
  </div>
  <div class="queue-tabs" aria-label="快捷审核筛选">
    <button :class="{ active: !status && !reviewed }" @click="status = ''; reviewed = ''; filter()">全部</button>
    <button :class="{ active: status === 'pending' && !reviewed }" @click="status = 'pending'; reviewed = ''; filter()">待裁决</button>
    <button :class="{ active: reviewed === 'no' }" @click="status = 'pending'; reviewed = 'no'; filter()">我未审核</button>
    <button :class="{ active: status === 'needs_changes' }" @click="status = 'needs_changes'; reviewed = ''; filter()">待补充</button>
    <span>{{ total }} 条</span>
    <button v-if="user?.role === 'admin'" class="queue-publish" @click="go('releases')">发布已通过知识 <Icon name="arrow" :size="14" /></button>
  </div>
  <section class="panel">
    <form class="filter-bar" @submit.prevent="filter">
      <div class="search-field">
        <Icon name="search" :size="18" /><input
          v-model="query"
          aria-label="搜索词条"
          placeholder="搜索名称、别名或正文"
        />
      </div>
      <select v-model="category" aria-label="分类筛选" @change="filter">
        <option value="">全部分类</option>
        <option value="streamer">主播档案</option>
        <option value="common">通用知识</option>
        <option value="style">翻译风格</option>
        <option value="translation">翻译纠错</option></select
      ><select v-model="status" aria-label="审核状态筛选" @change="filter">
        <option value="">全部裁决状态</option>
        <option value="pending">待裁决</option>
        <option value="needs_changes">待补充</option>
        <option value="approved">已通过</option>
        <option value="rejected">已驳回</option></select
      ><button class="button" type="submit">查询</button>
      <button type="button" class="text-button muted" @click="resetFilters">清除筛选</button>
    </form>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th class="entry-title-column">词条</th>
            <th>分类</th>
            <th>管理员决定</th>
            <th>成员记录</th>
            <th>负责人</th>
            <th>更新时间</th>
            <th><span class="sr-only">操作</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in rows" :key="entry.id">
            <td>
              <button class="table-title" @click="openEntry(entry)">
                {{ entry.title
                }}<span
                  v-if="entry.operation === 'delete'"
                  class="badge rejected"
                  >拟删除</span
                >
              </button>
              <div class="table-subtitle">{{ entry.summary }}</div>
            </td>
            <td>
              <span>{{ labels[entry.category] }}</span
              ><small class="cell-note">{{ entry.kind || "—" }}</small>
            </td>
            <td>
              <span class="badge" :class="entry.status">{{
                labels[entry.status]
              }}</span
              ><small class="cell-note">{{
                entry.published ? "已发布" : "v" + entry.number
              }}</small>
            </td>
            <td>
              <span class="review-count"
                ><Icon name="message" :size="15" />{{
                  entry.review_count
                }}</span
              >
              <small v-if="entry.my_review" class="cell-note green-text">我已审核</small>
            </td>
            <td>
              {{
                people.find((p) => p.id === entry.assignee_id)?.name || "未分配"
              }}
            </td>
            <td class="nowrap muted">{{ date(entry.created_at) }}</td>
            <td>
              <button class="text-button" @click="openEntry(entry)">
                审核<Icon name="chevron" :size="14" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!rows.length && !loading" class="empty-state">
      <Icon name="search" :size="36" />
      <h3>没有找到匹配的知识</h3>
    </div>
    <div v-if="loading" class="loading-inline"><span class="spinner"></span>正在读取…</div>
    <div class="pagination">
      <span
        >共 {{ total }} 条 · 第 {{ page }} /
        {{ Math.max(1, Math.ceil(total / 30)) }} 页</span
      >
      <div>
        <button
          class="button small"
          :disabled="page === 1 || loading"
          @click="
            page--;
            load();
          "
        >
          上一页</button
        ><button
          class="button small"
          :disabled="page * 30 >= total || loading"
          @click="
            page++;
            load();
          "
        >
          下一页
        </button>
      </div>
    </div>
  </section>
</template>
