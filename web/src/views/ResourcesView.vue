<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Delete,
  Edit,
  Position,
  Refresh,
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useResourcesStore } from '@/stores/resources'
import { useAuthStore } from '@/stores/auth'
import { listResourceTemplates } from '@/api/resources'
import type { ResourceTemplate } from '@/types/api'

const resources = useResourcesStore()
const auth = useAuthStore()

// 展开的 rule id
const expanded = ref<Set<number>>(new Set())

// 模板库（P2#1）
const templatesOpen = ref(false)
const templates = ref<ResourceTemplate[]>([])
const templatesLoading = ref(false)

// 新建 / 编辑 弹窗
const dialogOpen = ref(false)
const dialogSaving = ref(false)
const editingId = ref<number | null>(null)
const form = ref({
  name: '',
  kind: 'github' as 'github' | 'playwright' | 'huggingface' | 'terraform' | 'custom',
  upstreamUrl: '',
  defaultTtlSeconds: 86400,
  description: '',
  enabled: true,
})

onMounted(async () => {
  if (resources.rules.length === 0) await resources.fetch()
})

function toggleExpand(id: number): void {
  if (expanded.value.has(id)) {
    expanded.value.delete(id)
  } else {
    expanded.value.add(id)
    void resources.fetchCache(id, 50)
  }
}

function kindLabel(k: string): string {
  const map: Record<string, string> = {
    github: 'GitHub',
    playwright: 'Playwright',
    huggingface: 'Hugging Face',
    terraform: 'Terraform',
    custom: '自定义',
  }
  return map[k] || k
}

function kindIcon(k: string): string {
  const map: Record<string, string> = {
    github: '📦',
    playwright: '🎭',
    huggingface: '🤗',
    terraform: '🏗️',
    custom: '🔧',
  }
  return map[k] || '📁'
}

function upstreamHint(): string {
  switch (form.value.kind) {
    case 'github':
      return '推荐 https://raw.githubusercontent.com （raw 文件）或 https://github.com（release assets）'
    case 'playwright':
      return '推荐 https://playwright.azureedge.net（默认）'
    case 'huggingface':
      return '推荐 https://huggingface.co（模型/datasets）'
    case 'terraform':
      return '推荐 https://releases.hashicorp.com'
    default:
      return '任意 https/http 上游 URL'
  }
}

function ttlLabel(sec: number): string {
  if (sec >= 86400) return `${sec / 86400} 天`
  if (sec >= 3600) return `${sec / 3600} 小时`
  return `${sec} 秒`
}

async function openTemplates(): Promise<void> {
  templatesOpen.value = true
  if (templates.value.length === 0) {
    templatesLoading.value = true
    try {
      const resp = await listResourceTemplates()
      templates.value = resp.items
    } finally {
      templatesLoading.value = false
    }
  }
}

async function applyTemplate(t: ResourceTemplate): Promise<void> {
  // 检查同名 rule
  if (resources.rules.some((r) => r.name === t.name)) {
    ElMessage.warning(`已存在同名 rule「${t.name}」，请改名后再试`)
    return
  }
  const r = await resources.create({
    name: t.name,
    kind: t.kind,
    upstreamUrl: t.upstreamUrl,
    defaultTtlSeconds: 604800,
    description: t.description,
    pathPattern: t.pathPattern,
  } as any)
  if (r) {
    ElMessage.success(`已应用模板「${t.name}」`)
    templatesOpen.value = false
  } else {
    ElMessage.error(resources.errorMessage || '创建失败')
  }
}

async function openCreate(): Promise<void> {
  editingId.value = null
  form.value = { name: '', kind: 'github', upstreamUrl: '', defaultTtlSeconds: 86400, description: '', enabled: true }
  dialogOpen.value = true
}

function openEdit(r: { id: number; name: string; kind: 'github' | 'playwright' | 'huggingface' | 'terraform' | 'custom'; upstreamUrl: string; defaultTtlSeconds: number; description: string; enabled: boolean }): void {
  editingId.value = r.id
  form.value = { ...r }
  dialogOpen.value = true
}

async function onSave(): Promise<void> {
  if (!form.value.name.trim()) {
    ElMessage.error('请输入名称')
    return
  }
  if (!form.value.upstreamUrl.trim()) {
    ElMessage.error('请输入 upstreamUrl')
    return
  }
  if (!/^https?:\/\//.test(form.value.upstreamUrl)) {
    ElMessage.error('upstreamUrl 必须以 http:// 或 https:// 开头')
    return
  }
  dialogSaving.value = true
  if (editingId.value) {
    const r = await resources.patch(editingId.value, {
      upstreamUrl: form.value.upstreamUrl.trim(),
      defaultTtlSeconds: form.value.defaultTtlSeconds,
      description: form.value.description,
      enabled: form.value.enabled,
    })
    dialogSaving.value = false
    if (r) {
      ElMessage.success('已保存')
      dialogOpen.value = false
    } else {
      ElMessage.error(resources.errorMessage || '保存失败')
    }
  } else {
    const r = await resources.create({
      name: form.value.name.trim(),
      kind: form.value.kind,
      upstreamUrl: form.value.upstreamUrl.trim(),
      defaultTtlSeconds: form.value.defaultTtlSeconds,
      description: form.value.description,
      enabled: form.value.enabled,
    })
    dialogSaving.value = false
    if (r) {
      ElMessage.success(`已添加 rule「${r.name}」`)
      dialogOpen.value = false
    } else {
      ElMessage.error(resources.errorMessage || '创建失败')
    }
  }
}

async function onDelete(r: { id: number; name: string }): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `确定删除 rule「${r.name}」？该 rule 下所有缓存条目也会被清理。`,
      '删除确认',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  if (await resources.remove(r.id)) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(resources.errorMessage || '删除失败')
  }
}

async function onDeleteCache(c: { id: number; path: string; ruleId: number }): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除缓存「${c.path}」？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  if (await resources.removeCache(c.id, c.ruleId)) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(resources.errorMessage || '删除失败')
  }
}

const publicBaseUrl = computed(() => {
  if (typeof window === 'undefined') return ''
  return `${window.location.protocol}//${window.location.host}`
})

function previewUrl(name: string, samplePath: string): string {
  return `${publicBaseUrl.value}/r/${name}/${samplePath}`
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between gap-3">
      <p class="text-sm text-slate-400">白名单 URL 缓存 · GitHub / Hugging Face / Playwright / Terraform / 自定义</p>
      <div class="flex items-center gap-2">
        <el-button :icon="Refresh" size="small" plain :loading="resources.loading" @click="resources.fetch()">刷新</el-button>
        <el-button size="small" plain @click="openTemplates">模板库</el-button>
        <el-button type="primary" size="small" :disabled="!auth.isAdmin" @click="openCreate">新增 rule</el-button>
      </div>
    </header>

    <div v-if="resources.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ resources.errorMessage }}
    </div>

    <!-- 接入说明 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-3">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Position /></el-icon>
        <h3 class="text-base font-semibold">接入方式</h3>
      </div>
      <p class="text-sm text-slate-300 leading-relaxed">
        客户端把原本请求官方域名的 URL 改成 CNCacheHub 的 <code class="text-mint">/r/&lt;rule_name&gt;/&lt;原路径&gt;</code> 前缀。
        首次走上游拉取并落盘，后续直接读缓存（响应头 <code class="text-mint">X-Cncachehub-Cache: HIT</code>）。
      </p>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs">
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1">GitHub raw 文件</div>
          <code class="text-slate-200 break-all">curl {{ previewUrl('github-release', 'golang/go/master/CONTRIBUTING.md') }}</code>
        </div>
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1">Hugging Face 模型</div>
          <code class="text-slate-200 break-all">curl {{ previewUrl('huggingface', 'Qwen/Qwen2.5-7B-Instruct/resolve/main/config.json') }}</code>
        </div>
      </div>
    </div>

    <!-- rule 列表 -->
    <div v-for="r in resources.rules" :key="r.id" class="rounded-2xl border border-white/[.08] bg-black/20 overflow-hidden">
      <div class="px-5 py-4 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3 flex-1 min-w-0">
          <span class="text-2xl">{{ kindIcon(r.kind) }}</span>
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-base font-semibold text-slate-100">{{ r.name }}</span>
              <el-tag size="small" effect="plain">{{ kindLabel(r.kind) }}</el-tag>
              <el-tag v-if="!r.enabled" size="small" type="info" effect="plain">disabled</el-tag>
            </div>
            <div class="text-xs text-slate-500 font-mono mt-1 truncate">
              {{ r.upstreamUrl }} · pattern: <span class="text-mint">{{ r.pathPattern || '*' }}</span> · TTL {{ ttlLabel(r.defaultTtlSeconds) }}
            </div>
            <div v-if="r.description" class="text-xs text-slate-500 mt-0.5">{{ r.description }}</div>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <el-button size="small" plain @click="toggleExpand(r.id)">
            {{ expanded.has(r.id) ? '收起缓存' : '查看缓存' }}
          </el-button>
          <el-button :icon="Edit" size="small" plain :disabled="!auth.isAdmin" @click="openEdit(r)" />
          <el-button :icon="Delete" size="small" type="danger" plain :disabled="!auth.isAdmin" @click="onDelete(r)" />
        </div>
      </div>

      <!-- 展开：cache 列表 -->
      <div v-if="expanded.has(r.id)" class="border-t border-white/[.06] overflow-x-auto">
        <table class="w-full text-sm min-w-[800px]">
          <thead class="text-xs text-slate-500 border-b border-white/[.04]">
            <tr>
              <th class="text-left px-5 py-2 font-medium">Path</th>
              <th class="text-right px-3 py-2 font-medium">大小</th>
              <th class="text-right px-3 py-2 font-medium">命中</th>
              <th class="text-left px-3 py-2 font-medium">类型</th>
              <th class="text-left px-3 py-2 font-medium">到期</th>
              <th class="text-right px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in resources.caches[r.id] || []" :key="c.id" class="border-b border-white/[.04]">
              <td class="px-5 py-2 font-mono text-xs text-slate-200 truncate max-w-[400px]" :title="c.path">
                {{ c.path }}
              </td>
              <td class="px-3 py-2 text-right text-xs text-slate-400 font-mono">
                {{ formatBytes(c.sizeBytes) }}
              </td>
              <td class="px-3 py-2 text-right text-xs text-slate-300 font-mono">{{ c.hitCount }}</td>
              <td class="px-3 py-2 text-xs text-slate-500 font-mono truncate max-w-[180px]">
                {{ c.contentType || '—' }}
              </td>
              <td class="px-3 py-2 text-xs text-slate-500 font-mono">
                {{ c.expiresAt ? new Date(c.expiresAt * 1000).toLocaleString('zh-CN', { hour12: false }) : '永不过期' }}
              </td>
              <td class="px-3 py-2 text-right">
                <el-button :icon="Delete" size="small" type="danger" plain :disabled="!auth.isAdmin" @click="onDeleteCache(c)" />
              </td>
            </tr>
            <tr v-if="!resources.caches[r.id] || resources.caches[r.id].length === 0">
              <td colspan="6" class="text-center text-slate-500 py-3 text-sm">
                暂无缓存 · 客户端访问 <code class="text-mint">{{ previewUrl(r.name, '...') }}</code> 后会出现在这里
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="!auth.isAdmin" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm text-amber-200">
      只读模式，登录管理员后可新增 / 改 / 删 rule 和缓存。
    </div>

    <!-- 模板库 -->
    <el-dialog v-model="templatesOpen" title="资源加速模板库" width="720px">
      <div v-if="templatesLoading" class="text-center text-slate-500 py-6">加载中…</div>
      <div v-else class="space-y-3">
        <p class="text-sm text-slate-400">推荐配置 · 一键应用</p>
        <div
          v-for="t in templates"
          :key="t.name"
          class="rounded-2xl border border-white/[.08] bg-white/[.04] p-4 hover:border-mint/30 transition"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="text-base font-semibold text-slate-100">{{ t.name }}</span>
                <el-tag size="small" effect="plain">{{ t.kind }}</el-tag>
                <el-tag size="small" effect="plain">pattern: {{ t.pathPattern }}</el-tag>
              </div>
              <div class="text-xs text-slate-400 mt-1">{{ t.description }}</div>
              <div class="text-xs text-slate-500 font-mono mt-1 truncate">{{ t.upstreamUrl }}</div>
              <pre class="text-[10px] font-mono text-slate-400 bg-black/40 p-2 rounded mt-2 overflow-x-auto whitespace-pre"><code>{{ t.sample }}</code></pre>
            </div>
            <el-button
              type="primary"
              size="small"
              :disabled="!auth.isAdmin || resources.rules.some((r) => r.name === t.name)"
              @click="applyTemplate(t)"
            >
              {{ resources.rules.some((r) => r.name === t.name) ? '已存在' : '应用' }}
            </el-button>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 新建 / 编辑 rule -->
    <el-dialog v-model="dialogOpen" :title="editingId ? '编辑 Rule' : '新增 Rule'" width="560px" :close-on-click-modal="false">
      <el-form :model="form" label-position="top">
        <el-form-item label="Rule 名称（URL 中使用）" required>
          <el-input v-model="form.name" :placeholder="'github-release'" :disabled="!!editingId" />
          <p class="text-xs text-slate-500 mt-1">客户端访问 /r/<code class="text-mint">{{ form.name || '...' }}</code>/&lt;path&gt;</p>
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.kind" :disabled="!!editingId">
            <el-radio-button value="github">GitHub</el-radio-button>
            <el-radio-button value="huggingface">Hugging Face</el-radio-button>
            <el-radio-button value="playwright">Playwright</el-radio-button>
            <el-radio-button value="terraform">Terraform</el-radio-button>
            <el-radio-button value="custom">自定义</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="Upstream URL" required>
          <el-input v-model="form.upstreamUrl" :placeholder="form.kind === 'github' ? 'https://raw.githubusercontent.com' : 'https://...'" />
          <p class="text-xs text-slate-500 mt-1">{{ upstreamHint() }}</p>
        </el-form-item>
        <el-form-item label="默认 TTL（秒）">
          <el-input-number v-model="form.defaultTtlSeconds" :min="0" :step="3600" controls-position="right" class="w-full" />
          <p class="text-xs text-slate-500 mt-1">0 = 永不过期</p>
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="可选描述" />
        </el-form-item>
        <el-form-item>
          <el-switch v-model="form.enabled" inline-prompt active-text="启用" inactive-text="禁用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="dialogSaving" @click="onSave">
          {{ editingId ? '保存' : '创建' }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>
