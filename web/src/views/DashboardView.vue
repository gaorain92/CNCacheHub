<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import {
  Brush,
  Connection,
  DataLine,
  Delete,
  Lightning,
  Promotion,
  Refresh,
  Timer,
  Warning,
} from '@element-plus/icons-vue'
import StatusDot from '@/components/StatusDot.vue'
import { useHealthStore } from '@/stores/health'
import { useDashboardStore } from '@/stores/dashboard'
import { useUpstreamStore } from '@/stores/upstream'

const health = useHealthStore()
const dashboard = useDashboardStore()
const upstream = useUpstreamStore()

const dotStatus = computed(() => {
  if (health.status === 'ok' && health.backendConnected) return 'ok' as const
  if (health.status === 'down') return 'down' as const
  if (!health.backendConnected) return 'down' as const
  return 'unknown' as const
})

const upstreamStatus = computed<'ok' | 'down' | 'unknown'>(() => {
  if (!upstream.data) return 'unknown'
  return upstream.data.reachable ? 'ok' : 'down'
})

const welcomeTitle = computed(() => {
  if (health.backendConnected) return '自托管下载加速中枢已上线'
  return '后端尚未连接'
})

const welcomeSubtitle = computed(() => {
  if (health.backendConnected) {
    return '已连接 CNCacheHub 后端，可查看实时命中率、上游状态与各模块运行情况。'
  }
  return health.errorMessage
    ? `未能访问后端 API：${health.errorMessage}。请确认 server 进程已启动。`
    : '等待后端连接（生产端口 8082；开发模式由 Vite 代理转发 /api）。'
})

let timer: ReturnType<typeof setInterval> | null = null

function fetchAll(): void {
  health.fetch()
  dashboard.fetchAll()
  upstream.fetch()
}

onMounted(() => {
  fetchAll()
  timer = setInterval(fetchAll, 15_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

async function refresh(): Promise<void> {
  await Promise.all([health.fetch(), dashboard.fetchAll(), upstream.fetch()])
}

// ---- 时间格式化 helpers ----
function formatTimeAgo(unixSec: number | null | undefined): string {
  if (!unixSec) return '—'
  const diff = Math.floor(Date.now() / 1000) - unixSec
  if (diff < 60) return `${diff}s 前`
  if (diff < 3600) return `${Math.floor(diff / 60)}m 前`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h 前`
  return `${Math.floor(diff / 86400)}d 前`
}

function formatTime(unixSec: number | null | undefined): string {
  if (!unixSec) return '—'
  const d = new Date(unixSec * 1000)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function statusClass(status: number): string {
  if (status >= 500) return 'text-red-400'
  if (status >= 400) return 'text-amber-400'
  if (status >= 300) return 'text-sky-400'
  if (status >= 200) return 'text-mint'
  return 'text-slate-400'
}

function methodColor(method: string): string {
  switch (method) {
    case 'GET': return 'text-sky-400'
    case 'POST': return 'text-mint'
    case 'PUT': case 'PATCH': return 'text-amber-400'
    case 'DELETE': return 'text-red-400'
    default: return 'text-slate-400'
  }
}

const moduleCard = 'rounded-xl border border-white/[.08] bg-black/20 p-4 hover:border-mint/30 transition'
const statLabel = 'text-xs text-slate-500 mb-1.5'
const statValue = 'text-xl font-semibold text-slate-100'
const subText = 'text-[11px] text-slate-500 mt-1.5'

// preheat lastRun 状态映射
const preheatStatusText: Record<string, { text: string; cls: string }> = {
  done:     { text: '成功',   cls: 'text-mint' },
  error:    { text: '失败',   cls: 'text-red-400' },
  running:  { text: '运行中', cls: 'text-sky-400' },
  canceled: { text: '取消',   cls: 'text-slate-400' },
  pending:  { text: '排队',   cls: 'text-slate-400' },
}
function preheatStatus(s: string) {
  return preheatStatusText[s] || { text: s, cls: 'text-slate-400' }
}
</script>

<template>
  <section class="space-y-6">
    <!-- 顶栏 -->
    <header class="flex items-center justify-between">
      <div>
        <h2 class="text-lg font-semibold text-slate-100">{{ welcomeTitle }}</h2>
        <p class="text-sm text-slate-400 mt-0.5">{{ welcomeSubtitle }}</p>
      </div>
      <div class="flex items-center gap-3">
        <StatusDot :status="dotStatus" />
        <el-button :icon="Refresh" size="small" plain :loading="dashboard.loading" @click="refresh">
          刷新
        </el-button>
      </div>
    </header>

    <!-- 健康卡片 — 顶层 5 项 -->
    <div class="grid grid-cols-2 md:grid-cols-5 gap-3">
      <div class="rounded-xl border border-white/[.08] bg-black/20 p-4">
        <div :class="statLabel">后端状态</div>
        <div class="text-xl font-semibold" :class="health.backendConnected ? 'text-mint' : 'text-slate-400'">
          {{ health.backendConnected ? '在线' : '离线' }}
        </div>
        <div :class="subText">uptime {{ health.uptime }} · v{{ health.version }}</div>
      </div>

      <div class="rounded-xl border border-white/[.08] bg-black/20 p-4">
        <div :class="statLabel">上游 Registry</div>
        <div class="text-xl font-semibold flex items-center gap-2" :class="upstreamStatus === 'ok' ? 'text-mint' : 'text-slate-400'">
          <el-icon :size="16"><Connection /></el-icon>
          {{ upstreamStatus === 'ok' ? '可达' : upstreamStatus === 'down' ? '不可达' : '检测中' }}
        </div>
        <div :class="subText">
          <span v-if="upstream.data">{{ upstream.data.latencyMs }}ms · {{ upstream.data.url.replace('https://', '').replace('http://', '') }}</span>
          <span v-else>等待首次检测…</span>
        </div>
      </div>

      <div class="rounded-xl border border-white/[.08] bg-black/20 p-4">
        <div :class="statLabel">缓存条目</div>
        <div :class="statValue">{{ dashboard.data?.cacheEntries ?? '—' }}</div>
        <div :class="subText">
          {{ dashboard.cacheBytesHuman }}
          <span v-if="(dashboard.data?.bypassedCount ?? 0) > 0"> · {{ dashboard.data?.bypassedCount }} 旁路</span>
        </div>
      </div>

      <div class="rounded-xl border border-white/[.08] bg-black/20 p-4">
        <div :class="statLabel">命中率 · 24h</div>
        <div :class="statValue">
          <span :class="dashboard.hitRate > 50 ? 'text-mint' : dashboard.hitRate > 0 ? 'text-amber-400' : 'text-slate-400'">
            {{ dashboard.hitRate.toFixed(1) }}%
          </span>
        </div>
        <div :class="subText">
          hit {{ dashboard.data?.hitCount ?? 0 }} · miss {{ dashboard.data?.missCount ?? 0 }}
        </div>
      </div>

      <div class="rounded-xl border border-white/[.08] bg-black/20 p-4">
        <div :class="statLabel">24h 流量</div>
        <div :class="statValue">{{ dashboard.bytesOut24hHuman }}</div>
        <div :class="subText">
          {{ dashboard.data?.requestCount24h ?? 0 }} requests
          <span v-if="(dashboard.data?.errorCount24h ?? 0) > 0" class="text-amber-400">· {{ dashboard.data?.errorCount24h }} 错</span>
        </div>
      </div>
    </div>

    <!-- 业务模块概览 — 6 个模块 -->
    <div>
      <h3 class="text-sm font-medium text-slate-300 mb-3 flex items-center gap-2">
        <el-icon><Brush /></el-icon>
        业务模块
      </h3>
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        <!-- Docker 代理 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">Docker 代理</div>
            <el-icon class="text-slate-500"><Promotion /></el-icon>
          </div>
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div>
              <div class="text-[11px] text-slate-500">活跃 upstream</div>
              <div :class="['text-base font-semibold', (dashboard.data?.activeUpstreams ?? 0) > 0 ? 'text-mint' : 'text-slate-500']">
                {{ dashboard.data?.activeUpstreams ?? 0 }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">缓存条目</div>
              <div class="text-base font-semibold text-slate-200">{{ dashboard.data?.cacheEntries ?? 0 }}</div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">24h 命中</div>
              <div class="text-base font-semibold text-slate-200">{{ dashboard.data?.hitCount ?? 0 }}</div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">24h 命中率</div>
              <div :class="['text-base font-semibold', dashboard.hitRate > 50 ? 'text-mint' : 'text-amber-400']">
                {{ dashboard.hitRate.toFixed(0) }}%
              </div>
            </div>
          </div>
        </div>

        <!-- DNS 服务 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">DNS 服务</div>
            <el-icon class="text-slate-500"><Connection /></el-icon>
          </div>
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div>
              <div class="text-[11px] text-slate-500">状态</div>
              <div :class="['text-base font-semibold', dashboard.dnsStats ? 'text-mint' : 'text-slate-500']">
                {{ dashboard.dnsStats ? '运行中' : '未启动' }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">白名单规则</div>
              <div class="text-base font-semibold text-slate-200">8</div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">24h 查询</div>
              <div class="text-base font-semibold text-slate-200">
                {{ (dashboard.dnsStats?.totalQueries ?? 0).toLocaleString() }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">命中率</div>
              <div :class="['text-base font-semibold', dashboard.dnsHitRate > 0 ? 'text-mint' : 'text-slate-500']">
                {{ dashboard.dnsHitRate.toFixed(0) }}%
              </div>
            </div>
          </div>
          <div v-if="dashboard.dnsStats?.lastError" class="mt-2 text-[11px] text-amber-400 truncate">
            ⚠ {{ dashboard.dnsStats.lastError }}
          </div>
        </div>

        <!-- 预热任务 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">预热任务</div>
            <el-icon class="text-slate-500"><Lightning /></el-icon>
          </div>
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div>
              <div class="text-[11px] text-slate-500">活跃 / 总数</div>
              <div class="text-base font-semibold text-slate-200">
                {{ dashboard.preheatActiveCount }} / {{ dashboard.preheat.length }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">运行中</div>
              <div :class="['text-base font-semibold', dashboard.preheatRunning.length > 0 ? 'text-sky-400' : 'text-slate-500']">
                {{ dashboard.preheatRunning.length }}
              </div>
            </div>
          </div>
          <div v-if="dashboard.preheatLastRun" class="mt-2 pt-2 border-t border-white/5 text-[11px]">
            <div class="text-slate-400 truncate">
              最近：<span class="text-slate-200">{{ dashboard.preheatLastRun.name }}</span>
            </div>
            <div class="text-slate-500 mt-0.5 flex items-center gap-2">
              <span :class="preheatStatus(dashboard.preheatLastRun.status).cls">
                {{ preheatStatus(dashboard.preheatLastRun.status).text }}
              </span>
              <span>·</span>
              <span>{{ formatTimeAgo(dashboard.preheatLastRun.lastRunAt) }}</span>
              <span v-if="dashboard.preheatLastRun.lastDurationMs > 0">·</span>
              <span v-if="dashboard.preheatLastRun.lastDurationMs > 0">{{ (dashboard.preheatLastRun.lastDurationMs / 1000).toFixed(1) }}s</span>
            </div>
          </div>
          <div v-else class="mt-2 pt-2 border-t border-white/5 text-[11px] text-slate-500">
            尚未运行
          </div>
        </div>

        <!-- 清理任务 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">清理任务</div>
            <el-icon class="text-slate-500"><Delete /></el-icon>
          </div>
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div>
              <div class="text-[11px] text-slate-500">活跃 / 总数</div>
              <div class="text-base font-semibold text-slate-200">
                {{ dashboard.cleanupActiveCount }} / {{ dashboard.cleanup.length }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">最近清理</div>
              <div class="text-base font-semibold text-slate-200">
                {{ dashboard.bytesHuman(dashboard.cleanupLastRun?.lastFreedBytes) }}
              </div>
            </div>
          </div>
          <div v-if="dashboard.cleanupLastRun" class="mt-2 pt-2 border-t border-white/5 text-[11px]">
            <div class="text-slate-400 truncate">
              最近：<span class="text-slate-200">{{ dashboard.cleanupLastRun.name }}</span>
            </div>
            <div class="text-slate-500 mt-0.5 flex items-center gap-2">
              <span :class="dashboard.cleanupLastRun.lastStatus === 'ok' ? 'text-mint' : 'text-amber-400'">
                {{ dashboard.cleanupLastRun.lastStatus || '—' }}
              </span>
              <span>·</span>
              <span>{{ formatTimeAgo(dashboard.cleanupLastRun.lastRunAt) }}</span>
              <span v-if="dashboard.cleanupLastRun.lastFreedCount > 0">·</span>
              <span v-if="dashboard.cleanupLastRun.lastFreedCount > 0">{{ dashboard.cleanupLastRun.lastFreedCount }} 个对象</span>
            </div>
          </div>
          <div v-else class="mt-2 pt-2 border-t border-white/5 text-[11px] text-slate-500">
            尚未运行
          </div>
        </div>

        <!-- 资源加速 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">资源加速</div>
            <el-icon class="text-slate-500"><DataLine /></el-icon>
          </div>
          <div class="grid grid-cols-2 gap-2 text-sm">
            <div>
              <div class="text-[11px] text-slate-500">启用 / 总数</div>
              <div class="text-base font-semibold text-slate-200">
                {{ dashboard.resourcesEnabledCount }} / {{ dashboard.resources.length }}
              </div>
            </div>
            <div>
              <div class="text-[11px] text-slate-500">覆盖平台</div>
              <div class="text-base font-semibold text-slate-200">
                {{ new Set(dashboard.resources.map((r) => r.kind)).size }}
              </div>
            </div>
          </div>
          <div v-if="dashboard.resources.length > 0" class="mt-2 pt-2 border-t border-white/5 text-[11px] text-slate-500 flex flex-wrap gap-1">
            <span v-for="r in dashboard.resources.slice(0, 4)" :key="r.id"
                  class="px-1.5 py-0.5 rounded bg-white/5 text-slate-400"
                  :class="r.enabled ? '' : 'opacity-40'">
              {{ r.name }}
            </span>
            <span v-if="dashboard.resources.length > 4" class="text-slate-600">+{{ dashboard.resources.length - 4 }}</span>
          </div>
        </div>

        <!-- 系统状态 -->
        <div :class="moduleCard">
          <div class="flex items-center justify-between mb-2">
            <div class="text-sm font-medium text-slate-200">系统</div>
            <el-icon class="text-slate-500"><Timer /></el-icon>
          </div>
          <div class="space-y-1.5 text-[11px] text-slate-400">
            <div class="flex justify-between">
              <span class="text-slate-500">运行时</span>
              <span class="text-slate-200 font-mono">go {{ health.goVersion || '—' }}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-slate-500">数据库</span>
              <span :class="health.db === 'ok' ? 'text-mint' : 'text-amber-400'">
                {{ health.db || '—' }}
              </span>
            </div>
            <div class="flex justify-between">
              <span class="text-slate-500">版本</span>
              <span class="text-slate-200 font-mono">v{{ health.version }} · {{ health.commit?.slice(0, 7) || '—' }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 最近活动 -->
    <div v-if="dashboard.recentLogs.length > 0">
      <h3 class="text-sm font-medium text-slate-300 mb-3 flex items-center gap-2">
        <el-icon><Timer /></el-icon>
        最近活动
        <span class="text-xs text-slate-500 ml-1">· 最近 8 条请求</span>
      </h3>
      <div class="rounded-xl border border-white/[.08] bg-black/20 overflow-hidden">
        <table class="w-full text-sm">
          <thead class="text-[11px] text-slate-500 uppercase border-b border-white/5">
            <tr>
              <th class="text-left px-4 py-2 font-medium">时间</th>
              <th class="text-left px-4 py-2 font-medium w-16">方法</th>
              <th class="text-left px-4 py-2 font-medium">路径</th>
              <th class="text-right px-4 py-2 font-medium w-16">状态</th>
              <th class="text-right px-4 py-2 font-medium w-20">耗时</th>
              <th class="text-right px-4 py-2 font-medium w-20">大小</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="log in dashboard.recentLogs" :key="log.id" class="border-b border-white/[.03] last:border-b-0 hover:bg-white/[.02]">
              <td class="px-4 py-1.5 text-slate-500 font-mono text-[11px]">{{ formatTime(log.createdAt) }}</td>
              <td class="px-4 py-1.5 font-mono text-[11px]" :class="methodColor(log.method)">{{ log.method }}</td>
              <td class="px-4 py-1.5 text-slate-300 font-mono text-[11px] truncate max-w-0" :title="log.path">{{ log.path }}</td>
              <td class="px-4 py-1.5 text-right font-mono text-[11px]" :class="statusClass(log.status)">
                <span v-if="log.cached" title="缓存命中" class="text-mint mr-1">●</span>
                <span v-else-if="log.bypassed" :title="`旁路: ${log.bypassReason}`" class="text-amber-400 mr-1">●</span>
                {{ log.status }}
              </td>
              <td class="px-4 py-1.5 text-right text-slate-500 text-[11px]">{{ log.durationMs }}ms</td>
              <td class="px-4 py-1.5 text-right text-slate-500 text-[11px]">{{ dashboard.bytesHuman(log.bytes) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 错误告警 -->
    <div v-if="(dashboard.data?.errorCount24h ?? 0) > 0" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 flex items-start gap-3">
      <el-icon :size="20" color="#f59e0b"><Warning /></el-icon>
      <div class="text-sm text-amber-200">
        过去 24h 有 <strong>{{ dashboard.data?.errorCount24h }}</strong> 个 4xx/5xx 请求。前往
        <router-link to="/logs" class="underline">访问日志</router-link>
        查看详情。
      </div>
    </div>

    <!-- 路由入口 -->
    <div>
      <h3 class="text-sm font-medium text-slate-300 mb-3">快捷入口</h3>
      <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <router-link to="/docker" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><Promotion /></el-icon>
          <div class="text-sm font-medium">Docker</div>
          <div class="text-[11px] text-slate-500">daemon.json · registry</div>
        </router-link>
        <router-link to="/preheat" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><Lightning /></el-icon>
          <div class="text-sm font-medium">预热</div>
          <div class="text-[11px] text-slate-500">任务 · 进度 · 取消</div>
        </router-link>
        <router-link to="/cache" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><DataLine /></el-icon>
          <div class="text-sm font-medium">缓存</div>
          <div class="text-[11px] text-slate-500">条目 · 清理 · 删除</div>
        </router-link>
        <router-link to="/logs" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><Timer /></el-icon>
          <div class="text-sm font-medium">日志</div>
          <div class="text-[11px] text-slate-500">访问 · 错误诊断</div>
        </router-link>
        <router-link to="/diagnostics" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><CircleCheck /></el-icon>
          <div class="text-sm font-medium">诊断</div>
          <div class="text-[11px] text-slate-500">健康检查 · bundle</div>
        </router-link>
        <router-link to="/resources" :class="[moduleCard, 'flex flex-col items-start gap-1']">
          <el-icon :size="18" color="#94a3b8" class="group-hover:text-mint"><Brush /></el-icon>
          <div class="text-sm font-medium">资源</div>
          <div class="text-[11px] text-slate-500">规则 · 缓存</div>
        </router-link>
      </div>
    </div>
  </section>
</template>
