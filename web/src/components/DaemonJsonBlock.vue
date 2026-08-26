<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Document, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useDockerStore } from '@/stores/docker'
import { copyToClipboard } from '@/utils/clipboard'

const docker = useDockerStore()
const copied = ref(false)

async function load(): Promise<void> {
  await docker.fetchDaemonJson()
}

async function onCopy(): Promise<void> {
  if (!docker.daemonJson) return
  const ok = await copyToClipboard(docker.daemonJson)
  if (ok) {
    copied.value = true
    ElMessage.success('已复制到剪贴板')
    setTimeout(() => (copied.value = false), 1500)
  } else {
    ElMessage.warning('请用弹窗手动复制（Ctrl+C）')
  }
}

onMounted(() => {
  if (!docker.daemonJson) load()
})
</script>

<template>
  <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6">
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Document /></el-icon>
        <h3 class="text-base font-semibold">daemon.json 片段</h3>
        <span v-if="docker.daemonJsonFor" class="text-xs text-slate-500">/ for: {{ docker.daemonJsonFor }}</span>
      </div>
      <div class="flex items-center gap-2">
        <el-button :icon="Refresh" size="small" plain @click="load">刷新</el-button>
        <el-button
          type="primary"
          size="small"
          :disabled="!docker.daemonJson"
          @click="onCopy"
        >
          {{ copied ? '已复制' : '复制' }}
        </el-button>
      </div>
    </div>

    <pre
      v-if="docker.daemonJson"
      class="text-xs font-mono text-slate-200 bg-black/40 p-4 rounded-xl overflow-x-auto whitespace-pre"
    ><code>{{ docker.daemonJson }}</code></pre>
    <div v-else class="text-sm text-slate-500 py-6 text-center">加载中…</div>

    <div class="mt-4 text-xs text-slate-500 space-y-1">
      <p>· Linux 写入：<code class="text-slate-400">/etc/docker/daemon.json</code>，然后 <code class="text-slate-400">sudo systemctl restart docker</code></p>
      <p>· macOS 写入：<code class="text-slate-400">~/.docker/daemon.json</code>，然后 Docker Desktop → Restart</p>
      <p>· 验证：<code class="text-slate-400">docker info | grep -A2 "Registry Mirrors"</code></p>
      <p>· 自签 / HTTP mirror 已在 <code class="text-slate-400">insecure-registries</code> 中加入</p>
    </div>
  </div>
</template>
