<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import StatusDot from '@/components/StatusDot.vue'
import { useHealthStore } from '@/stores/health'

const health = useHealthStore()

const apiBase = import.meta.env.VITE_API_BASE || '/api'

let timer: ReturnType<typeof setInterval> | null = null

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
    return '已连接 CNCacheHub 后端，可以开始生成客户端配置、查看缓存命中与请求日志。'
  }
  return health.errorMessage
    ? `未能访问后端 API：${health.errorMessage}。请确认 server 已启动并暴露在 :8080。`
    : '请确认后端服务已启动在 :8080（开发模式由 Vite 代理转发 /api）。'
})

onMounted(() => {
  health.fetch()
  // Phase 0 暂时用 30s 轮询，后续接 WebSocket
  timer = setInterval(() => {
    health.fetch()
  }, 30_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

function refresh(): void {
  health.fetch()
}
</script>

<template>
  <div class="space-y-5">
    <!-- 欢迎区 -->
    <section
      class="soft rounded-[2rem] p-7 relative overflow-hidden"
    >
      <div class="relative flex flex-col lg:flex-row lg:items-center lg:justify-between gap-6">
        <div class="max-w-2xl">
          <div
            class="inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs"
            :class="health.backendConnected
              ? 'border-mint/25 bg-mint/10 text-mint'
              : 'border-rose-300/25 bg-rose-300/10 text-rose-300'"
          >
            <StatusDot :status="dotStatus" size="sm" :pulse="true" />
            <span v-if="health.backendConnected">全部核心服务运行中</span>
            <span v-else>后端未连接</span>
          </div>
          <h2 class="mt-5 text-3xl lg:text-4xl font-semibold leading-tight tracking-tight">
            {{ welcomeTitle }}
          </h2>
          <p class="mt-4 text-slate-300 leading-7">
            {{ welcomeSubtitle }}
          </p>
          <div class="mt-6 flex flex-wrap gap-3">
            <button
              type="button"
              class="btn rounded-2xl bg-mint px-5 py-3 text-sm font-semibold text-ink shadow-glow transition"
              @click="$router.push('/clients')"
            >
              生成 Docker 配置
            </button>
            <button
              type="button"
              class="btn rounded-2xl bg-white/[.08] px-5 py-3 text-sm text-slate-100 hover:bg-white/[.12] transition"
              @click="$router.push('/steamcmd')"
            >
              启动 SteamCMD 向导
            </button>
            <button
              type="button"
              class="btn rounded-2xl bg-white/[.04] px-5 py-3 text-sm text-slate-300 hover:bg-white/[.08] transition"
              @click="refresh"
              :disabled="health.loading"
            >
              {{ health.loading ? '刷新中…' : '刷新状态' }}
            </button>
          </div>
        </div>

        <div
          class="w-full lg:w-72 rounded-[1.5rem] border border-white/[.10] bg-black/30 p-4"
        >
          <div class="text-xs text-slate-400">后端 API</div>
          <div class="mt-2 text-sm font-medium text-mint break-all">
            {{ health.backendConnected ? `${apiBase}/healthz` : '—' }}
          </div>
          <div class="mt-4 h-2 rounded-full bg-slate-800 overflow-hidden">
            <div
              class="h-full rounded-full"
              :class="health.backendConnected ? 'bg-gradient-to-r from-mint to-violet' : 'bg-slate-700'"
              :style="{ width: health.backendConnected ? '82%' : '8%' }"
            />
          </div>
          <div class="mt-2 flex justify-between text-xs text-slate-400">
            <span>连接质量</span>
            <span>{{ health.backendConnected ? '正常' : '断开' }}</span>
          </div>
        </div>
      </div>
    </section>

    <!-- 4 个 metric 卡片 -->
    <section class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
      <div class="soft rounded-3xl p-5">
        <div class="text-sm text-slate-400">后端状态</div>
        <div class="mt-3 flex items-center gap-2 text-2xl font-semibold">
          <StatusDot :status="dotStatus" size="md" :pulse="true" />
          <span :class="health.backendConnected ? 'text-mint' : 'text-rose-300'">
            {{ health.backendConnected ? '在线' : '离线' }}
          </span>
        </div>
        <div class="mt-2 text-xs text-slate-500">
          {{ health.backendConnected ? '健康检查通过' : (health.errorMessage || '等待后端响应') }}
        </div>
      </div>
      <div class="soft rounded-3xl p-5">
        <div class="text-sm text-slate-400">当前版本</div>
        <div class="mt-3 text-2xl font-semibold">{{ health.version || '—' }}</div>
        <div class="mt-2 text-xs text-slate-500">
          Go {{ health.goVersion }} · {{ health.commit || 'dev' }}
        </div>
      </div>
      <div class="soft rounded-3xl p-5">
        <div class="text-sm text-slate-400">数据库</div>
        <div class="mt-3 text-2xl font-semibold">{{ health.db || '—' }}</div>
        <div class="mt-2 text-xs text-slate-500">SQLite (modernc)</div>
      </div>
      <div class="soft rounded-3xl p-5">
        <div class="text-sm text-slate-400">启动时间</div>
        <div class="mt-3 text-2xl font-semibold">{{ health.uptime || '—' }}</div>
        <div class="mt-2 text-xs text-slate-500">
          上次检查 {{ health.lastCheckedAt ? new Date(health.lastCheckedAt).toLocaleTimeString('zh-CN') : '尚未检查' }}
        </div>
      </div>
    </section>

    <!-- 开发进度 -->
    <section class="soft rounded-[2rem] p-6">
      <div class="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h3 class="text-lg font-semibold">当前阶段 · Phase 0 项目骨架</h3>
          <p class="mt-1 text-sm text-slate-400">
            前端 Vue 3 控制台骨架已就绪，下一步进入 Phase 1 — Docker 加速 MVP。
          </p>
        </div>
        <div class="flex gap-2 text-xs text-slate-400">
          <span class="rounded-full border border-mint/30 bg-mint/10 px-3 py-1 text-mint">Phase 0</span>
          <span class="rounded-full border border-white/10 px-3 py-1">Phase 1</span>
          <span class="rounded-full border border-white/10 px-3 py-1">Phase 2</span>
          <span class="rounded-full border border-white/10 px-3 py-1">Phase 3</span>
        </div>
      </div>

      <div class="mt-5 grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 text-sm">
        <div class="rounded-3xl border border-mint/20 bg-mint/10 p-4">
          <div class="text-mint font-medium">Phase 0 · 项目骨架</div>
          <ul class="mt-3 space-y-2 text-slate-300 list-disc list-inside">
            <li>完整 PRD (docs/prd.md)</li>
            <li>高保真 HTML 原型 (prototype/index.html)</li>
            <li>仓库 monorepo 目录 + AGENTS.md</li>
            <li>Go server 骨架（进行中）</li>
            <li>Vue 3 web 骨架（<b class="text-mint">本任务</b>）</li>
            <li>Docker Compose 部署脚本</li>
          </ul>
        </div>
        <div class="rounded-3xl bg-white/[.05] p-4">
          <div class="text-slate-200 font-medium">Phase 1 · Docker 加速 MVP</div>
          <ul class="mt-3 space-y-2 text-slate-400 list-disc list-inside">
            <li>Registry pull-through cache 代理</li>
            <li>Docker daemon.json 配置生成</li>
            <li>缓存条目元数据</li>
            <li>基础请求日志</li>
            <li>总览仪表盘真实数据</li>
          </ul>
        </div>
        <div class="rounded-3xl bg-white/[.05] p-4">
          <div class="text-slate-200 font-medium">Phase 2 · 扩展模块</div>
          <ul class="mt-3 space-y-2 text-slate-400 list-disc list-inside">
            <li>SteamCMD 缓存</li>
            <li>多 Registry（GHCR / Quay / k8s）</li>
            <li>资源加速中心 + 诊断中心</li>
            <li>定时清理 + 小容量 VPS 优化</li>
          </ul>
        </div>
      </div>
    </section>

    <!-- 底部说明 -->
    <section class="text-xs text-slate-500 text-center py-3">
      Phase 0 骨架 · 后续接入真实数据 · API 文档见 docs/prd.md
    </section>
  </div>
</template>
