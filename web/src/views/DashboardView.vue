<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import {
  DataLine,
  FolderOpened,
  Lightning,
  Promotion,
  Refresh,
  Warning,
} from '@element-plus/icons-vue'
import StatusDot from '@/components/StatusDot.vue'
import { useHealthStore } from '@/stores/health'
import { useDashboardStore } from '@/stores/dashboard'

const health = useHealthStore()
const dashboard = useDashboardStore()

const dotStatus = computed(() => {
  if (health.status === 'ok' && health.backendConnected) return 'ok' as const
  if (health.status === 'down') return 'down' as const
  if (!health.backendConnected) return 'down' as const
  return 'unknown' as const
})

const welcomeTitle = computed(() => {
  if (health.backendConnected) return '自托管下载加速中枢已上线'
  return '后端尚未连接'
})

const welcomeSubtitle = computed(() => {
  if (health.backendConnected) {
    return '已连接 CNCacheHub 后端，可查看实时命中率、上游状态与请求日志。'
  }
  return health.errorMessage
    ? `未能访问后端 API：${health.errorMessage}。请确认 server 已启动并暴露在 :8080。`
    : '请确认后端服务已启动在 :8080（开发模式由 Vite 代理转发 /api）。'
})

let timer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  await health.fetch()
  await dashboard.fetch()
  timer = setInterval(() => {
    health.fetch()
    dashboard.fetch()
  }, 15_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

async function refresh(): Promise<void> {
  await Promise.all([health.fetch(), dashboard.fetch()])
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-semibold">{{ welcomeTitle }}</h1>
        <p class="text-sm text-slate-400 mt-1">{{ welcomeSubtitle }}</p>
      </div>
      <div class="flex items-center gap-3">
        <StatusDot :status="dotStatus" />
        <el-button :icon="Refresh" size="small" plain @click="refresh">刷新</el-button>
      </div>
    </header>

    <!-- 健康卡片 -->
    <div class="grid grid-cols-1 md:grid-cols-4 gap-4">
      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">后端状态</div>
        <div class="text-2xl font-semibold" :class="health.backendConnected ? 'text-mint' : 'text-rose-400'">
          {{ health.backendConnected ? '在线' : '离线' }}
        </div>
        <div class="text-xs text-slate-500 mt-2">uptime {{ health.uptime }}</div>
      </div>

      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">缓存条目</div>
        <div class="text-2xl font-semibold text-slate-100">
          {{ dashboard.data?.cacheEntries ?? '—' }}
        </div>
        <div class="text-xs text-slate-500 mt-2">
          <el-icon :size="12"><FolderOpened /></el-icon>
          {{ dashboard.cacheBytesHuman }}
        </div>
      </div>

      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">命中率 · 24h</div>
        <div class="text-2xl font-semibold text-slate-100">
          {{ dashboard.hitRate.toFixed(1) }}%
        </div>
        <div class="text-xs text-slate-500 mt-2">
          hit {{ dashboard.data?.hitCount ?? 0 }} · miss {{ dashboard.data?.missCount ?? 0 }}
        </div>
      </div>

      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">24h 流量</div>
        <div class="text-2xl font-semibold text-slate-100">
          {{ dashboard.bytesOut24hHuman }}
        </div>
        <div class="text-xs text-slate-500 mt-2">
          {{ dashboard.data?.requestCount24h ?? 0 }} requests
        </div>
      </div>
    </div>

    <!-- 错误 / 异常 -->
    <div v-if="(dashboard.data?.errorCount24h ?? 0) > 0" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 flex items-start gap-3">
      <el-icon :size="20" color="#f59e0b"><Warning /></el-icon>
      <div class="text-sm text-amber-200">
        过去 24h 有 <strong>{{ dashboard.data?.errorCount24h }}</strong> 个 4xx/5xx 请求。前往
        <router-link to="/logs" class="underline">访问日志</router-link>
        查看详情。
      </div>
    </div>

    <!-- 路由入口 -->
    <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
      <router-link
        to="/docker"
        class="rounded-2xl border border-white/[.08] bg-black/20 p-5 hover:border-mint/30 transition group"
      >
        <el-icon :size="22" color="#94a3b8" class="mb-2 group-hover:text-mint"><Promotion /></el-icon>
        <div class="text-sm font-medium">Docker 加速</div>
        <div class="text-xs text-slate-500 mt-1">生成 daemon.json · 客户端接入</div>
      </router-link>

      <router-link
        to="/logs"
        class="rounded-2xl border border-white/[.08] bg-black/20 p-5 hover:border-mint/30 transition group"
      >
        <el-icon :size="22" color="#94a3b8" class="mb-2 group-hover:text-mint"><DataLine /></el-icon>
        <div class="text-sm font-medium">访问日志</div>
        <div class="text-xs text-slate-500 mt-1">实时请求 · 命中状态 · 错误诊断</div>
      </router-link>

      <router-link
        to="/cache"
        class="rounded-2xl border border-white/[.08] bg-black/20 p-5 hover:border-mint/30 transition group"
      >
        <el-icon :size="22" color="#94a3b8" class="mb-2 group-hover:text-mint"><Lightning /></el-icon>
        <div class="text-sm font-medium">缓存管理</div>
        <div class="text-xs text-slate-500 mt-1">条目浏览 · 手动清理（Phase 1.2）</div>
      </router-link>
    </div>
  </section>
</template>
