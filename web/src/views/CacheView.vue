<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Brush, DataLine, Delete, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useCacheStore } from '@/stores/cache'
import { getCleanupTasks, runCleanupTask, dryRunCleanupTask } from '@/api/cleanup'
import type { CleanupTask } from '@/types/api'

const cache = useCacheStore()
const tasks = ref<CleanupTask[]>([])
const runningId = ref<number | null>(null)

const totalPages = computed(() => Math.max(1, Math.ceil(cache.total / cache.pageSize)))

onMounted(async () => {
  if (cache.items.length === 0) cache.fetch()
  try {
    tasks.value = await getCleanupTasks()
  } catch {
    // ignore
  }
})

function shortDigest(d: string): string {
  if (d.length <= 20) return d
  return d.slice(0, 7) + '…' + d.slice(-12)
}

function formatTime(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false })
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function describeTask(t: CleanupTask): string {
  if (t.strategy === 'lru') {
    return `${(t.thresholdSeconds / 86400).toFixed(1)} 天未访问 → 删除`
  }
  return `总量 > ${formatBytes(t.thresholdBytes)} → 删除最旧`
}

async function handleDelete(entry: { id: number; repository: string; digest: string }): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `确定删除缓存条目？\n\n${entry.repository}\n${entry.digest}\n\n此操作不可撤销。`,
      '确认删除',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  const ok = await cache.remove(entry.id)
  if (ok) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(`删除失败：${cache.errorMessage}`)
  }
}

function onSearch(q: string): void {
  cache.setQuery(q.trim())
}

async function runCleanup(t: CleanupTask): Promise<void> {
  // 1) 先 dry-run 预估（PRD §9.6.5 防误删）
  const report = await dryRunCleanupTask(t.id)
  const human = `预计释放 ${report.freedCount} 条 / ${formatBytes(report.freedBytes)} / 耗时 ${report.durationMs}ms`
  try {
    await ElMessageBox.confirm(
      `${describeTask(t)}\n\n${human}\n\n确认执行清理？此操作不可撤销。`,
      `${t.name} · 清理预估`,
      { type: 'warning', confirmButtonText: '确认清理', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  // 2) 真跑
  runningId.value = t.id
  try {
    const rep = await runCleanupTask(t.id)
    ElMessage.success(
      `清理完成：删除 ${rep.freedCount} 条 / 释放 ${formatBytes(rep.freedBytes)} / 耗时 ${rep.durationMs}ms`
    )
    await cache.fetch()
    const fresh = await getCleanupTasks()
    tasks.value = fresh
  } catch (e) {
    ElMessage.error(`清理失败：${(e as Error).message}`)
  } finally {
    runningId.value = null
  }
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
          <el-icon :size="22" color="#020617"><DataLine /></el-icon>
        </div>
        <div>
          <h2 class="text-2xl font-semibold">缓存管理</h2>
          <p class="text-sm text-slate-400">共 {{ cache.total }} 条 · 本页 {{ cache.items.length }} 条</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <el-input
          :model-value="cache.query"
          placeholder="搜索 repo / digest / registry"
          clearable
          style="width: 260px"
          @keyup.enter="(e: KeyboardEvent) => onSearch((e.target as HTMLInputElement).value)"
          @clear="onSearch('')"
        >
          <template #prefix>
            <el-icon><Search /></el-icon>
          </template>
        </el-input>
        <el-button @click="cache.fetch()">刷新</el-button>
      </div>
    </header>

    <div v-if="cache.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ cache.errorMessage }}
    </div>

    <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">本页占用</div>
        <div class="text-2xl font-semibold text-slate-100">{{ formatBytes(cache.totalBytes) }}</div>
        <div class="text-xs text-slate-500 mt-2">{{ cache.items.length }} 个条目</div>
      </div>
      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">本页总命中</div>
        <div class="text-2xl font-semibold text-slate-100">{{ cache.totalHits }}</div>
        <div class="text-xs text-slate-500 mt-2">hit_count 累计</div>
      </div>
      <div class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
        <div class="text-xs text-slate-500 mb-2">总条数</div>
        <div class="text-2xl font-semibold text-slate-100">{{ cache.total }}</div>
        <div class="text-xs text-slate-500 mt-2">按 last_access 倒序</div>
      </div>
    </div>

    <!-- 清理任务 -->
    <div v-if="tasks.length > 0" class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
      <div class="flex items-center gap-2 mb-3">
        <el-icon :size="18" color="#94a3b8"><Brush /></el-icon>
        <h3 class="text-base font-semibold">清理任务</h3>
        <span class="text-xs text-slate-500">定期释放缓存空间</span>
      </div>
      <div class="space-y-2">
        <div
          v-for="t in tasks"
          :key="t.id"
          class="flex items-center justify-between rounded-xl bg-white/[.04] px-4 py-3"
        >
          <div class="flex-1">
            <div class="flex items-center gap-2">
              <el-tag size="small" :type="t.strategy === 'lru' ? 'info' : 'warning'" effect="dark">
                {{ t.strategy.toUpperCase() }}
              </el-tag>
              <span class="text-sm text-slate-200">{{ t.name }}</span>
              <el-tag v-if="!t.enabled" size="small" effect="plain">disabled</el-tag>
            </div>
            <div class="text-xs text-slate-500 mt-1">
              {{ describeTask(t) }}
              <span v-if="t.lastRunAt > 0">· 上次 {{ formatTime(t.lastRunAt) }}</span>
              <span v-if="t.lastStatus" :class="t.lastStatus.startsWith('error') ? 'text-rose-400' : 'text-slate-500'">
                · {{ t.lastStatus }}
              </span>
              <span v-if="t.lastFreedCount > 0">· 释放 {{ t.lastFreedCount }} 条 / {{ formatBytes(t.lastFreedBytes) }}</span>
            </div>
          </div>
          <el-button
            :icon="Brush"
            size="small"
            plain
            :loading="runningId === t.id"
            @click="runCleanup(t)"
          >
            立即跑
          </el-button>
        </div>
      </div>
    </div>

    <div class="rounded-2xl border border-white/[.08] bg-black/20 overflow-hidden">
      <table class="w-full text-sm">
        <thead class="text-xs text-slate-500 border-b border-white/[.06]">
          <tr>
            <th class="text-left px-4 py-3 font-medium">ID</th>
            <th class="text-left px-4 py-3 font-medium">Registry</th>
            <th class="text-left px-4 py-3 font-medium">Repository</th>
            <th class="text-left px-4 py-3 font-medium">Digest</th>
            <th class="text-right px-4 py-3 font-medium">大小</th>
            <th class="text-right px-4 py-3 font-medium">命中</th>
            <th class="text-left px-4 py-3 font-medium">最近访问</th>
            <th class="text-left px-4 py-3 font-medium">状态</th>
            <th class="text-right px-4 py-3 font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="e in cache.items"
            :key="e.id"
            class="border-b border-white/[.04] hover:bg-white/[.02]"
          >
            <td class="px-4 py-2 text-xs text-slate-500 font-mono">#{{ e.id }}</td>
            <td class="px-4 py-2">
              <el-tag size="small" effect="plain">{{ e.registry }}</el-tag>
            </td>
            <td class="px-4 py-2 text-sm text-slate-200">{{ e.repository }}</td>
            <td class="px-4 py-2 font-mono text-xs text-slate-400" :title="e.digest">
              {{ shortDigest(e.digest) }}
            </td>
            <td class="px-4 py-2 text-right text-xs text-slate-400 font-mono">
              {{ formatBytes(e.sizeBytes) }}
            </td>
            <td class="px-4 py-2 text-right text-xs text-slate-300 font-mono">
              {{ e.hitCount }}
            </td>
            <td class="px-4 py-2 text-xs text-slate-500 font-mono">{{ formatTime(e.lastAccessAt) }}</td>
            <td class="px-4 py-2">
              <el-tag v-if="e.bypassed" type="warning" size="small">
                BYPASS · {{ e.bypassReason }}
              </el-tag>
              <el-tag v-else type="success" size="small">CACHED</el-tag>
            </td>
            <td class="px-4 py-2 text-right">
              <el-button
                :icon="Delete"
                size="small"
                type="danger"
                plain
                @click="handleDelete(e)"
              >
                删除
              </el-button>
            </td>
          </tr>
          <tr v-if="!cache.loading && cache.items.length === 0">
            <td colspan="9" class="text-center text-slate-500 py-8 text-sm">暂无缓存条目</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="totalPages > 1" class="flex items-center justify-center">
      <el-pagination
        v-model:current-page="cache.page"
        :total="cache.total"
        :page-size="cache.pageSize"
        layout="prev, pager, next, total"
        @current-change="(p: number) => cache.setPage(p)"
      />
    </div>
  </section>
</template>
