<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  ArrowDown,
  Delete,
  FolderOpened,
  Plus,
  Refresh,
  VideoPlay,
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePreheatStore } from '@/stores/preheat'
import { useAuthStore } from '@/stores/auth'

const preheat = usePreheatStore()
const auth = useAuthStore()

// 展开的任务 id
const expanded = ref<Set<number>>(new Set())
let pollTimer: ReturnType<typeof setInterval> | null = null

// 创建表单
const dialogOpen = ref(false)
const dialogSaving = ref(false)
const form = ref({
  name: '',
  kind: 'docker' as 'docker' | 'steam' | 'resource',
  targetsText: '',
  cronExpression: '',
  enabled: true,
})

onMounted(async () => {
  await preheat.fetch()
  // 每 5s 轮询刷新（任务跑时进度实时更新）
  pollTimer = setInterval(() => {
    preheat.fetch()
  }, 5000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

function toggleExpand(id: number): void {
  if (expanded.value.has(id)) {
    expanded.value.delete(id)
  } else {
    expanded.value.add(id)
    void preheat.fetchItems(id)
  }
}

async function openCreate(): Promise<void> {
  form.value = { name: '', kind: 'docker', targetsText: '', cronExpression: '', enabled: true }
  dialogOpen.value = true
}

const targetPlaceholder = computed(() => {
  switch (form.value.kind) {
    case 'docker':
      return 'nginx:alpine\nredis:7\nghcr.io/owner/repo:tag\nquay.io/prometheus/prometheus:v2.52.0'
    case 'steam':
      return '730\n2394010\n896660'
    case 'resource':
      return 'https://github.com/.../release.tar.gz\nhttps://huggingface.co/.../model.safetensors'
  }
  return ''
})

const kindHint = computed(() => {
  switch (form.value.kind) {
    case 'docker':
      return '每行一条镜像；自动识别 Registry（docker.io / ghcr.io / quay.io / registry.k8s.io）。预热会拉 manifest + 各 layer，写入 CNCacheHub 缓存。'
    case 'steam':
      return '每行一个 Steam AppID。预热会在 CNCacheHub 宿主机跑 docker run cm2network/steamcmd +app_update，需要 anonymous 公开仓库。'
    case 'resource':
      return '每行一个资源 URL。P2 阶段实现；当前 P1 暂不支持。'
  }
  return ''
})

async function onSaveTask(): Promise<void> {
  if (!form.value.name.trim()) {
    ElMessage.error('请输入任务名称')
    return
  }
  const targets = form.value.targetsText
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  if (targets.length === 0) {
    ElMessage.error('至少输入 1 个 target')
    return
  }
  dialogSaving.value = true
  const t = await preheat.create({
    name: form.value.name.trim(),
    kind: form.value.kind,
    targets,
    cronExpression: form.value.cronExpression.trim(),
    enabled: form.value.enabled,
  })
  dialogSaving.value = false
  if (t) {
    ElMessage.success(`任务已创建（id=${t.id}）`)
    dialogOpen.value = false
  } else {
    ElMessage.error(preheat.errorMessage || '创建失败')
  }
}

async function onDelete(t: { id: number; name: string }): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `确定删除预热任务「${t.name}」？${'\\n'}任务下所有 item 记录也会被清理。`,
      '删除确认',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  if (await preheat.remove(t.id)) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(preheat.errorMessage || '删除失败')
  }
}

async function onRun(t: { id: number; name: string }): Promise<void> {
  const updated = await preheat.run(t.id)
  if (updated) {
    ElMessage.success(`已触发「${t.name}」，异步执行中…`)
    setTimeout(() => preheat.fetch(), 1000)
  } else {
    ElMessage.error(preheat.errorMessage || '启动失败')
  }
}

async function onCancel(t: { id: number; name: string }): Promise<void> {
  if (await preheat.cancel(t.id)) {
    ElMessage.success(`已请求取消「${t.name}」`)
  } else {
    ElMessage.error(preheat.errorMessage || '取消失败（可能不在运行）')
  }
}

function statusType(s: string): 'success' | 'warning' | 'danger' | 'info' {
  if (s === 'done') return 'success'
  if (s === 'running') return 'info'
  if (s === 'pending') return 'warning'
  if (s === 'error') return 'danger'
  if (s === 'canceled') return 'info'
  return 'info'
}

function kindLabel(k: string): string {
  if (k === 'docker') return 'Docker'
  if (k === 'steam') return 'Steam'
  if (k === 'resource') return 'Resource'
  if (k === 'huggingface_model') return 'HF 模型'
  return k
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

function formatDuration(ms: number): string {
  if (ms <= 0) return '—'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

// 当任务状态变化时，自动拉新 items（如果展开了）
watch(
  () => preheat.tasks.map((t) => `${t.id}:${t.status}`).join('|'),
  () => {
    for (const id of expanded.value) {
      void preheat.fetchItems(id)
    }
  }
)
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between gap-3">
      <p class="text-sm text-slate-400">批量输入镜像 / Steam AppID，提前写入缓存。</p>
      <div class="flex items-center gap-2">
        <el-button :icon="Refresh" size="small" plain :loading="preheat.loading" @click="preheat.fetch()">刷新</el-button>
        <el-button type="primary" :icon="Plus" size="small" :disabled="!auth.isAdmin" @click="openCreate">
          新建任务
        </el-button>
      </div>
    </header>

    <div v-if="preheat.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ preheat.errorMessage }}
    </div>

    <div v-if="!auth.isAdmin" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm text-amber-200">
      只读模式，登录管理员后可创建 / 触发 / 取消预热任务。
    </div>

    <!-- 任务列表 -->
    <div v-if="preheat.tasks.length === 0 && !preheat.loading" class="rounded-2xl border border-white/[.08] bg-black/20 p-12 text-center">
      <el-icon :size="48" color="#475569" class="mb-3"><FolderOpened /></el-icon>
      <div class="text-slate-400 mb-2">暂无预热任务</div>
      <div class="text-xs text-slate-500">点击右上角「新建任务」开始：批量粘贴镜像名 / AppID，提前把常用内容拉进 CNCacheHub 缓存。</div>
    </div>

    <div v-for="t in preheat.tasks" :key="t.id" class="rounded-2xl border border-white/[.08] bg-black/20 overflow-hidden">
      <div class="px-5 py-4 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3 flex-1 min-w-0">
          <el-button
            :icon="ArrowDown"
            size="small"
            plain
            :class="['transition', expanded.has(t.id) ? 'rotate-180' : '']"
            @click="toggleExpand(t.id)"
          />
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-base font-semibold text-slate-100">{{ t.name }}</span>
              <el-tag size="small" effect="plain">{{ kindLabel(t.kind) }}</el-tag>
              <el-tag :type="statusType(t.status)" size="small" effect="dark">{{ t.status }}</el-tag>
              <el-tag v-if="!t.enabled" size="small" effect="plain">disabled</el-tag>
            </div>
            <div class="text-xs text-slate-500 mt-1 truncate">
              {{ t.targets.length }} 个 target
              <span v-if="t.lastRunAt > 0"> · 上次跑 {{ relTime(t.lastRunAt) }} · 耗时 {{ formatDuration(t.lastDurationMs) }}</span>
              <span v-if="t.errorMessage"> · <span class="text-rose-400">{{ t.errorMessage }}</span></span>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <el-button
            v-if="t.status === 'running'"
            size="small"
            type="warning"
            plain
            :disabled="!auth.isAdmin"
            @click="onCancel(t)"
          >
            取消
          </el-button>
          <el-button
            v-else
            type="primary"
            :icon="VideoPlay"
            size="small"
            plain
            :disabled="!auth.isAdmin"
            @click="onRun(t)"
          >
            立即跑
          </el-button>
          <el-button
            :icon="Delete"
            size="small"
            type="danger"
            plain
            :disabled="!auth.isAdmin"
            @click="onDelete(t)"
          />
        </div>
      </div>

      <!-- 进度条 -->
      <div v-if="t.status === 'running' || t.progressDone > 0" class="px-5 pb-3">
        <div class="flex items-center justify-between text-xs text-slate-400 mb-1">
          <span>进度 {{ t.progressDone }} / {{ t.progressTotal }}</span>
          <span>{{ formatBytes(t.progressBytes) }}</span>
        </div>
        <div class="h-1.5 rounded-full bg-white/[.06] overflow-hidden">
          <div
            class="h-full bg-gradient-to-r from-mint to-violet transition-all"
            :style="{ width: t.progressTotal > 0 ? `${(t.progressDone / t.progressTotal * 100).toFixed(0)}%` : '0%' }"
          />
        </div>
      </div>

      <!-- 展开 item 列表 -->
      <div v-if="expanded.has(t.id)" class="border-t border-white/[.06]">
        <table class="w-full text-sm">
          <thead class="text-xs text-slate-500 border-b border-white/[.04]">
            <tr>
              <th class="text-left px-5 py-2 font-medium">Target</th>
              <th class="text-left px-3 py-2 font-medium">状态</th>
              <th class="text-right px-3 py-2 font-medium">大小</th>
              <th class="text-left px-3 py-2 font-medium">错误</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="it in preheat.items[t.id] || []" :key="it.id" class="border-b border-white/[.04]">
              <td class="px-5 py-2 font-mono text-xs text-slate-200 truncate max-w-[300px]" :title="it.target">
                {{ it.target }}
              </td>
              <td class="px-3 py-2">
                <el-tag :type="statusType(it.status)" size="small" effect="dark">{{ it.status }}</el-tag>
              </td>
              <td class="px-3 py-2 text-right text-xs text-slate-400 font-mono">
                {{ it.bytesAdded > 0 ? formatBytes(it.bytesAdded) : '—' }}
              </td>
              <td class="px-3 py-2 text-xs text-rose-300 font-mono truncate max-w-[400px]" :title="it.errorMessage">
                {{ it.errorMessage || '—' }}
              </td>
            </tr>
            <tr v-if="!preheat.items[t.id] || preheat.items[t.id].length === 0">
              <td colspan="4" class="text-center text-slate-500 py-3 text-sm">加载中…</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 新建任务 -->
    <el-dialog v-model="dialogOpen" title="新建预热任务" width="560px" :close-on-click-modal="false">
      <el-form :model="form" label-position="top">
        <el-form-item label="任务名称" required>
          <el-input v-model="form.name" placeholder="常用基础镜像" />
        </el-form-item>
        <el-form-item label="任务类型">
          <el-radio-group v-model="form.kind">
            <el-radio-button value="docker">Docker 镜像</el-radio-button>
            <el-radio-button value="steam">Steam AppID</el-radio-button>
            <el-radio-button value="resource">资源 URL（P2）</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="`Targets（${form.kind === 'steam' ? '每行一个 AppID' : '每行一条'}）`" required>
          <el-input
            v-model="form.targetsText"
            type="textarea"
            :rows="6"
            :placeholder="targetPlaceholder"
            class="font-mono"
          />
          <p class="text-xs text-slate-500 mt-1">{{ kindHint }}</p>
        </el-form-item>
        <el-form-item label="定时（cron 表达式，留空 = 一次性）">
          <el-input v-model="form.cronExpression" placeholder="0 2 * * *（每天凌晨 2 点跑）" class="font-mono" />
          <p class="text-xs text-slate-500 mt-1">
            MVP 暂未实现 cron 解析；这里只是占位字段，后续用 robfig/cron 跑调度。
          </p>
        </el-form-item>
        <el-form-item>
          <el-switch v-model="form.enabled" inline-prompt active-text="启用" inactive-text="禁用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="dialogSaving" @click="onSaveTask">创建</el-button>
      </template>
    </el-dialog>
  </section>
</template>
