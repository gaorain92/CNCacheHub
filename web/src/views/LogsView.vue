<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { useLogsStore } from '@/stores/logs'

const logs = useLogsStore()

onMounted(() => {
  if (logs.items.length === 0) logs.fetch()
})

const totalPages = computed(() => Math.max(1, Math.ceil(logs.total / logs.pageSize)))

function statusType(status: number): 'success' | 'warning' | 'danger' | 'info' {
  if (status >= 500) return 'danger'
  if (status >= 400) return 'warning'
  if (status >= 300) return 'info'
  return 'success'
}

// 命中状态 tag：HIT / BYPASS 细分（PRD §9.6.4 BYPASS_SIZE_LIMIT）
function bypassLabel(reason: string): string {
  if (reason === 'size_limit') return 'BYPASS_SIZE_LIMIT'
  if (reason === 'disk_low') return 'BYPASS_DISK_LOW'
  return 'BYPASS'
}

function bypassType(reason: string): 'warning' | 'danger' {
  // size_limit 黄色（可恢复），disk_low 红色（磁盘问题）
  return reason === 'disk_low' ? 'danger' : 'warning'
}

function shortPath(p: string): string {
  if (p.length <= 60) return p
  return p.slice(0, 30) + '…' + p.slice(-25)
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between">
      <div>
        <h2 class="text-2xl font-semibold">访问日志</h2>
        <p class="text-sm text-slate-400">最近 {{ logs.total }} 条 · 每页 {{ logs.pageSize }}</p>
      </div>
      <el-button :icon="Refresh" size="small" plain @click="logs.fetch()">刷新</el-button>
    </header>

    <div v-if="logs.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      加载失败：{{ logs.errorMessage }}
    </div>

    <div class="rounded-2xl border border-white/[.08] bg-black/20 overflow-x-auto">
      <table class="w-full text-sm min-w-[800px]">
        <thead class="text-xs text-slate-500 border-b border-white/[.06]">
          <tr>
            <th class="text-left px-4 py-3 font-medium">方法</th>
            <th class="text-left px-4 py-3 font-medium">路径</th>
            <th class="text-left px-4 py-3 font-medium">状态</th>
            <th class="text-right px-4 py-3 font-medium">耗时</th>
            <th class="text-right px-4 py-3 font-medium">大小</th>
            <th class="text-left px-4 py-3 font-medium">命中</th>
            <th class="text-left px-4 py-3 font-medium">客户端</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(item, idx) in logs.items" :key="idx" class="border-b border-white/[.04] hover:bg-white/[.02]">
            <td class="px-4 py-2 font-mono text-xs">{{ item.method }}</td>
            <td class="px-4 py-2 font-mono text-xs text-slate-300" :title="item.path">
              {{ shortPath(item.path) }}
            </td>
            <td class="px-4 py-2">
              <el-tag :type="statusType(item.status)" size="small" effect="dark">{{ item.status }}</el-tag>
            </td>
            <td class="px-4 py-2 text-right text-xs text-slate-400 font-mono">
              {{ formatDuration(item.durationMs) }}
            </td>
            <td class="px-4 py-2 text-right text-xs text-slate-400 font-mono">
              {{ formatBytes(item.bytes) }}
            </td>
            <td class="px-4 py-2">
              <el-tag v-if="item.cached" type="success" size="small" effect="dark">HIT</el-tag>
              <el-tag
                v-else-if="item.bypassed"
                :type="bypassType(item.bypassReason)"
                size="small"
                effect="dark"
              >
                {{ bypassLabel(item.bypassReason) }}
              </el-tag>
              <el-tag v-else size="small" effect="plain">MISS</el-tag>
            </td>
            <td class="px-4 py-2 text-xs text-slate-500 font-mono">{{ item.clientIp }}</td>
          </tr>
          <tr v-if="!logs.loading && logs.items.length === 0">
            <td colspan="7" class="text-center text-slate-500 py-8 text-sm">暂无访问记录</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="totalPages > 1" class="flex items-center justify-center">
      <el-pagination
        v-model:current-page="logs.page"
        :total="logs.total"
        :page-size="logs.pageSize"
        layout="prev, pager, next, total"
        @current-change="(p: number) => logs.setPage(p)"
      />
    </div>
  </section>
</template>
