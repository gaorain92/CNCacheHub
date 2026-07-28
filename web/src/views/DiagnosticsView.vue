<script setup lang="ts">
import { computed, onMounted } from 'vue'
import {
  Check,
  CircleClose,
  Refresh,
  Warning,
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useDiagnosticsStore } from '@/stores/diagnostics'
import { useAuthStore } from '@/stores/auth'
import type { DiagStatus } from '@/types/api'

const diag = useDiagnosticsStore()
const auth = useAuthStore()

onMounted(async () => {
  if (!diag.report) await diag.run()
})

async function onRun(): Promise<void> {
  if (!auth.isAdmin) {
    ElMessage.warning('请用管理员账号登录')
    return
  }
  const ok = await diag.run()
  if (ok) {
    const summary = overallSummary.value
    if (summary === 'ok') ElMessage.success('全部检查通过')
    else if (summary === 'warning') ElMessage.warning('有警告，请查看详情')
    else ElMessage.error('有错误，请查看详情并按提示修复')
  } else {
    ElMessage.error(diag.errorMessage || '运行失败')
  }
}

const overallSummary = computed<DiagStatus | 'pending'>(() => {
  if (!diag.report) return 'pending'
  for (const pb of diag.report.playbooks) {
    if (pb.summary === 'error') return 'error'
  }
  for (const pb of diag.report.playbooks) {
    if (pb.summary === 'warning') return 'warning'
  }
  return 'ok'
})

function statusIcon(s: DiagStatus) {
  if (s === 'ok') return Check
  if (s === 'warning') return Warning
  return CircleClose
}

function statusColor(s: DiagStatus): 'success' | 'warning' | 'danger' {
  if (s === 'ok') return 'success'
  if (s === 'warning') return 'warning'
  return 'danger'
}

function playbookLabel(p: string): string {
  if (p === 'docker_pull') return 'Docker 拉取'
  if (p === 'steamcmd_dns') return 'SteamCMD DNS'
  if (p === 'reverse_proxy') return '反代 + TLS'
  return p
}

function playbookIcon(p: string): string {
  if (p === 'docker_pull') return '📦'
  if (p === 'steamcmd_dns') return '🎮'
  if (p === 'reverse_proxy') return '🔒'
  return '🔍'
}

function relTime(unixMs: number): string {
  if (!unixMs) return '—'
  const diff = (Date.now() - unixMs) / 1000
  if (diff < 60) return `${Math.floor(diff)}s ago`
  return new Date(unixMs).toLocaleString('zh-CN', { hour12: false })
}

const passCount = computed(() => {
  if (!diag.report) return 0
  let n = 0
  for (const pb of diag.report.playbooks) {
    for (const c of pb.checks) if (c.status === 'ok') n++
  }
  return n
})

const warnCount = computed(() => {
  if (!diag.report) return 0
  let n = 0
  for (const pb of diag.report.playbooks) {
    for (const c of pb.checks) if (c.status === 'warning') n++
  }
  return n
})

const errCount = computed(() => {
  if (!diag.report) return 0
  let n = 0
  for (const pb of diag.report.playbooks) {
    for (const c of pb.checks) if (c.status === 'error') n++
  }
  return n
})
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between gap-3">
      <p class="text-sm text-slate-400">下载失败 / DNS 未生效 / 证书错误的一键排查。</p>
      <el-button
        type="primary"
        :icon="Refresh"
        size="small"
        :loading="diag.running"
        :disabled="!auth.isAdmin"
        @click="onRun"
      >
        重新运行
      </el-button>
    </header>

    <div v-if="diag.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ diag.errorMessage }}
    </div>

    <!-- 总览 -->
    <div v-if="diag.report" class="rounded-2xl border border-white/[.08] bg-black/20 p-5">
      <div class="flex items-center justify-between flex-wrap gap-3">
        <div class="flex items-center gap-3">
          <el-tag :type="statusColor(overallSummary as DiagStatus)" size="large" effect="dark" round>
            {{ overallSummary }}
          </el-tag>
          <div class="text-sm text-slate-400">
            上次跑：{{ relTime(diag.lastRunAt) }} · 共 {{ diag.report.playbooks.length }} 个剧本
          </div>
        </div>
        <div class="flex items-center gap-2 text-xs">
          <span class="rounded-full bg-mint/20 text-mint px-3 py-1">通过 {{ passCount }}</span>
          <span class="rounded-full bg-amber-500/20 text-amber-300 px-3 py-1">警告 {{ warnCount }}</span>
          <span class="rounded-full bg-rose-500/20 text-rose-300 px-3 py-1">错误 {{ errCount }}</span>
        </div>
      </div>
    </div>

    <!-- 剧本列表 -->
    <div v-if="!diag.report && diag.running" class="rounded-2xl border border-white/[.08] bg-black/20 p-12 text-center text-slate-400">
      正在跑诊断剧本…
    </div>

    <div v-for="pb in diag.report?.playbooks || []" :key="pb.playbook" class="rounded-2xl border border-white/[.08] bg-black/20 overflow-hidden">
      <div class="px-5 py-4 flex items-center justify-between border-b border-white/[.06]">
        <div class="flex items-center gap-3">
          <span class="text-2xl">{{ playbookIcon(pb.playbook) }}</span>
          <div>
            <div class="flex items-center gap-2">
              <span class="text-base font-semibold text-slate-100">{{ pb.title }}</span>
              <span class="text-xs text-slate-500 font-mono">{{ playbookLabel(pb.playbook) }}</span>
            </div>
            <div class="text-xs text-slate-500 mt-0.5">
              {{ pb.checks.length }} 项检查 ·
              <span :class="statusColor(pb.summary) === 'success' ? 'text-mint' : statusColor(pb.summary) === 'warning' ? 'text-amber-300' : 'text-rose-300'">
                {{ pb.summary }}
              </span>
            </div>
          </div>
        </div>
        <el-tag :type="statusColor(pb.summary)" effect="dark" size="small">
          {{ pb.summary }}
        </el-tag>
      </div>

      <div class="divide-y divide-white/[.04]">
        <div
          v-for="(c, i) in pb.checks"
          :key="i"
          class="px-5 py-3 flex items-start gap-3"
        >
          <el-icon :size="18" :color="statusColor(c.status) === 'success' ? '#2dd4bf' : statusColor(c.status) === 'warning' ? '#fbbf24' : '#fb7185'" class="mt-0.5 shrink-0">
            <component :is="statusIcon(c.status)" />
          </el-icon>
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-sm font-medium text-slate-100">{{ c.name }}</span>
              <el-tag :type="statusColor(c.status)" size="small" effect="dark">{{ c.status }}</el-tag>
            </div>
            <div class="text-sm text-slate-300 mt-1 break-all">{{ c.message }}</div>
            <div v-if="c.detail" class="text-xs text-slate-500 mt-1 font-mono break-all">{{ c.detail }}</div>
            <div v-if="c.fix && c.status !== 'ok'" class="mt-2 rounded-lg bg-amber-500/5 border border-amber-500/20 p-3 text-xs text-amber-200 space-y-1">
              <div class="font-semibold flex items-center gap-1">
                <el-icon :size="14"><Warning /></el-icon>
                修复建议
              </div>
              <div class="text-amber-100/90 break-words whitespace-pre-wrap">{{ c.fix }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="diag.report && diag.report.playbooks.length === 0" class="rounded-2xl border border-white/[.08] bg-black/20 p-12 text-center">
      <div class="text-slate-400">暂无诊断剧本</div>
    </div>

    <div v-if="!auth.isAdmin" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm text-amber-200">
      只读模式（已自动跑了一次）。登录管理员后可手动「重新运行」。
    </div>
  </section>
</template>
