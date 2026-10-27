<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { api, date, labels, type Detail, type Entry } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import Markdown from "../components/Markdown.vue";

const props = defineProps<{ entryId?: string }>();
const { go, toast, settings } = useApp();

const loading = ref(true),
  error = ref(""),
  profile = ref<Detail | null>(null),
  entries = ref<Entry[]>([]),
  typeFilter = ref("");

/* ---------------------------------------------------------------- 解析 */

type Field = { key: string; value: string };
type Nest = { depth: number; value: string };

function splitSections(md: string): Record<string, string> {
  const out: Record<string, string> = {};
  let heading = "";
  let buffer: string[] = [];
  const flush = () => {
    if (heading) out[heading] = buffer.join("\n").trim();
  };
  for (const line of md.split(/\r?\n/)) {
    const found = line.match(/^#{1,3}\s+(.+?)\s*$/);
    if (found) {
      flush();
      heading = found[1]!;
      buffer = [];
    } else buffer.push(line);
  }
  flush();
  return out;
}

function strip(value: string): string {
  return value.replace(/^-\s*/, "").trim();
}

function fields(text: string): Field[] {
  return (text ?? "").replace(/<!--[^]*?-->/g, "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"))
    .map(strip)
    .map((line) => {
      const label = line.match(/^\[([^\[\]]+)\]\s?(.*)$/);
      if (label) return { key: label[1]!.trim(), value: label[2]!.trim() };
      const at = line.search(/[:：]/);
      return at > 0
        ? { key: line.slice(0, at).trim(), value: line.slice(at + 1).trim() }
        : { key: "", value: line };
    })
    .filter((item) => item.value);
}

function pick(list: Field[], key: string): string {
  return list.find((item) => item.key === key)?.value ?? "";
}

// 有些条目写作「- 节目问候梗「…」「…」」而没有冒号，需要按前缀找整行。
function prefix(list: Field[], head: string): string {
  const hit = list.find((item) => item.value.startsWith(head));
  return hit ? hit.value.slice(head.length).replace(/^[:：]\s*/, "") : "";
}

function bullets(text: string): string[] {
  return (text ?? "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("-"))
    .map(strip);
}

function nests(text: string): Nest[] {
  return (text ?? "")
    .split(/\r?\n/)
    .filter((line) => line.trim().startsWith("-"))
    .map((line) => ({
      depth: Math.floor((line.length - line.trimStart().length) / 2),
      value: strip(line),
    }));
}

function quotes(text: string): string[] {
  return [...(text ?? "").matchAll(/「([^」]+)」/g)].map((match) => match[1]!);
}

const visibleContent = computed(() => (profile.value?.entry.content ?? "").replace(/<!--[^]*?-->/g, ""));
const sections = computed(() => splitSections(visibleContent.value));
const nodeFormat = computed(() => ["finesub-0.5.1", "finesub-0.6.0"].includes(settings.value.profile) || /^(?:-\s+)?\[[^\[\]]+\]/m.test(visibleContent.value));

const intro = computed(
  () =>
    visibleContent.value
      .split(/\r?\n/)
      .find((line) => line.trim() && !line.startsWith("#"))
      ?.trim() ?? "",
);

const registry = computed(() => fields(sections.value["档案"] ?? ""));

const basics = computed(() => {
  const list = fields(sections.value["基本信息"] ?? "");
  return [
    { key: "生日", value: pick(list, "生日") },
    { key: "出身地", value: pick(list, "出身地") },
    { key: "身高", value: pick(list, "身高") },
    { key: "血型", value: pick(list, "血型") },
    { key: "事务所", value: pick(list, "事务所") },
    { key: "特长", value: pick(list, "特长") },
  ].filter((item) => item.value);
});

const names = computed(() =>
  pick(registry.value, "别名")
    .split(/[,、，]/)
    .map((item) => item.replace(/（[^）]*）/g, "").trim())
    .filter(Boolean),
);

const basicName = computed(() =>
  pick(registry.value, "本名")
    .split(/[／/]/)
    .map((item) => item.trim())
    .filter(Boolean),
);

// 人设与徽标都只取档案里真实存在的内容，读不到就不渲染。
const roleLine = computed(() => pick(registry.value, "人设"));
const monogram = computed(
  () => profile.value?.entry.title.trim().slice(0, 1) ?? "",
);

// 节目名只取当前词条自己写的内容，取不到就用中性标题，不写死某个节目。
const programName = computed(
  () => `${intro.value}\n${roleLine.value}`.match(/《([^》]+)》/)?.[1]?.trim() ?? "",
);

const live = computed(() => fields(sections.value["直播内容"] ?? ""));

const segments = computed(() =>
  pick(live.value, "固定环节")
    .split("、")
    .map((item) =>
      item
        .replace(/等（[^）]*）$/, "")
        .replace(/等$/, "")
        .trim(),
    )
    .filter(Boolean)
    .map((item) => {
      const found = item.match(/^(.+?)（([^）]*)）$/);
      return found
        ? { name: found[1]!.trim(), note: found[2]!.trim() }
        : { name: item, note: "" };
    }),
);

const greetings = computed(() =>
  quotes(prefix(live.value, "节目问候梗")).map((name) => ({
    name,
    note: "节目问候",
  })),
);

const style = computed(() => fields(sections.value["说话风格"] ?? ""));

const catchphrases = computed(() => quotes(pick(style.value, "口癖")));

const timeline = computed(() =>
  bullets(sections.value["重要经历"] ?? "").map((line) => {
    // 日期写法不统一：「2024-12」「2026-03-23」「2025 年末」「2026-07~08」。
    const found = line.match(/^(\d{4}[^:：]*?)\s*[:：]\s*(.+)$/);
    return found
      ? { date: found[1]!.trim(), text: found[2]! }
      : { date: "", text: line };
  }),
);

const relations = computed(() => {
  const list = nests(sections.value["人际关系"] ?? "");
  const groups: {
    title: string;
    note: string;
    items: { name: string; note: string }[];
  }[] = [];
  for (const item of list) {
    const at = item.value.search(/[:：]/);
    const head = at > 0 ? item.value.slice(0, at).trim() : item.value.trim();
    const rest = at > 0 ? item.value.slice(at + 1).trim() : "";
    if (item.depth === 0) {
      groups.push({ title: head, note: rest, items: [] });
      continue;
    }
    const group = groups[groups.length - 1];
    if (!group) continue;
    if (!rest) {
      // 纯补充说明：并入上一位成员，或作为整组的说明
      const last = group.items[group.items.length - 1];
      if (last) last.note = `${last.note}（${head}）`.trim();
      else group.note = `${group.note}（${head}）`.trim();
      continue;
    }
    group.items.push({ name: head, note: rest });
  }
  return groups;
});

const kindOf = (item: Entry) => item.kind || "未标注类型";

const kinds = computed(() => {
  const seen = new Map<string, number>();
  for (const item of entries.value)
    seen.set(kindOf(item), (seen.get(kindOf(item)) ?? 0) + 1);
  return [...seen.entries()].sort((a, b) => b[1] - a[1]);
});

// 翻译纠错词条本身没有「类型」字段，属正常；只统计应填而未填的。
const untyped = computed(() =>
  entries.value.filter((item) => !item.kind && item.category !== "translation"),
);

const visibleEntries = computed(() =>
  typeFilter.value
    ? entries.value.filter((item) => kindOf(item) === typeFilter.value)
    : entries.value,
);

const profileEntryId = computed(() => profile.value?.entry.entry_id ?? "");

/* ---------------------------------------------------------------- 加载 */

// 连点刷新会并发多次 load()，只让最后一次的结果落到页面上。
let sequence = 0;

async function load() {
  const request = ++sequence;
  loading.value = true;
  error.value = "";
  profile.value = null;
  typeFilter.value = "";
  const wanted = props.entryId?.trim() ?? "";
  try {
    // 已知词条直接按 id 取，列表只用于没带 id 时选默认档案，
    // 否则主播超过 100 个、或目标不在第一页时会误报「未找到」。
    let targetId = wanted;
    if (!targetId) {
      const list = await api<{ items: Entry[] }>(
        "/entries?category=streamer&limit=100",
      );
      if (request !== sequence) return;
      targetId = list.items[0]?.entry_id ?? "";
      if (!targetId) {
        error.value = "暂无主播档案";
        return;
      }
    }
    const [detail, common, translation] = await Promise.all([
      api<Detail>(`/entries/${targetId}`),
      api<{ items: Entry[] }>("/entries?category=common&limit=100"),
      api<{ items: Entry[] }>("/entries?category=translation&limit=100"),
    ]);
    if (request !== sequence) return;
    if (wanted && detail.entry.category !== "streamer") {
      error.value = "未找到该主播档案";
      return;
    }
    profile.value = detail;
    entries.value = [
      ...common.items,
      ...translation.items.map((item) => ({ ...item, kind: "翻译纠错" })),
    ];
  } catch (e) {
    if (request !== sequence) return;
    const status = (e as Error & { status?: number }).status;
    error.value =
      wanted && status === 404 ? "未找到该主播档案" : (e as Error).message;
    toast(error.value, true);
  } finally {
    if (request === sequence) loading.value = false;
  }
}

onMounted(load);
// 父组件按 entryId 挂 :key，重建时这里不会触发；保留是为了 :key 被移除后
// 仍能跟随 entryId 重新加载，避免静默显示上一个主播。
watch(() => props.entryId, load);
</script>

<template>
  <div v-if="loading" class="loading-state">
    <span class="spinner"></span>正在读取主播档案
  </div>
  <div v-else-if="error" class="panel empty-state">
    <Icon name="users" :size="36" />
    <h2>暂时无法生成档案页</h2>
    <p>{{ error }}</p>
    <button class="button" @click="load">重试</button>
  </div>
  <template v-else-if="profile">
    <section class="profile-hero">
      <div class="profile-identity">
        <span v-if="monogram" class="profile-monogram">{{ monogram }}</span>
        <div>
          <div class="eyebrow">主播档案</div>
          <h1>{{ profile.entry.title }}</h1>
          <p v-if="basicName.length" class="profile-romaji">{{ basicName.join(" / ") }}</p>
          <p v-if="roleLine" class="profile-role">{{ roleLine }}</p>
        </div>
      </div>
      <div class="profile-hero-side">
        <div class="nickname-block">
          <span class="nickname-label">观众爱称</span>
          <div class="nickname-chips">
            <span v-for="name in names" :key="name" class="nickname-chip">{{
              name
            }}</span>
          </div>
        </div>
        <div class="profile-actions">
          <button class="button primary" @click="go('review', profileEntryId)">
            <Icon name="edit" :size="16" />审核 / 修订档案
          </button>
          <button class="button" @click="load">
            <Icon name="refresh" :size="16" />刷新
          </button>
        </div>
      </div>
    </section>

    <p v-if="intro" class="profile-intro">{{ intro }}</p>

    <section class="profile-facts" v-if="basics.length">
      <div v-for="item in basics" :key="item.key" class="fact">
        <span class="fact-label">{{ item.key }}</span>
        <span class="fact-value">{{ item.value }}</span>
      </div>
    </section>

    <section v-if="nodeFormat" class="panel profile-block">
      <div class="panel-heading"><h2>当前主播档案</h2></div>
      <div class="content-pad"><Markdown :text="visibleContent" /></div>
    </section>
    <section v-else class="panel profile-block">
      <div class="panel-heading">
        <div>
          <h2>{{ programName ? `节目《${programName}》` : "节目内容" }}</h2>
        </div>
        <Icon name="message" :size="19" />
      </div>
      <div class="content-pad">
        <div v-if="catchphrases.length" class="catchphrase-row">
          <span class="nickname-label">标志台词</span>
          <p v-for="line in catchphrases" :key="line" class="catchphrase">
            {{ line }}
          </p>
        </div>
        <div v-if="greetings.length" class="segment-grid">
          <article
            v-for="item in greetings"
            :key="item.name"
            class="segment-card quote"
          >
            <strong>{{ item.name }}</strong>
            <small>节目问候语</small>
          </article>
        </div>
        <template v-if="segments.length">
          <h3 class="profile-subheading">固定环节</h3>
          <div class="segment-grid">
            <article
              v-for="item in segments"
              :key="item.name"
              class="segment-card"
            >
              <strong>{{ item.name }}</strong>
              <small v-if="item.note">{{ item.note }}</small>
            </article>
          </div>
        </template>
        <div v-if="live.length" class="profile-notes">
          <p
            v-for="item in live.filter(
              (x) => x.key && x.key !== '固定环节',
            )"
            :key="item.key"
          >
            <strong>{{ item.key }}</strong>{{ item.value }}
          </p>
        </div>
      </div>
    </section>

    <div class="profile-columns">
      <div class="profile-main">
        <section v-if="!nodeFormat" class="panel profile-block">
          <div class="panel-heading">
            <div>
              <h2>年表 <span class="count-chip">{{ timeline.length }}</span></h2>
            </div>
            <Icon name="clock" :size="19" />
          </div>
          <ol class="timeline">
            <li v-for="item in timeline" :key="item.text" class="timeline-item">
              <span class="timeline-dot"></span>
              <div>
                <span v-if="item.date" class="timeline-date">{{
                  item.date
                }}</span>
                <p>{{ item.text }}</p>
              </div>
            </li>
          </ol>
        </section>

        <section class="panel profile-block">
          <div class="panel-heading">
            <div>
              <h2>知识库词条 <span class="count-chip">{{ entries.length }}</span></h2>
            </div>
            <Icon name="book" :size="19" />
          </div>
          <div class="content-pad">
            <div class="type-filter">
              <button
                class="filter-chip"
                :class="{ active: typeFilter === '' }"
                @click="typeFilter = ''"
              >
                全部 <span>{{ entries.length }}</span>
              </button>
              <button
                v-for="[kind, count] in kinds"
                :key="kind"
                class="filter-chip"
                :class="{ active: typeFilter === kind }"
                @click="typeFilter = kind"
              >
                {{ kind }} <span>{{ count }}</span>
              </button>
            </div>
            <p v-if="untyped.length" class="untyped-hint">
              <Icon name="shield" :size="15" />{{ untyped.length }} 项未分类
            </p>
          </div>
          <div class="kb-grid">
            <button
              v-for="item in visibleEntries"
              :key="item.id"
              class="kb-card"
              @click="go('review', item.entry_id)"
            >
              <span class="kb-card-head">
                <strong>{{ item.title }}</strong>
                <span class="badge" :class="item.status">{{
                  labels[item.status]
                }}</span>
              </span>
              <small :class="{ missing: !item.kind }">{{ kindOf(item) }}</small>
              <p>{{ item.summary || "暂无摘要" }}</p>
              <span class="kb-card-foot">
                <span class="review-count"
                  ><Icon name="message" :size="14" />{{
                    item.review_count
                  }}</span
                >
                <span v-if="item.published" class="tag current">已发布</span>
                <span v-else class="tag">v{{ item.number }}</span>
              </span>
            </button>
          </div>
        </section>
      </div>

      <aside v-if="!nodeFormat" class="profile-aside">
        <section class="panel profile-block">
          <div class="panel-heading">
            <div><h2>说话风格</h2></div>
          </div>
          <div class="content-pad profile-notes">
            <p v-for="item in style" :key="item.key + item.value">
              <strong v-if="item.key">{{ item.key }}</strong>{{ item.value }}
            </p>
          </div>
        </section>

        <section class="panel profile-block">
          <div class="panel-heading">
            <div>
              <h2>人际关系</h2>
            </div>
          </div>
          <div class="content-pad relation-groups">
            <div
              v-for="group in relations"
              :key="group.title"
              class="relation-group"
            >
              <h3>{{ group.title }}</h3>
              <p v-if="group.note && !group.items.length" class="relation-note">
                {{ group.note }}
              </p>
              <ul v-if="group.items.length">
                <li v-for="item in group.items" :key="item.name">
                  <strong>{{ item.name }}</strong>
                  <span v-if="item.note">{{ item.note }}</span>
                </li>
              </ul>
            </div>
          </div>
        </section>

        <section class="panel profile-block">
          <div class="content-pad meta-block">
            <h3>档案元数据</h3>
            <dl>
              <dt>词条路径</dt>
              <dd>{{ profile.entry.path }}</dd>
              <dt>当前版本</dt>
              <dd>v{{ profile.entry.number }}</dd>
              <dt>更新时间</dt>
              <dd>{{ date(profile.entry.created_at) }}</dd>
              <dt>审核状态</dt>
              <dd>{{ labels[profile.entry.status] }}</dd>
              <dt>发布状态</dt>
              <dd>
                {{ profile.entry.published ? "已进入当前发布" : "尚未发布" }}
              </dd>
            </dl>
          </div>
        </section>
      </aside>
    </div>

    <details class="panel raw-panel">
      <summary>查看档案词条原文（Markdown）</summary>
      <Markdown :text="profile.entry.content" />
    </details>
  </template>
</template>
