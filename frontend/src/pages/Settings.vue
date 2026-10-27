<script setup lang="ts">
import { onMounted, ref } from "vue";
import { api, post, date, type User } from "../api";
import { useApp } from "../context";
import Icon from "../components/Icon.vue";
import Modal from "../components/Modal.vue";
defineProps<{ accountOnly?: boolean }>();
const { user, people, settings, toast, refresh } = useApp();
const busy = ref(false),
  userOpen = ref(false),
  tab = ref("members"),
  name = ref(settings.value.name),
  profile = ref(settings.value.profile);
const memberName = ref(user.value?.name || "");
async function saveMemberName() {
  await act(async () => {
    user.value = await post("/auth/name", { name: memberName.value });
    toast("审核署名已保存");
  });
}
const newUser = ref({ username: "", name: "", password: "", role: "admin" }),
  password = ref({ current: "", password: "" }),
  logs = ref<any[]>([]),
  auditPage = ref(1);
const actions: Record<string, string> = {
  "auth.login": "登录",
  "auth.password": "修改密码",
  "access.enter": "口令进入",
  "access.name": "修改审核署名",
  "user.create": "创建账号",
  "user.update": "调整账号",
  "settings.update": "更新设置",
  "import.upload": "上传知识",
  "import.confirm": "确认导入",
  "entry.revise": "修订正文",
  "entry.assign": "分配审核",
  "review.record": "提交审核记录",
  "review.resolve": "处理成员意见",
  "review.decide": "管理员裁决",
  "release.request": "创建发布任务",
  "release.complete": "完成发布",
  "release.download": "下载发布包",
  "job.retry": "重试后台任务",
};
async function act(fn: () => Promise<void>) {
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
function createUser() {
  act(async () => {
    await post("/users", newUser.value);
    userOpen.value = false;
    newUser.value = { username: "", name: "", password: "", role: "admin" };
    toast("管理员账号已创建");
  });
}
function togglePerson(person: User) {
  act(async () => {
    await api("/users/" + person.id, {
      method: "PATCH",
      body: JSON.stringify({ active: !person.active, role: person.role }),
    });
    toast(person.active ? "账号已停用，现有会话已失效" : "账号已启用");
  });
}
function changeRole(person: User, value: string) {
  act(async () => {
    await api("/users/" + person.id, {
      method: "PATCH",
      body: JSON.stringify({ active: person.active, role: value }),
    });
    toast("角色已更新，成员需重新登录");
  });
}
function saveSettings() {
  act(async () => {
    await api("/settings", {
      method: "PUT",
      body: JSON.stringify({ name: name.value, profile: profile.value }),
    });
    toast("知识库设置已保存");
  });
}
async function changePassword() {
  busy.value = true;
  try {
    await post("/auth/password", password.value);
    toast("密码已更新，请重新登录");
    window.dispatchEvent(new Event("session-expired"));
  } catch (e) {
    toast((e as Error).message, true);
  } finally {
    busy.value = false;
  }
}
async function loadAudit() {
  try {
    logs.value = await api("/audit?page=" + auditPage.value);
  } catch (e) {
    toast((e as Error).message, true);
  }
}
/** 日志详情按键值行展示，超长值截断，完整内容留在 title 里。 */
function detailText(value: unknown) {
  const text = typeof value === "string" ? value : (JSON.stringify(value) ?? "");
  return text.length > 80 ? text.slice(0, 80) + "…" : text;
}
function detailTitle(value: unknown) {
  return typeof value === "string" ? value : (JSON.stringify(value) ?? "");
}
</script>
<template>
  <div class="page-heading">
    <div>
      <h1>
        {{
          accountOnly
            ? user?.passcode_member
              ? "我的审核署名"
              : "账号与登录安全"
            : "管理设置"
        }}
      </h1>
    </div>
    <button
      v-if="!accountOnly && user?.role === 'admin'"
      class="button primary"
      @click="userOpen = true"
    >
      <Icon name="plus" :size="17" />创建管理员账号
    </button>
  </div>
  <template v-if="!accountOnly && user?.role === 'admin'"
    ><div class="settings-tabs tabs">
      <button :class="{ active: tab === 'members' }" @click="tab = 'members'">
        成员管理</button
      ><button
        :class="{ active: tab === 'knowledge' }"
        @click="tab = 'knowledge'"
      >
        知识库设置</button
      ><button
        :class="{ active: tab === 'audit' }"
        @click="
          tab = 'audit';
          loadAudit();
        "
      >
        操作记录
      </button>
    </div>
    <section v-if="tab === 'members'" class="panel">
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>成员</th>
              <th>账号</th>
              <th>角色</th>
              <th>状态</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="person in people" :key="person.id">
              <td>
                <span class="inline-person"
                  ><span class="avatar small">{{
                    person.name.slice(0, 1)
                  }}</span
                  ><strong>{{ person.name }}</strong
                  ><span v-if="person.id === user.id" class="tag"
                    >我</span
                  ></span
                >
              </td>
              <td>
                {{ person.passcode_member ? "口令进入" : person.username }}
              </td>
              <td>
                <select
                  :value="person.role"
                  :disabled="
                    person.passcode_member || person.id === user.id || busy
                  "
                  :aria-label="person.name + '的角色'"
                  @change="
                    changeRole(
                      person,
                      ($event.target as HTMLSelectElement).value,
                    )
                  "
                >
                  <option value="member">普通成员</option>
                  <option value="admin">管理员</option>
                </select>
              </td>
              <td>
                <span
                  class="badge"
                  :class="person.active ? 'approved' : 'rejected'"
                  >{{ person.active ? "正常" : "已停用" }}</span
                >
              </td>
              <td>
                <button
                  v-if="person.id !== user.id"
                  class="text-button"
                  :disabled="busy"
                  @click="togglePerson(person)"
                >
                  {{ person.active ? "停用账号" : "启用账号" }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <section v-else-if="tab === 'knowledge'" class="panel settings-form">
      <h2>知识库与兼容设置</h2>
      <form @submit.prevent="saveSettings">
        <label
          >知识库名称<input v-model="name" required maxlength="100" /></label
        ><label
          >导出兼容规则<select v-model="profile">
            <option
              v-for="(value, key) in settings.profiles"
              :key="key"
              :value="key"
            >
              {{ value.name }}
            </option>
          </select></label
        >
        <button class="button primary" :disabled="busy">保存设置</button>
      </form>
    </section>
    <section v-else class="panel">
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>时间</th>
              <th>操作人</th>
              <th>操作</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="log in logs" :key="log.id">
              <td class="nowrap">{{ date(log.created_at) }}</td>
              <td>{{ log.actor }}</td>
              <td class="nowrap">{{ actions[log.action] || log.action }}</td>
              <td>
                <dl
                  v-if="log.detail && Object.keys(log.detail).length"
                  class="audit-fields"
                >
                  <template v-for="(value, key) in log.detail" :key="key">
                    <dt>{{ key }}</dt>
                    <dd :title="detailTitle(value)">{{ detailText(value) }}</dd>
                  </template>
                </dl>
                <span v-else class="muted">—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="pagination">
        <span>第 {{ auditPage }} 页</span>
        <div>
          <button
            class="button small"
            :disabled="auditPage === 1"
            @click="
              auditPage--;
              loadAudit();
            "
          >
            上一页</button
          ><button
            class="button small"
            :disabled="logs.length < 50"
            @click="
              auditPage++;
              loadAudit();
            "
          >
            下一页
          </button>
        </div>
      </div>
    </section></template
  >
  <section v-else class="panel settings-form">
    <div class="inline-person">
      <span class="avatar">{{ user?.name.slice(0, 1) }}</span>
      <div>
        <h2>{{ user?.name }}</h2>
        <span class="muted"
          >{{ user?.username }} ·
          {{ user?.role === "admin" ? "管理员" : "普通成员" }}</span
        >
      </div>
    </div>
    <form v-if="user?.passcode_member" @submit.prevent="saveMemberName">
      <label
        >审核署名<input
          v-model="memberName"
          required
          maxlength="80"
          placeholder="昵称"
      /></label>
      <button class="button primary" :disabled="busy">保存署名</button>
    </form>
    <form v-else @submit.prevent="changePassword">
      <h3>修改密码</h3>
      <label
        >当前密码<input
          v-model="password.current"
          type="password"
          autocomplete="current-password"
          required /></label
      ><label
        >新密码<input
          v-model="password.password"
          type="password"
          autocomplete="new-password"
          minlength="10"
          maxlength="256"
          required
          placeholder="至少 10 个字符"
      /></label>
      <p class="muted small-text">修改后会退出此账号的所有登录会话。</p>
      <button class="button primary" :disabled="busy">
        修改密码并重新登录
      </button>
    </form>
  </section>
  <Modal v-if="userOpen" title="创建管理员账号" @close="userOpen = false"
    ><form @submit.prevent="createUser">
      <label
        >账号<input
          v-model="newUser.username"
          required
          pattern="[A-Za-z0-9_.\-]{3,80}"
          placeholder="3–80 位字母、数字、点、下划线或连字符"
          autocomplete="off" /></label
      ><label
        >显示名称<input
          v-model="newUser.name"
          required
          maxlength="80"
          placeholder="成员的名称" /></label
      ><label
        >初始密码<input
          v-model="newUser.password"
          type="password"
          required
          minlength="10"
          maxlength="256"
          autocomplete="new-password"
          placeholder="至少 10 个字符"
      /></label>
      <div class="modal-actions">
        <button type="button" class="button" @click="userOpen = false">
          取消</button
        ><button class="button primary" :disabled="busy">创建账号</button>
      </div>
    </form></Modal
  >
</template>
