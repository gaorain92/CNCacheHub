<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  Check,
  Connection,
  CopyDocument,
  Delete,
  Edit,
  FolderOpened,
  Goods,
  HotWater,
  Plus,
  Position,
  Refresh,
  Search,
  Setting,
  Warning,
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useDNSStore } from '@/stores/dns'
import { useSteamcmdStore } from '@/stores/steamcmd'
import { useAuthStore } from '@/stores/auth'

const dns = useDNSStore()
const steamcmd = useSteamcmdStore()
const auth = useAuthStore()

// 表单本地状态（与 store.config 双向同步：syncFromStore 拉取，syncToStore 提交）
const enabled = ref(false)
const listenAddr = ref('0.0.0.0:5353')
const upstream = ref('1.1.1.1:53')
const answerIp = ref('127.0.0.1')
const rulesText = ref('') // 一行一条
const testingDomain = ref('client-download.steampowered.com')

const saving = ref(false)
const testRunning = ref(false)
const copied = ref<string | null>(null)

onMounted(async () => {
  if (!dns.config) await dns.fetch()
  syncFromStore()
  if (steamcmd.items.length === 0) await steamcmd.fetch()
})

function syncFromStore(): void {
  if (!dns.config) return
  enabled.value = dns.config.enabled
  listenAddr.value = dns.config.listenAddr
  upstream.value = dns.config.upstream
  answerIp.value = dns.config.answerIp
  rulesText.value = dns.config.domainRules.join('\n')
}

watch(
  () => dns.config,
  () => syncFromStore()
)

const dirty = computed(() => {
  const cfg = dns.config
  if (!cfg) return false
  const curRules = rulesText.value
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  const rulesEqual =
    curRules.length === cfg.domainRules.length &&
    curRules.every((r, i) => r === cfg.domainRules[i])
  return (
    enabled.value !== cfg.enabled ||
    listenAddr.value !== cfg.listenAddr ||
    upstream.value !== cfg.upstream ||
    answerIp.value !== cfg.answerIp ||
    !rulesEqual
  )
})

async function onSave(): Promise<void> {
  saving.value = true
  const rules = rulesText.value
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  const ok = await dns.patch({
    enabled: enabled.value,
    listenAddr: listenAddr.value.trim(),
    upstream: upstream.value.trim(),
    answerIp: answerIp.value.trim(),
    domainRules: rules,
  })
  saving.value = false
  if (ok) {
    ElMessage.success(enabled.value ? 'DNS 启动器已启动' : 'DNS 启动器已停用')
  } else {
    ElMessage.error(dns.errorMessage || '保存失败')
  }
}

function onReset(): void {
  syncFromStore()
}

async function onTest(): Promise<void> {
  const d = testingDomain.value.trim()
  if (!d) {
    ElMessage.warning('请输入要测试的域名')
    return
  }
  testRunning.value = true
  const r = await dns.test(d)
  testRunning.value = false
  if (r) {
    if (r.error) {
      ElMessage.error(`查询失败：${r.error}`)
    } else if (r.rcode === 'NOERROR') {
      ElMessage.success(`${r.domain} → ${r.answers.map((a) => a.data).join(', ') || '(无 A 记录)'}`)
    } else {
      ElMessage.warning(`${r.domain} → ${r.rcode}`)
    }
  }
}

// 模式 A 客户端接入命令
const modeACmd = computed(() => {
  const ip = answerIp.value || '<CNCacheHub_IP>'
  return `# 1. 让 SteamCMD 容器把 DNS 指向 CNCacheHub
docker run --rm \\
  --dns ${ip} \\
  --dns-option timeout:2 \\
  --dns-option attempts:3 \\
  -v /data/steamapps:/steamapps \\
  cm2network/steamcmd \\
  +login anonymous +app_update <APP_ID> validate +quit

# 2. 验证（容器内执行）
nslookup client-download.steampowered.com ${ip}
# 应返回 ${answerIp.value || '<CNCacheHub_IP>'}（命中 CNCacheHub 白名单）`
})

const modeBLanCmd = computed(() => {
  return `# 把宿主机 / 局域网其他机器的 DNS 切到 CNCacheHub（路由器 DHCP 推）
# 验证
nslookup client-download.steampowered.com ${answerIp.value || '<CNCacheHub_IP>'}

# SteamCMD 直接跑（不再需要 --dns）
steamcmd +login anonymous +app_update <APP_ID> validate +quit`
})

async function copy(text: string, key: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = key
    ElMessage.success('已复制到剪贴板')
    setTimeout(() => (copied.value = null), 1500)
  } catch {
    ElMessage.error('复制失败')
  }
}

function formatNum(n: number): string {
  return n.toLocaleString('en-US')
}

function relTime(unixSec: number): string {
  if (!unixSec) return '—'
  const diff = Date.now() / 1000 - unixSec
  if (diff < 60) return `${Math.floor(diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  return `${Math.floor(diff / 3600)}h ago`
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// === AppID 管理 ===

const dialogOpen = ref(false)
const dialogSaving = ref(false)
const editingId = ref<number | null>(null)
const form = ref({
  appId: 0,
  name: '',
  loginType: 'anonymous' as 'anonymous' | 'account',
  installDir: '',
  cacheBytesEstimate: 0,
})
const preheatingId = ref<number | null>(null)

function preheatStatusType(s: string): 'success' | 'warning' | 'danger' | 'info' {
  if (s === 'ok') return 'success'
  if (s === 'running') return 'info'
  if (s === 'skipped') return 'warning'
  if (s === 'error') return 'danger'
  return 'info'
}

function openCreate(): void {
  editingId.value = null
  form.value = { appId: 0, name: '', loginType: 'anonymous', installDir: '', cacheBytesEstimate: 0 }
  dialogOpen.value = true
}

function openEdit(a: { id: number; appId: number; name: string; loginType: 'anonymous' | 'account'; installDir: string; cacheBytesEstimate: number }): void {
  editingId.value = a.id
  form.value = {
    appId: a.appId,
    name: a.name,
    loginType: a.loginType,
    installDir: a.installDir,
    cacheBytesEstimate: a.cacheBytesEstimate,
  }
  dialogOpen.value = true
}

async function onSaveAppID(): Promise<void> {
  if (!form.value.appId || form.value.appId <= 0) {
    ElMessage.error('AppID 必须为正整数')
    return
  }
  if (!form.value.name.trim()) {
    ElMessage.error('名称不能为空')
    return
  }
  dialogSaving.value = true
  if (editingId.value) {
    const r = await steamcmd.patch(editingId.value, {
      name: form.value.name.trim(),
      loginType: form.value.loginType,
      installDir: form.value.installDir.trim(),
      cacheBytesEstimate: form.value.cacheBytesEstimate,
    })
    dialogSaving.value = false
    if (r) {
      ElMessage.success('已保存')
      dialogOpen.value = false
    } else {
      ElMessage.error(steamcmd.errorMessage || '保存失败')
    }
  } else {
    const r = await steamcmd.create({
      appId: form.value.appId,
      name: form.value.name.trim(),
      loginType: form.value.loginType,
      installDir: form.value.installDir.trim(),
    })
    dialogSaving.value = false
    if (r) {
      ElMessage.success(`已添加 AppID ${r.appId}`)
      dialogOpen.value = false
    } else {
      ElMessage.error(steamcmd.errorMessage || '创建失败')
    }
  }
}

async function onDelete(a: { id: number; appId: number; name: string }): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `确定删除 AppID ${a.appId} (${a.name})？此操作不可撤销。`,
      '删除确认',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  const ok = await steamcmd.remove(a.id)
  if (ok) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(steamcmd.errorMessage || '删除失败')
  }
}

async function onPreheat(a: { id: number; appId: number; name: string; loginType: string }): Promise<void> {
  preheatingId.value = a.id
  const r = await steamcmd.preheat(a.id)
  preheatingId.value = null
  if (r) {
    if (r.status === 'running') {
      ElMessage.success(`已触发预热：${r.message}`)
    } else if (r.status === 'skipped') {
      ElMessage.warning(r.message)
    } else if (r.status === 'ok') {
      ElMessage.success('预热完成')
    } else {
      ElMessage.error(r.message || '预热失败')
    }
    await steamcmd.fetch()
  } else {
    ElMessage.error(steamcmd.errorMessage || '预热请求失败')
  }
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><Goods /></el-icon>
      </div>
      <div class="flex-1">
        <h2 class="text-2xl font-semibold">SteamCMD 加速</h2>
        <p class="text-sm text-slate-400">DNS 启动器 · LANCache 风格劫持 · AppID 缓存指引</p>
      </div>
      <el-button :icon="Refresh" size="small" plain :loading="dns.loading" @click="dns.fetch()">刷新</el-button>
    </header>

    <div v-if="dns.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ dns.errorMessage }}
    </div>

    <!-- DNS 启动器状态卡 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
      <div class="flex items-center justify-between mb-4 flex-wrap gap-3">
        <div class="flex items-center gap-2">
          <el-icon :size="20" color="#2dd4bf"><Connection /></el-icon>
          <h3 class="text-base font-semibold">DNS 启动器状态</h3>
        </div>
        <div class="flex items-center gap-3">
          <div class="flex items-center gap-1.5 text-xs text-slate-400">
            <span class="dot" :class="dns.config?.enabled ? 'bg-mint animate-pulse' : 'bg-slate-500'" />
            <span>{{ dns.config?.enabled ? '已启动' : '已停用' }}</span>
          </div>
          <div v-if="dns.stats" class="text-xs text-slate-500 font-mono">
            查询 {{ formatNum(dns.stats.totalQueries) }} · 命中 {{ formatNum(dns.stats.hitQueries) }} · 上游 {{ formatNum(dns.stats.missQueries) }} · 失败 {{ formatNum(dns.stats.blockedQueries) }}
          </div>
        </div>
      </div>

      <div v-if="dns.config" class="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
        <div>
          <div class="text-slate-500">监听</div>
          <div class="font-mono text-slate-200 mt-1">{{ dns.config.listenAddr }}</div>
        </div>
        <div>
          <div class="text-slate-500">上游 DNS</div>
          <div class="font-mono text-slate-200 mt-1">{{ dns.config.upstream }}</div>
        </div>
        <div>
          <div class="text-slate-500">答案 IP（白名单命中）</div>
          <div class="font-mono text-slate-200 mt-1">{{ dns.config.answerIp }}</div>
        </div>
        <div>
          <div class="text-slate-500">最近查询</div>
          <div class="font-mono text-slate-200 mt-1">{{ relTime(dns.stats?.lastQueryAt ?? 0) }}</div>
        </div>
      </div>
    </div>

    <!-- DNS 配置表单 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-5">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#a3e635"><Setting /></el-icon>
          <h3 class="text-base font-semibold">DNS 启动器配置</h3>
        </div>
        <el-switch
          v-model="enabled"
          size="large"
          inline-prompt
          active-text="启动"
          inactive-text="停用"
          :disabled="!auth.isAdmin"
        />
      </div>

      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div>
          <label class="text-xs text-slate-400 mb-2 block">监听地址（host:port）</label>
          <el-input
            v-model="listenAddr"
            placeholder="0.0.0.0:5353"
            :disabled="!auth.isAdmin"
          />
          <p class="text-xs text-slate-500 mt-1">默认 5353/UDP（无需 root）。如需 53 需 root + 端口转发。</p>
        </div>
        <div>
          <label class="text-xs text-slate-400 mb-2 block">上游 DNS</label>
          <el-input
            v-model="upstream"
            placeholder="1.1.1.1:53"
            :disabled="!auth.isAdmin"
          />
          <p class="text-xs text-slate-500 mt-1">非白名单查询转发到这里。</p>
        </div>
        <div>
          <label class="text-xs text-slate-400 mb-2 block">答案 IP（A 记录）</label>
          <el-input
            v-model="answerIp"
            placeholder="192.168.1.10"
            :disabled="!auth.isAdmin"
          />
          <p class="text-xs text-slate-500 mt-1">CNCacheHub 自身可达 IP（局域网内机器要能连）。</p>
        </div>
      </div>

      <div>
        <label class="text-xs text-slate-400 mb-2 block">域名白名单（每行一条，支持 *.example.com 通配）</label>
        <el-input
          v-model="rulesText"
          type="textarea"
          :rows="9"
          placeholder="*.steamcontent.com"
          :disabled="!auth.isAdmin"
          class="font-mono"
        />
        <p class="text-xs text-slate-500 mt-1">
          命中：返回答案 IP；未命中：转发上游。修改后点保存会触发热重载。
        </p>
      </div>

      <div class="flex items-center justify-end gap-2 pt-2">
        <el-button v-if="dirty" plain :disabled="saving" @click="onReset">放弃修改</el-button>
        <el-button
          type="primary"
          :loading="saving"
          :disabled="!dirty || !auth.isAdmin"
          @click="onSave"
        >
          保存并热重载
        </el-button>
      </div>

      <div v-if="!auth.isAdmin" class="rounded-xl bg-amber-500/5 border border-amber-500/20 p-3 text-xs text-amber-200">
        <el-icon :size="14" class="align-text-top"><Warning /></el-icon>
        当前为只读模式，登录管理员后可启停 DNS。
      </div>
    </div>

    <!-- DNS 测试 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Search /></el-icon>
        <h3 class="text-base font-semibold">DNS 解析测试</h3>
      </div>
      <div class="flex items-end gap-2 flex-wrap">
        <div class="flex-1 min-w-[260px]">
          <label class="text-xs text-slate-400 mb-2 block">查询域名</label>
          <el-input
            v-model="testingDomain"
            placeholder="client-download.steampowered.com"
            :disabled="!auth.isAdmin"
            @keyup.enter="onTest"
          />
        </div>
        <el-button
          type="primary"
          :icon="Search"
          :loading="testRunning"
          :disabled="!auth.isAdmin"
          @click="onTest"
        >
          查询
        </el-button>
        <el-button :icon="Refresh" plain :disabled="testRunning" @click="dns.fetch()">刷统计</el-button>
      </div>

      <div v-if="dns.lastTest" class="rounded-xl bg-white/[.04] p-4 text-sm space-y-2">
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-slate-400">域名：</span>
          <code class="text-slate-200">{{ dns.lastTest.domain }}</code>
          <el-tag
            v-if="dns.lastTest.matched"
            type="success"
            size="small"
            effect="dark"
          >命中白名单</el-tag>
          <el-tag v-else size="small" effect="plain">未命中 → 走上游</el-tag>
          <span class="text-slate-500">→</span>
          <code class="text-mint">{{ dns.lastTest.server }}</code>
          <el-tag :type="dns.lastTest.rcode === 'NOERROR' ? 'success' : 'warning'" size="small" effect="dark">
            {{ dns.lastTest.rcode }}
          </el-tag>
          <span class="text-xs text-slate-500">{{ dns.lastTest.latencyMs }}ms</span>
        </div>
        <div v-if="dns.lastTest.answers.length > 0" class="space-y-1">
          <div v-for="(a, i) in dns.lastTest.answers" :key="i" class="font-mono text-xs text-slate-300">
            <span class="text-slate-500">{{ a.name }}</span>
            <span class="text-slate-500"> {{ a.ttl }} </span>
            <span class="text-mint">IN</span>
            <span class="text-slate-400"> A </span>
            <span class="text-slate-100">{{ a.data }}</span>
          </div>
        </div>
        <div v-if="dns.lastTest.error" class="text-xs text-rose-300 font-mono">
          {{ dns.lastTest.error }}
        </div>
      </div>
    </div>

    <!-- AppID 管理 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center justify-between flex-wrap gap-3">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#a3e635"><FolderOpened /></el-icon>
          <h3 class="text-base font-semibold">AppID 管理（常见服务端）</h3>
          <span class="text-xs text-slate-500">共 {{ steamcmd.total }} 条</span>
        </div>
        <div class="flex items-center gap-2">
          <el-button :icon="Refresh" size="small" plain :loading="steamcmd.loading" @click="steamcmd.fetch()">刷新</el-button>
          <el-button
            type="primary"
            :icon="Plus"
            size="small"
            :disabled="!auth.isAdmin"
            @click="openCreate"
          >
            新增
          </el-button>
        </div>
      </div>

      <div v-if="steamcmd.errorMessage" class="rounded-xl border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-300">
        {{ steamcmd.errorMessage }}
      </div>

      <div class="overflow-x-auto">
        <table class="w-full text-sm min-w-[900px]">
          <thead class="text-xs text-slate-500 border-b border-white/[.06]">
            <tr>
              <th class="text-left px-4 py-3 font-medium">AppID</th>
              <th class="text-left px-4 py-3 font-medium">名称</th>
              <th class="text-left px-4 py-3 font-medium">登录</th>
              <th class="text-left px-4 py-3 font-medium">安装目录</th>
              <th class="text-left px-4 py-3 font-medium">最近预热</th>
              <th class="text-right px-4 py-3 font-medium">缓存估算</th>
              <th class="text-left px-4 py-3 font-medium">状态</th>
              <th class="text-right px-4 py-3 font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="a in steamcmd.items"
              :key="a.id"
              class="border-b border-white/[.04] hover:bg-white/[.02]"
            >
              <td class="px-4 py-2 font-mono text-xs text-slate-200">{{ a.appId }}</td>
              <td class="px-4 py-2 text-slate-100">
                {{ a.name }}
                <el-tag v-if="!a.enabled" size="small" effect="plain" class="ml-2">disabled</el-tag>
              </td>
              <td class="px-4 py-2 text-xs">
                <el-tag :type="a.loginType === 'anonymous' ? 'success' : 'warning'" size="small" effect="dark">
                  {{ a.loginType }}
                </el-tag>
              </td>
              <td class="px-4 py-2 font-mono text-xs text-slate-400 truncate max-w-[180px]" :title="a.installDir">
                {{ a.installDir || '—' }}
              </td>
              <td class="px-4 py-2 text-xs text-slate-500 font-mono">
                {{ a.lastPreheatAt ? relTime(a.lastPreheatAt) : '—' }}
              </td>
              <td class="px-4 py-2 text-right text-xs text-slate-400 font-mono">
                {{ a.cacheBytesEstimate > 0 ? formatBytes(a.cacheBytesEstimate) : '—' }}
              </td>
              <td class="px-4 py-2">
                <el-tag
                  v-if="a.lastPreheatStatus"
                  :type="preheatStatusType(a.lastPreheatStatus)"
                  size="small"
                  effect="dark"
                >
                  {{ a.lastPreheatStatus }}
                </el-tag>
                <span v-else class="text-xs text-slate-500">—</span>
              </td>
              <td class="px-4 py-2 text-right space-x-1">
                <el-button
                  :icon="HotWater"
                  size="small"
                  type="primary"
                  plain
                  :disabled="!auth.isAdmin || a.lastPreheatStatus === 'running'"
                  :loading="preheatingId === a.id"
                  @click="onPreheat(a)"
                >
                  预热
                </el-button>
                <el-button
                  :icon="Edit"
                  size="small"
                  plain
                  :disabled="!auth.isAdmin"
                  @click="openEdit(a)"
                />
                <el-button
                  :icon="Delete"
                  size="small"
                  type="danger"
                  plain
                  :disabled="!auth.isAdmin"
                  @click="onDelete(a)"
                />
              </td>
            </tr>
            <tr v-if="!steamcmd.loading && steamcmd.items.length === 0">
              <td colspan="8" class="text-center text-slate-500 py-8 text-sm">暂无 AppID</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="!auth.isAdmin" class="rounded-xl bg-amber-500/5 border border-amber-500/20 p-3 text-xs text-amber-200">
        <el-icon :size="14" class="align-text-top"><Warning /></el-icon>
        只读模式，登录管理员后可新增 / 预热 / 删除 AppID。
      </div>
    </div>

    <!-- 新增 / 编辑 AppID 弹窗 -->
    <el-dialog
      v-model="dialogOpen"
      :title="editingId ? '编辑 AppID' : '新增 AppID'"
      width="460px"
      :close-on-click-modal="false"
    >
      <el-form :model="form" label-position="top">
        <el-form-item label="AppID（Steam 应用 ID）" required>
          <el-input
            v-model.number="form.appId"
            type="number"
            placeholder="2394010 (Palworld)"
            :disabled="!!editingId"
          />
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="Palworld Dedicated Server" />
        </el-form-item>
        <el-form-item label="登录方式">
          <el-radio-group v-model="form.loginType">
            <el-radio-button value="anonymous">anonymous</el-radio-button>
            <el-radio-button value="account">account</el-radio-button>
          </el-radio-group>
          <p class="text-xs text-slate-500 mt-1">
            anonymous 公开仓库可走预热；account 需要交互式登录，本模块仅展示。
          </p>
        </el-form-item>
        <el-form-item label="安装目录">
          <el-input v-model="form.installDir" placeholder="/data/steamapps/palworld" />
        </el-form-item>
        <el-form-item v-if="editingId" label="缓存估算（bytes）">
          <el-input-number v-model="form.cacheBytesEstimate" :min="0" :step="1048576" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="dialogSaving" @click="onSaveAppID">
          {{ editingId ? '保存' : '创建' }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 客户端接入流程 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Position /></el-icon>
        <h3 class="text-base font-semibold">客户端接入流程</h3>
      </div>

      <div class="space-y-5">
        <!-- 模式 A：DNS + Docker 包装 -->
        <div>
          <div class="flex items-center gap-2 mb-2">
            <span class="rounded-full bg-mint/20 text-mint text-xs px-2 py-0.5">模式 A</span>
            <span class="text-sm font-medium">DNS 劫持（推荐 · 单台 / 局域网多机）</span>
          </div>
          <pre class="text-xs font-mono text-slate-200 bg-black/40 p-4 rounded-xl overflow-x-auto whitespace-pre"><code>{{ modeACmd }}</code></pre>
          <div class="flex justify-end mt-2">
            <el-button
              size="small"
              :icon="copied === 'modeA' ? Check : CopyDocument"
              @click="copy(modeACmd, 'modeA')"
            >{{ copied === 'modeA' ? '已复制' : '复制命令' }}</el-button>
          </div>
        </div>

        <!-- 模式 B：LAN DNS 接管 -->
        <div>
          <div class="flex items-center gap-2 mb-2">
            <span class="rounded-full bg-violet/20 text-violet text-xs px-2 py-0.5">模式 B</span>
            <span class="text-sm font-medium">LAN DNS 接管（路由器 / DHCP 推送）</span>
          </div>
          <pre class="text-xs font-mono text-slate-200 bg-black/40 p-4 rounded-xl overflow-x-auto whitespace-pre"><code>{{ modeBLanCmd }}</code></pre>
          <div class="flex justify-end mt-2">
            <el-button
              size="small"
              :icon="copied === 'modeB' ? Check : CopyDocument"
              @click="copy(modeBLanCmd, 'modeB')"
            >{{ copied === 'modeB' ? '已复制' : '复制命令' }}</el-button>
          </div>
          <p class="text-xs text-slate-500 mt-2">
            提示：CNCacheHub 默认 5353 端口，路由器 DHCP 推送 DNS 时把 53 端口转发到 5353，
            或在路由器直接用 53（需要 CNCacheHub 用 root 启动）。小贴士：CNCacheHub 仅劫持白名单 Steam 域名，
            其他查询（baidu.com 等）正常转发上游 1.1.1.1。
          </p>
        </div>

        <!-- 已知限制 -->
        <div class="rounded-xl bg-amber-500/5 border border-amber-500/20 p-4 text-xs text-amber-200 space-y-1">
          <div class="font-medium flex items-center gap-1">
            <el-icon :size="14"><Warning /></el-icon>
            已知限制（PRD §9.3.5）
          </div>
          <ul class="list-disc pl-5 space-y-0.5 text-amber-100/80">
            <li>只加速用户有权下载的内容；不绕过 Steam 账号、订阅、付费或地区权限。</li>
            <li>私有 / 加密内容是否可缓存取决于 SteamCMD 实际请求和授权结果。</li>
            <li>HTTPS 流量无法在不做 MITM 的情况下缓存明文；本模块只做"把客户端请求导到 CNCacheHub IP"（后续可接 HAProxy 缓存网关）。</li>
            <li>首次安装需用户先在 SteamCMD 容器内跑一次完整下载，后续相同 AppID 命中缓存。</li>
          </ul>
        </div>
      </div>
    </div>
  </section>
</template>
