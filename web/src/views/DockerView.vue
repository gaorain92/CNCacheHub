<script setup lang="ts">
import { onMounted } from 'vue'
import { Box, Connection, Link } from '@element-plus/icons-vue'
import DaemonJsonBlock from '@/components/DaemonJsonBlock.vue'
import { useDockerStore } from '@/stores/docker'

const docker = useDockerStore()

onMounted(async () => {
  if (docker.upstreams.length === 0) await docker.fetchUpstreams()
  if (!docker.daemonJson) await docker.fetchDaemonJson()
})
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><Box /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">Docker 加速</h2>
        <p class="text-sm text-slate-400">Registry 代理缓存 · 客户端配置生成 · 上游状态</p>
      </div>
    </header>

    <!-- 上游状态 -->
    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6">
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#94a3b8"><Connection /></el-icon>
          <h3 class="text-base font-semibold">上游 Registry</h3>
        </div>
        <el-button size="small" plain @click="docker.fetchUpstreams()">刷新</el-button>
      </div>

      <div v-if="docker.upstreams.length > 0" class="space-y-3">
        <div
          v-for="up in docker.upstreams"
          :key="up.id"
          class="flex items-center justify-between rounded-2xl bg-white/[.04] px-4 py-3"
        >
          <div>
            <div class="text-sm font-medium text-slate-200">{{ up.name }}</div>
            <div class="text-xs text-slate-500 font-mono mt-0.5">{{ up.upstreamUrl }}</div>
          </div>
          <div class="flex items-center gap-2">
            <el-tag size="small" :type="up.enabled ? 'success' : 'info'" effect="dark">
              {{ up.enabled ? 'enabled' : 'disabled' }}
            </el-tag>
            <code class="text-xs text-slate-500 bg-black/30 px-2 py-1 rounded">{{ up.mirrorPath }}</code>
          </div>
        </div>
      </div>
      <div v-else-if="docker.loading" class="text-sm text-slate-500 py-4 text-center">加载中…</div>
      <div v-else class="text-sm text-slate-500 py-4 text-center">
        暂无可用 upstream · {{ docker.errorMessage }}
      </div>
    </div>

    <!-- daemon.json 配置生成 -->
    <DaemonJsonBlock />

    <!-- 接入提示 -->
    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6">
      <div class="flex items-center gap-2 mb-3">
        <el-icon :size="18" color="#94a3b8"><Link /></el-icon>
        <h3 class="text-base font-semibold">客户端接入流程</h3>
      </div>
      <ol class="text-sm text-slate-300 space-y-2 list-decimal pl-5">
        <li>复制上面 <code class="text-slate-400">daemon.json</code> 片段，写入到对应系统的 docker daemon 配置文件。</li>
        <li>重启 docker daemon（Linux: <code class="text-slate-400">systemctl restart docker</code>）。</li>
        <li>
          验证 mirror 生效：<code class="text-slate-400">docker info | grep -A2 "Registry Mirrors"</code> 应包含本服务地址。
        </li>
        <li>
          拉一个常见镜像：<code class="text-slate-400">docker pull nginx:alpine</code>，首次走 upstream 拉取并落盘，二次从本地缓存秒回。
        </li>
      </ol>
    </div>
  </section>
</template>
