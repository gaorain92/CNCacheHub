<script setup lang="ts">
import { ref, watch } from 'vue'
import { Check, Connection, CopyDocument, Document, Download, Position, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElTabPane, ElTabs } from 'element-plus'
import DaemonJsonBlock from '@/components/DaemonJsonBlock.vue'
import { useClientConfigStore } from '@/stores/client-config'
import { downloadClientConfigBundle } from '@/api/client-config'

const config = useClientConfigStore()

const activeTab = ref<'docker' | 'containerd' | 'k3s'>('docker')

// 容器化 / k3s 内部：选 registry（按 enabled upstream）
const registryOptions = [
  { value: 'dockerhub', label: 'docker.io (Docker Hub)' },
  { value: 'ghcr', label: 'ghcr.io (GitHub Container Registry)' },
  { value: 'quay', label: 'quay.io (Quay)' },
  { value: 'k8s', label: 'registry.k8s.io (Kubernetes)' },
]
const selectedRegistry = ref('dockerhub')
const selectedFormat = ref<'containerd-hosts' | 'k3s-registries'>('containerd-hosts')

const copied = ref(false)
const downloading = ref(false)

async function regen(): Promise<void> {
  if (activeTab.value === 'docker') return
  const format = activeTab.value === 'k3s' ? 'k3s-registries' : 'containerd-hosts'
  selectedFormat.value = format
  const ok = await config.generate(format, selectedRegistry.value)
  if (!ok) {
    ElMessage.error(config.errorMessage || '生成失败')
  }
}

async function copyContent(): Promise<void> {
  if (!config.data) return
  try {
    await navigator.clipboard.writeText(config.data.content)
    copied.value = true
    ElMessage.success('已复制到剪贴板')
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    ElMessage.error('复制失败')
  }
}

// 下载完整配置包 zip（§9.5.4）
async function downloadBundle(): Promise<void> {
  downloading.value = true
  try {
    const blob = await downloadClientConfigBundle()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `cncachehub-client-config-${new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)}.zip`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    ElMessage.success('配置包已下载，解压后 bash verify.sh 验证')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error?.message || e?.message || '下载失败')
  } finally {
    downloading.value = false
  }
}

// 切 tab 或 registry 时重生成
watch([activeTab, selectedRegistry], () => {
  if (activeTab.value !== 'docker') {
    regen()
  }
})

// 切到 containerd/k3s tab 立即生成
const onTabChange = (name: string | number): void => {
  activeTab.value = name as 'docker' | 'containerd' | 'k3s'
  if (name !== 'docker') {
    regen()
  }
}
</script>

<template>
  <section class="soft rounded-[2rem] p-8 space-y-6">
    <header class="flex items-center justify-between gap-3 flex-wrap">
      <p class="text-sm text-slate-400">生成 Docker / containerd / k3s 客户端配置，一键复制粘贴接入。</p>
      <el-button
        :icon="Download"
        type="primary"
        :loading="downloading"
        @click="downloadBundle"
      >
        下载完整配置包（zip）
      </el-button>
    </header>

    <el-tabs v-model="activeTab" @tab-change="onTabChange">
      <el-tab-pane name="docker">
        <template #label>
          <span class="flex items-center gap-1.5">
            <el-icon :size="14"><Document /></el-icon>
            Docker Engine
          </span>
        </template>
        <DaemonJsonBlock />
      </el-tab-pane>

      <el-tab-pane name="containerd">
        <template #label>
          <span class="flex items-center gap-1.5">
            <el-icon :size="14"><Connection /></el-icon>
            containerd / nerdctl
          </span>
        </template>

        <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6 space-y-4">
          <div class="flex items-center justify-between flex-wrap gap-3">
            <div class="flex items-center gap-2">
              <el-icon :size="18" color="#94a3b8"><Connection /></el-icon>
              <h3 class="text-base font-semibold">containerd hosts.toml</h3>
              <span class="text-xs text-slate-500">每 registry 一个文件</span>
            </div>
            <el-select
              v-model="selectedRegistry"
              size="small"
              style="width: 240px"
              :disabled="config.loading"
            >
              <el-option
                v-for="r in registryOptions"
                :key="r.value"
                :label="r.label"
                :value="r.value"
              />
            </el-select>
            <el-button :icon="Refresh" size="small" plain :loading="config.loading" @click="regen">刷新</el-button>
          </div>

          <div v-if="config.data" class="space-y-3">
            <pre class="text-xs font-mono text-slate-200 bg-black/40 p-4 rounded-xl overflow-x-auto whitespace-pre"><code>{{ config.data.content }}</code></pre>

            <div class="rounded-xl bg-white/[.04] p-4 text-xs space-y-2">
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Position /></el-icon>
                <div>
                  <span class="text-slate-400">目标路径：</span>
                  <code class="text-mint">{{ config.data.targetPath }}</code>
                </div>
              </div>
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Position /></el-icon>
                <div>
                  <span class="text-slate-400">重启：</span>
                  <code class="text-mint">{{ config.data.restartCmd }}</code>
                </div>
              </div>
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Check /></el-icon>
                <div>
                  <span class="text-slate-400">验证：</span>
                  <code class="text-mint">{{ config.data.verifyCmd }}</code>
                </div>
              </div>
            </div>

            <div class="flex justify-end">
              <el-button
                type="primary"
                size="small"
                :icon="copied ? Check : CopyDocument"
                :disabled="!config.data"
                @click="copyContent"
              >
                {{ copied ? '已复制' : '复制 hosts.toml' }}
              </el-button>
            </div>
          </div>

          <div v-else-if="config.loading" class="text-sm text-slate-500 py-6 text-center">生成中…</div>
          <div v-else class="text-sm text-slate-500 py-6 text-center">未生成</div>

          <div v-if="config.errorMessage" class="rounded-lg border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-300">
            {{ config.errorMessage }}
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane name="k3s">
        <template #label>
          <span class="flex items-center gap-1.5">
            <el-icon :size="14"><Position /></el-icon>
            k3s / rke2
          </span>
        </template>

        <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6 space-y-4">
          <div class="flex items-center justify-between flex-wrap gap-3">
            <div class="flex items-center gap-2">
              <el-icon :size="18" color="#94a3b8"><Position /></el-icon>
              <h3 class="text-base font-semibold">k3s registries.yaml</h3>
              <span class="text-xs text-slate-500">K3s / RKE2 镜像加速</span>
            </div>
            <el-button :icon="Refresh" size="small" plain :loading="config.loading" @click="regen">刷新</el-button>
          </div>

          <div v-if="config.data" class="space-y-3">
            <pre class="text-xs font-mono text-slate-200 bg-black/40 p-4 rounded-xl overflow-x-auto whitespace-pre"><code>{{ config.data.content }}</code></pre>

            <div class="rounded-xl bg-white/[.04] p-4 text-xs space-y-2">
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Position /></el-icon>
                <div>
                  <span class="text-slate-400">目标路径：</span>
                  <code class="text-mint">{{ config.data.targetPath }}</code>
                </div>
              </div>
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Position /></el-icon>
                <div>
                  <span class="text-slate-400">重启：</span>
                  <code class="text-mint">{{ config.data.restartCmd }}</code>
                </div>
              </div>
              <div class="flex items-start gap-2">
                <el-icon :size="14" color="#a3e635" class="mt-0.5"><Check /></el-icon>
                <div>
                  <span class="text-slate-400">验证：</span>
                  <code class="text-mint">{{ config.data.verifyCmd }}</code>
                </div>
              </div>
            </div>

            <div class="flex justify-end">
              <el-button
                type="primary"
                size="small"
                :icon="copied ? Check : CopyDocument"
                :disabled="!config.data"
                @click="copyContent"
              >
                {{ copied ? '已复制' : '复制 registries.yaml' }}
              </el-button>
            </div>
          </div>

          <div v-else-if="config.loading" class="text-sm text-slate-500 py-6 text-center">生成中…</div>
          <div v-else class="text-sm text-slate-500 py-6 text-center">未生成</div>

          <div v-if="config.errorMessage" class="rounded-lg border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-300">
            {{ config.errorMessage }}
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>
  </section>
</template>
