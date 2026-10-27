<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { api, date, labels, type Entry } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
const { user, go, toast } = useApp();
const queue = computed(() => user.value?.role === 'admin' ? 'status=pending' : 'status=pending&reviewed=no');
const stats = ref<any>(null),
  queueTotal = ref(0),
  entries = ref<Entry[]>([]),
  error = ref("");
onMounted(async () => {
  try {
    const [s, e] = await Promise.all([
      api("/dashboard"),
      api("/entries?" + queue.value + "&limit=6"),
    ]);
    stats.value = s;
    entries.value = e.items;
    queueTotal.value = e.total;
  } catch (e) {
    error.value = (e as Error).message;
    toast(error.value, true);
  }
});
</script>
<template>
  <div class="page-heading dashboard-heading workbench-lead">
    <div>
      <h1>工作台</h1>
    </div>
    <div class="heading-actions">
    <button v-if="entries.length" class="button primary" @click="go('review', entries[0]!.entry_id, queue)">继续审核 <Icon name="arrow" :size="17" /></button>
    <button
      v-if="user?.role === 'admin'"
      class="button"
      @click="go('imports')"
    >
      <Icon name="plus" :size="18" />导入知识库
    </button>
    <button v-else class="button primary" @click="go('entries')">
      <Icon name="book" :size="18" />浏览知识库
    </button>
    </div>
  </div>
  <p v-if="error" class="form-error">{{ error }}</p>
  <template v-if="stats">
    <div class="workflow-overview" aria-label="知识审核流程">
      <button @click="go('entries')"><Icon name="book" :size="21" /><span>知识库<strong>{{ stats.total }}<small>条知识</small></strong></span><Icon name="arrow" :size="16" /></button>
      <button class="current-step" @click="go('entries', 'pending')"><Icon name="clock" :size="21" /><span>待裁决<strong>{{ stats.counts.pending || 0 }}<small>条</small></strong></span><Icon name="arrow" :size="16" /></button>
      <button @click="go('entries', 'needs_changes')"><Icon name="edit" :size="21" /><span>待补充<strong>{{ stats.counts.needs_changes || 0 }}<small>条</small></strong></span><Icon name="arrow" :size="16" /></button>
      <button @click="go('releases')"><Icon name="download" :size="21" /><span>通过待发布<strong>{{ stats.pending_publish }}<small>条</small></strong></span><Icon name="arrow" :size="16" /></button>
    </div>
    <div class="dashboard-columns">
      <section class="panel">
        <div class="panel-heading">
          <div class="heading-with-count">
            <h2>{{ user?.role === 'admin' ? '待裁决知识' : '我未审核的知识' }}</h2>
            <span class="count-chip">{{ queueTotal }}</span>
          </div>
          <button class="text-button" @click="go('entries', '', queue)">
            查看全部<Icon name="arrow" :size="16" />
          </button>
        </div>
        <div v-if="!entries.length" class="empty-state">
          <Icon name="check" :size="36" />
          <h3>暂无待审知识</h3>
          <button
            v-if="user?.role === 'admin' && !stats.total"
            class="button"
            @click="go('imports')"
          >
            导入知识库<Icon name="arrow" :size="16" />
          </button>
        </div>
        <button
          v-for="entry in entries"
          :key="entry.id"
          class="entry-preview"
          @click="go('review', entry.entry_id, queue)"
        >
          <span class="entry-file"
            ><Icon
              :name="entry.category === 'translation' ? 'message' : 'file'"
          /></span>
          <span class="entry-preview-body"
            ><strong>{{ entry.title }}</strong
            ><small
              >{{ labels[entry.category]
              }}<span v-if="entry.kind"> / {{ entry.kind }}</span
              ><span> · v{{ entry.number }}</span></small
            ></span
          >
          <span class="review-count"
            ><Icon name="message" :size="15" />{{ entry.review_count }}</span
          >
          <span class="badge pending">待裁决</span
          ><Icon name="chevron" :size="16" />
        </button>
      </section>
      <div class="dashboard-side">
        <section class="panel">
          <div class="panel-heading">
            <h2>最近导入</h2>
            <Icon name="upload" :size="18" />
          </div>
          <p v-if="!stats.recent_batches.length" class="empty-small">
            暂无导入批次
          </p>
          <button
            v-for="batch in stats.recent_batches"
            :key="batch.id"
            class="batch-preview"
            @click="go('imports', batch.id)"
          >
            <div>
              <strong>{{ batch.name }}</strong
              ><small>{{ date(batch.created_at) }}</small>
            </div>
            <span class="badge" :class="batch.status">{{
              labels[batch.status]
            }}</span>
          </button>
        </section>
        <section class="panel quick-links">
          <div class="panel-heading">
            <h2>快捷入口</h2>
            <span class="muted small-text">{{ stats.reviews }} 条审核记录</span>
          </div>
          <button @click="go('mine')">
            <span class="quick-link-icon"><Icon name="check" :size="18" /></span
            ><span>我的审核</span
            ><Icon name="chevron" :size="16" />
          </button>
          <button @click="go('streamer')">
            <span class="quick-link-icon"><Icon name="users" :size="18" /></span
            ><span>主播档案</span
            ><Icon name="chevron" :size="16" />
          </button>
        </section>
      </div>
    </div>
  </template>
  <div v-else-if="!error" class="loading-state">
    <span class="spinner"></span>正在读取工作台
  </div>
</template>
