<script setup lang="ts">
import { onMounted } from 'vue'
import { Box, Connection, Link } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import DaemonJsonBlock from '@/components/DaemonJsonBlock.vue'
import { useDockerStore } from '@/stores/docker'
import { useRegistriesStore } from '@/stores/registries'
import { useAuthStore } from '@/stores/auth'

const docker = useDockerStore()
const registries = useRegistriesStore()
const auth = useAuthStore()

onMounted(async () => {
  if (registries.items.length === 0) await registries.fetch()
  if (docker.upstreams.length === 0) await docker.fetchUpstreams()
  if (!docker.daemonJson) await docker.fetchDaemonJson()
})

async function toggleEnabled(name: string, current: boolean): Promise<void> {
  if (!auth.isAdmin) {
    ElMessage.warning('只有管理员可以启停 Registry')
    return
  }
  const next = !current
  const ok = await registries.setEnabled(name, next)
  if (ok) {
    ElMessage.success(`${name} 已${next ? '启用' : '禁用'}`)
  } else {
    ElMessage.error(registries.errorMessage || '操作失败')
  }
}

function mirrorPathLabel(m: string): string {
  return m === '' ? '/v2 (默认)' : m
}
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

    <!-- 多 Registry 列表 -->
    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-6">
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#94a3b8"><Connection /></el-icon>
          <h3 class="text-base font-semibold">多 Registry 代理</h3>
          <span class="text-xs text-slate-500">每条独立上游、独立缓存</span>
        </div>
        <el-button size="small" plain @click="registries.fetch()">刷新</el-button>
      </div>

      <div v-if="registries.items.length > 0" class="space-y-3">
        <div
          v-for="r in registries.items"
          :key="r.id"
          class="flex items-center justify-between rounded-2xl bg-white/[.04] px-4 py-3"
        >
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2">
              <span class="text-sm font-semibold text-slate-100">{{ r.name }}</span>
              <el-tag size="small" :type="r.enabled ? 'success' : 'info'" effect="dark">
                {{ r.enabled ? 'enabled' : 'disabled' }}
              </el-tag>
            </div>
            <div class="text-xs text-slate-500 font-mono mt-1 truncate">
              {{ r.upstreamUrl }}
            </div>
            <div class="text-xs text-slate-500 mt-0.5">
              客户端访问：<code class="text-mint">{{ mirrorPathLabel(r.mirrorPath) }}/&lt;repo&gt;/manifests/&lt;ref&gt;</code>
            </div>
          </div>
          <el-switch
            :model-value="r.enabled"
            :disabled="!auth.isAdmin"
            inline-prompt
            active-text="ON"
            inactive-text="OFF"
            @change="() => toggleEnabled(r.name, r.enabled)"
          />
        </div>
      </div>
      <div v-else-if="registries.loading" class="text-sm text-slate-500 py-4 text-center">加载中…</div>
      <div v-else class="text-sm text-slate-500 py-4 text-center">
        暂无可用 registry
      </div>

      <div v-if="!auth.isAdmin" class="mt-3 text-xs text-amber-300/80">
        ⚠ 当前为只读模式，登录管理员后可启停 Registry
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
        <li>
          <strong class="text-mint">Docker Engine</strong>：复制上面 <code class="text-slate-400">daemon.json</code> 片段，写入 docker daemon 配置文件；只支持 <code>dockerhub</code> 默认 upstream。
        </li>
        <li>
          <strong class="text-mint">containerd / k3s</strong>：在 <code>/etc/containerd/certs.d/&lt;host&gt;/hosts.toml</code> 配置
          <code>server = "http://&lt;cnch&gt;/v2/ghcr"</code> 等（ghcr/quay/k8s 各自一个 hosts.toml）。
        </li>
        <li>
          <strong class="text-mint">Kubernetes</strong>：在 containerd 配置同 location 写 server URL 即可，按 registry 切。
        </li>
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
