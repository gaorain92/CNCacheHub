<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Lightning, MagicStick, Search } from '@element-plus/icons-vue'
import StatusDot from './StatusDot.vue'
import { useHealthStore } from '@/stores/health'

const route = useRoute()
const router = useRouter()

const health = useHealthStore()

const pageTitle = computed(() => (route.meta?.title as string | undefined) ?? 'CNCacheHub')

const envLabel = computed(() => '开发环境 / 本机节点')

const dotStatus = computed(() => {
  if (health.loading && health.lastCheckedAt === 0) return 'unknown' as const
  if (health.status === 'ok' && health.backendConnected) return 'ok' as const
  if (health.status === 'down') return 'down' as const
  if (!health.backendConnected) return 'down' as const
  return 'warn' as const
})

function goDiagnostics(): void {
  router.push('/diagnostics')
}

function goClients(): void {
  router.push('/clients')
}
</script>

<template>
  <header
    class="sticky top-5 z-10 mb-6 glass rounded-[2rem] px-5 py-4 flex items-center justify-between gap-4"
  >
    <div class="min-w-0">
      <div class="text-xs text-slate-400 flex items-center gap-2">
        <span>{{ envLabel }}</span>
        <span class="hidden sm:inline">·</span>
        <StatusDot :status="dotStatus" size="sm" :pulse="true" />
        <span v-if="health.backendConnected" class="text-mint">后端已连接</span>
        <span v-else class="text-rose-300">后端未连接</span>
      </div>
      <h1 class="mt-1 text-2xl font-semibold tracking-tight truncate">
        {{ pageTitle }}
      </h1>
    </div>

    <div class="flex items-center gap-3 shrink-0">
      <div
        class="hidden xl:flex items-center gap-2 rounded-2xl bg-white/[.05] px-4 py-2 text-sm text-slate-300 min-w-[16rem]"
      >
        <el-icon :size="14" class="text-slate-500"><Search /></el-icon>
        <span class="text-slate-500">搜索镜像 / AppID / 日志 / IP</span>
      </div>
      <button
        type="button"
        class="btn rounded-2xl bg-white/[.06] px-4 py-2 text-sm hover:bg-white/[.10] transition flex items-center gap-2"
        @click="goDiagnostics"
      >
        <el-icon :size="14" color="#a3e635"><Lightning /></el-icon>
        快速诊断
      </button>
      <button
        type="button"
        class="btn rounded-2xl bg-gradient-to-r from-mint to-violet px-4 py-2 text-sm font-semibold text-ink shadow-glow transition flex items-center gap-2"
        @click="goClients"
      >
        <el-icon :size="14"><MagicStick /></el-icon>
        生成配置
      </button>
    </div>
  </header>
</template>
