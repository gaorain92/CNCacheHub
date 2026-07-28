<script setup lang="ts">
import { computed, onMounted, ref, reactive } from 'vue'
import { Refresh, Search, Delete } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useLogsStore } from '@/stores/logs'
import type { LogFilter } from '@/types/api'

const logs = useLogsStore()

onMounted(() => {
  if (logs.items.length === 0) logs.fetch()
})

const totalPages = computed(() => Math.max(1, Math.ceil(logs.total / logs.pageSize)))

// ---- 筛选表单 ----
const showFilters = ref(false)
const filterForm = reactive<LogFilter>({
  statusCls: 0,
  method: '',
  path: '',
  cached: undefined,
  bypassed: undefined,
  clientIp: '',
})

const statusClsOptions = [
  { label: '全部', value: 0 },
  { label: '2xx 成功', value: 2 },
  { label: '3xx 重定向', value: 3 },
  { label: '4xx 客户端错误', value: 4 },
  { label: '5xx 服务端错误', value: 5 },
]

const methodOptions = ['', 'GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE']

const hitOptions = [
  { label: '全部', value: undefined },
  { label: 'HIT (命中)', value: true },
  { label: 'MISS (未命中)', value: false },
]

const bypassOptions = [
  { label: '全部', value: undefined },
  { label: '仅旁路', value: true },
  { label: '非旁路', value: false },
]

function applyFilters() {
  const f: LogFilter = {}
  if (filterForm.statusCls && filterForm.statusCls > 0) f.statusCls = filterForm.statusCls
  if (filterForm.method) f.method = filterForm.method
  if (filterForm.path) f.path = filterForm.path
  if (filterForm.cached !== undefined) f.cached = filterForm.cached
  if (filterForm.bypassed !== undefined) f.bypassed = filterForm.bypassed
  if (filterForm.clientIp) f.clientIp = filterForm.clientIp
  logs.setFilter(f)
}

function resetFilters() {
  filterForm.statusCls = 0
  filterForm.method = ''
  filterForm.path = ''
  filterForm.cached = undefined
  filterForm.bypassed = undefined
  filterForm.clientIp = ''
  logs.clearFilter()
}

const hasActiveFilters = computed(() => {
  const f = logs.filter
  return !!(f.statusCls || f.method || f.path || f.cached !== undefined || f.bypassed !== undefined || f.clientIp)
})

// ---- 清理日志 ----
async function handlePurge() {
  try {
    const { value } = await ElMessageBox.prompt(
      '输入保留天数，超出的日志将被永久删除',
      '清理历史日志',
      {
        inputValue: '30',
        inputPattern: /^\d+$/,
        inputErrorMessage: '请输入正整数',
        confirmButtonText: '清理',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
    const days = parseInt(value, 10)
    if (days <= 0) {
      ElMessage.warning('保留天数必须大于 0')
      return
    }
    const result = await logs.purge(days)
    ElMessage.success(`已清理 ${result.deleted} 条超过 ${days} 天的日志`)
  } catch {
    // 用户取消
  }
}

// ---- 表格辅助 ----
function statusType(status: number): 'success' | 'warning' | 'danger' | 'info' {
  if (status >= 500) return 'danger'
  if (status >= 400) return 'warning'
  if (status >= 300) return 'info'
  return 'success'
}

function bypassLabel(reason: string): string {
  if (reason === 'size_limit') return 'BYPASS_SIZE_LIMIT'
  if (reason === 'disk_low') return 'BYPASS_DISK_LOW'
  return 'BYPASS'
}

function bypassType(reason: string): 'warning' | 'danger' {
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

function formatTime(ts: number): string {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
</script>

<template>
  <section class="space-y-5">
    <header class="flex items-center justify-between">
      <p class="text-sm text-slate-400">
        共 {{ logs.total }} 条
        <template v-if="hasActiveFilters"> · <span class="text-amber-400">筛选中</span></template>
      </p>
      <div class="flex items-center gap-2">
        <el-button
          :icon="Search"
          size="small"
          :type="showFilters ? 'primary' : 'default'"
          plain
          @click="showFilters = !showFilters"
        >
          筛选
        </el-button>
        <el-button :icon="Delete" size="small" plain type="warning" @click="handlePurge">
          清理日志
        </el-button>
        <el-button :icon="Refresh" size="small" plain :loading="logs.loading" @click="logs.fetch()">
          刷新
        </el-button>
      </div>
    </header>

    <!-- 筛选面板 -->
    <transition name="el-zoom-in-top">
      <div v-show="showFilters" class="rounded-2xl border border-white/[.08] bg-black/20 p-5 space-y-4">
        <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          <!-- 状态码 -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">状态码</label>
            <el-select v-model="filterForm.statusCls" class="w-full" size="small">
              <el-option
                v-for="opt in statusClsOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </div>
          <!-- 方法 -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">方法</label>
            <el-select v-model="filterForm.method" class="w-full" size="small" clearable placeholder="全部">
              <el-option v-for="m in methodOptions" :key="m" :label="m || '全部'" :value="m" />
            </el-select>
          </div>
          <!-- 路径搜索 -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">路径关键词</label>
            <el-input v-model="filterForm.path" size="small" placeholder="如: /v2/library/nginx" clearable />
          </div>
          <!-- 客户端 IP -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">客户端 IP</label>
            <el-input v-model="filterForm.clientIp" size="small" placeholder="如: 10.0.0.1" clearable />
          </div>
          <!-- 命中状态 -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">缓存命中</label>
            <el-select v-model="filterForm.cached" class="w-full" size="small">
              <el-option
                v-for="opt in hitOptions"
                :key="String(opt.value)"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </div>
          <!-- 旁路状态 -->
          <div>
            <label class="block text-xs text-slate-400 mb-1">旁路状态</label>
            <el-select v-model="filterForm.bypassed" class="w-full" size="small">
              <el-option
                v-for="opt in bypassOptions"
                :key="String(opt.value)"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </div>
        </div>
        <div class="flex items-center gap-2 pt-1">
          <el-button type="primary" size="small" @click="applyFilters">应用筛选</el-button>
          <el-button size="small" @click="resetFilters">重置</el-button>
          <span v-if="hasActiveFilters" class="text-xs text-amber-400/80 ml-2">
            当前有活跃筛选条件
          </span>
        </div>
      </div>
    </transition>

    <!-- 错误 -->
    <div v-if="logs.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      加载失败：{{ logs.errorMessage }}
    </div>

    <!-- 日志表格 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 overflow-x-auto">
      <table class="w-full text-sm min-w-[900px]">
        <thead class="text-xs text-slate-500 border-b border-white/[.06]">
          <tr>
            <th class="text-left px-4 py-3 font-medium">时间</th>
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
          <tr v-for="item in logs.items" :key="item.id" class="border-b border-white/[.04] hover:bg-white/[.02]">
            <td class="px-4 py-2 text-xs text-slate-400 font-mono whitespace-nowrap">
              {{ formatTime(item.createdAt) }}
            </td>
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
              <el-tag v-else type="info" size="small" effect="dark">MISS</el-tag>
            </td>
            <td class="px-4 py-2 text-xs text-slate-500 font-mono">{{ item.clientIp }}</td>
          </tr>
          <tr v-if="!logs.loading && logs.items.length === 0">
            <td colspan="8" class="text-center text-slate-500 py-8 text-sm">
              {{ hasActiveFilters ? '没有匹配的日志，试试调整筛选条件' : '暂无访问记录' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 分页 -->
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
