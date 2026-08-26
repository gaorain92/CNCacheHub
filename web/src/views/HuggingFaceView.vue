<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  Delete,
  FolderOpened,
  Position,
  Refresh,
  VideoPlay,
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useSettingsStore } from '@/stores/settings'
import { useAuthStore } from '@/stores/auth'
import { usePreheatStore } from '@/stores/preheat'
import {
  createHuggingFacePreheat,
  listHuggingFaceTree,
} from '@/api/huggingface'
import type { HuggingFaceTreeFile } from '@/types/api'
import { copyToClipboard } from '@/utils/clipboard'

const settings = useSettingsStore()
const auth = useAuthStore()
const preheat = usePreheatStore()

// 任务轮询
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  if (!settings.data) await settings.fetch()
  await preheat.fetch()
  // 5s 轮询，让 HF preheat 任务进度实时更新
  pollTimer = setInterval(() => {
    void preheat.fetch()
  }, 5000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

// ---- token 状态 ----
const hfTokenSet = computed(() => !!settings.data?.huggingfaceTokenSet)

// ---- URL 生成器 ----
const hfModel = ref('Qwen/Qwen2.5-1.5B-Instruct')
const hfRevision = ref('main')
const hfFilename = ref('config.json')

const publicBaseUrl = computed(() => {
  if (typeof window === 'undefined') return ''
  return `${window.location.protocol}//${window.location.host}`
})

// HF 镜像端点 base URL（用户在 HF_ENDPOINT 用的）
const mirrorBaseUrl = computed(() => publicBaseUrl.value)

async function copyMirrorUrl(): Promise<void> {
  const url = `${mirrorBaseUrl.value}/hf`
  const ok = await copyToClipboard(url)
  if (ok) {
    ElMessage.success('已复制：' + url)
  } else {
    // copyToClipboard 内部已经 fallback 到 prompt，提示用户用弹窗
    ElMessage.info('已弹出复制对话框，请 Ctrl+C')
  }
}

const hfDownloadUrl = computed(() => {
  if (!hfModel.value.trim()) return ''
  const path = `${hfModel.value.trim()}/resolve/${hfRevision.value || 'main'}/${hfFilename.value || 'config.json'}`
  return `${publicBaseUrl.value}/r/huggingface-models/${path}`
})

const hfCurlCommand = computed(() => {
  if (!hfModel.value.trim()) return ''
  return [
    `curl -L -o "${hfFilename.value || 'model'}" "${hfDownloadUrl.value}"`,
    `# 断点续传（Range 透传，hf-models 自动 follow 302 → cdn-lfs）:`,
    `curl -L -C - -o "${hfFilename.value || 'model'}" "${hfDownloadUrl.value}"`,
  ].join('\n')
})

// ---- 文件树 + 全量预热 ----
const treeModel = ref('Qwen/Qwen2.5-1.5B-Instruct')
const treeRevision = ref('main')
const treeLoading = ref(false)
const treeError = ref('')
const treeErrorCode = ref('')
const treeFiles = ref<HuggingFaceTreeFile[]>([])
// 简单 throttle 防突发点击（HF 401/403 = rate limit）
// 只对【同一 query + 已有结果】生效，换模型 / 改 revision / 失败重试 都不挡
let lastTreeFetchKey = ''
let lastTreeFetchAt = 0
const TREE_COOLDOWN_MS = 1500

const treeTotalBytes = computed(() => {
  let n = 0
  for (const f of treeFiles.value) {
    n += f.size || 0
  }
  return n
})

const treeTotalHuman = computed(() => formatBytes(treeTotalBytes.value))

async function loadTree(): Promise<void> {
  const id = treeModel.value.trim()
  if (!id) {
    ElMessage.warning('请输入模型 ID')
    return
  }
  const revision = treeRevision.value || 'main'
  const key = `${id}@${revision}`
  // throttle：只对"同 query 且已有结果"挡 — 换模型 / 改 rev / 失败重试 都不挡
  const now = Date.now()
  if (key === lastTreeFetchKey && now - lastTreeFetchAt < TREE_COOLDOWN_MS && treeFiles.value.length > 0) {
    return
  }
  lastTreeFetchKey = key
  lastTreeFetchAt = now
  treeLoading.value = true
  treeError.value = ''
  treeErrorCode.value = ''
  try {
    const resp = await listHuggingFaceTree(id, revision)
    treeFiles.value = resp.files
    if (resp.files.length === 0) {
      ElMessage.info('该模型文件树为空')
    }
  } catch (e) {
    const err = e as { response?: { data?: { error?: { message?: string; code?: string } } }; message?: string }
    treeErrorCode.value = err.response?.data?.error?.code || ''
    treeError.value = err.response?.data?.error?.message || err.message || '拉取失败'
    treeFiles.value = []
  } finally {
    treeLoading.value = false
  }
}

// patterns & full preheat
const patternInput = ref('*.json\ntokenizer*')
const maxFiles = ref(500)
const preheating = ref(false)
const preheatTaskName = ref('')
const preheatForce = ref(false)

const patterns = computed(() =>
  patternInput.value
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
)

// 当前 cache cap（GB），从 settings 拿
const cacheCapGb = computed(() => settings.data?.cacheTotalGb ?? 0)
const cacheCapBytes = computed(() => cacheCapGb.value * 1024 * 1024 * 1024)

// 预估 tree 总量是否超出 cap
const willExceedCap = computed(() => {
  return cacheCapBytes.value > 0 && treeTotalBytes.value > cacheCapBytes.value
})
const oversizeRatio = computed(() => {
  if (cacheCapBytes.value <= 0) return 0
  return treeTotalBytes.value / cacheCapBytes.value
})

// === Presets（针对小 VPS 场景） ===
function applyPreset(kind: 'configs-only' | 'configs-tokenizer' | 'small-files' | 'all'): void {
  switch (kind) {
    case 'configs-only':
      // config.json 等元数据，最小，但用不了模型
      patternInput.value = '*.json'
      break
    case 'configs-tokenizer':
      // config + tokenizer ≈ 几十 MB，能加载模型 + 推理（但 weights 还是要现场下）
      patternInput.value = '*.json\ntokenizer*'
      break
    case 'small-files':
      // 100MB 以下的全部：常见于 README/license/config/tokenizer/小权重
      // 真正的 .safetensors 通常 ≥1GB，会被 patterns 排除
      // （patten 不支持大小过滤，这只是粗筛；想精确用 server 端 force 检查）
      patternInput.value = '*.json\ntokenizer*\n*.md\n*.txt'
      break
    case 'all':
      patternInput.value = ''
      break
  }
}

async function onFullPreheat(): Promise<void> {
  const id = treeModel.value.trim()
  if (!id) {
    ElMessage.warning('请先输入模型 ID')
    return
  }
  // 预估大小 vs cap，超出时弹确认（即使服务器兜底也提一句）
  let confirmMsg = `将创建全量预热任务（按当前 patterns 过滤，最多 ${maxFiles.value} 个文件）。任务会在后台异步跑，进度在下方任务列表查看。`
  if (willExceedCap.value) {
    confirmMsg = `⚠ 预估总大小 ${treeTotalHuman.value} 已超过缓存上限 ${cacheCapGb.value} GB（${(oversizeRatio.value * 100).toFixed(0)}%）。\n\n请先调整 patterns（如用 "Configs+Tokenizer" preset），或确认真的要强行下载（可勾"强制"）。`
  }
  try {
    await ElMessageBox.confirm(confirmMsg, '确认全量预热', {
      type: willExceedCap.value ? 'warning' : 'info',
      confirmButtonText: '开始',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  preheating.value = true
  try {
    const resp = await createHuggingFacePreheat({
      modelId: id,
      revision: treeRevision.value || 'main',
      patterns: patterns.value,
      maxFiles: maxFiles.value,
      name: preheatTaskName.value || undefined,
      force: preheatForce.value || undefined,
    })
    if (resp.refused) {
      // 兜底：server 也可能拒；给用户一个明确的"用 force" 入口
      try {
        await ElMessageBox.confirm(
          `${resp.refusedWhy}\n\n是否仍然强制创建（会超出缓存上限，可能触发自动清理）？`,
          '超出缓存上限',
          { type: 'warning', confirmButtonText: '强制创建', cancelButtonText: '取消' }
        )
      } catch {
        return
      }
      // 二次调用，force=true
      preheatForce.value = true
      const resp2 = await createHuggingFacePreheat({
        modelId: id,
        revision: treeRevision.value || 'main',
        patterns: patterns.value,
        maxFiles: maxFiles.value,
        name: preheatTaskName.value || undefined,
        force: true,
      })
      if (resp2.refused) {
        ElMessage.error(resp2.refusedWhy || '创建被拒')
        return
      }
      ElMessage.success(
        `已强制创建任务 #${resp2.task.id}（${resp2.filteredFiles} 个文件，约 ${formatBytes(resp2.bytesTotal)}）`
      )
    } else {
      ElMessage.success(
        `已创建任务 #${resp.task.id}（${resp.filteredFiles} 个文件，约 ${formatBytes(resp.bytesTotal)}）`
      )
    }
    preheatTaskName.value = ''
    preheatForce.value = false
    await preheat.fetch()
  } catch (e) {
    const err = e as { response?: { data?: { error?: { message?: string } } }; message?: string }
    ElMessage.error(err.response?.data?.error?.message || err.message || '创建失败')
  } finally {
    preheating.value = false
  }
}

// ---- 任务列表（只显示 huggingface_model） ----
const hfTasks = computed(() =>
  preheat.tasks
    .filter((t) => t.kind === 'huggingface_model')
    .sort((a, b) => b.id - a.id)
)

async function runTask(id: number): Promise<void> {
  const t = await preheat.run(id)
  if (t) {
    ElMessage.success(`任务 #${id} 已触发`)
  } else {
    ElMessage.error(preheat.errorMessage || '触发失败')
  }
}

async function deleteTask(id: number): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除预热任务 #${id}？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  if (await preheat.remove(id)) {
    ElMessage.success('已删除')
  } else {
    ElMessage.error(preheat.errorMessage || '删除失败')
  }
}

function statusType(s: string): 'success' | 'warning' | 'danger' | 'info' {
  switch (s) {
    case 'done':
      return 'success'
    case 'running':
      return 'warning'
    case 'error':
    case 'canceled':
      return 'danger'
    default:
      return 'info'
  }
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
      <p class="text-sm text-slate-400">
        HuggingFace 模型加速 · 通过 <code class="text-mint">/r/huggingface-models/&lt;path&gt;</code> 缓存 HF LFS 大文件 + Range 断点续传
      </p>
      <div class="flex items-center gap-2 text-xs">
        <span v-if="hfTokenSet" class="text-mint">● HF token 已配置（gated 模型可用）</span>
        <span v-else class="text-amber-400">● HF token 未配置（gated 模型会 401）</span>
        <router-link to="/settings" class="text-sky-400 hover:underline">设置</router-link>
      </div>
    </header>

    <!-- 单文件 URL 生成器 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Position /></el-icon>
        <h3 class="text-base font-semibold">单文件下载 URL 生成器</h3>
      </div>
      <p class="text-xs text-slate-400 leading-relaxed">
        输入模型 ID / revision / 文件名，自动生成可下载 URL。支持 LFS 大文件 + Range 断点续传 + 302 重定向跟随。
      </p>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
        <el-input v-model="hfModel" placeholder="Qwen/Qwen2.5-1.5B-Instruct" clearable>
          <template #prepend>模型 ID</template>
        </el-input>
        <el-input v-model="hfRevision" placeholder="main" clearable>
          <template #prepend>Revision</template>
        </el-input>
        <el-input v-model="hfFilename" placeholder="config.json" clearable>
          <template #prepend>文件名</template>
        </el-input>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs">
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1">下载 URL</div>
          <code class="text-slate-200 break-all">{{ hfDownloadUrl || '—' }}</code>
        </div>
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1">curl 命令</div>
          <pre class="text-slate-200 whitespace-pre-wrap break-all font-mono">{{ hfCurlCommand || '—' }}</pre>
        </div>
      </div>
      <p class="text-[11px] text-slate-500">
        提示：Python 可用 <code class="text-slate-300">huggingface_hub.snapshot_download</code> 改
        <code class="text-slate-300">endpoint</code> 指向本机批量下载整个 repo。
      </p>
    </div>

    <!-- 镜像端点（HF_ENDPOINT 兼容） -->
    <div class="rounded-2xl border border-mint/30 bg-mint/5 p-6 space-y-4">
      <div class="flex items-center gap-2">
        <span class="text-2xl">🪞</span>
        <h3 class="text-base font-semibold">HF_ENDPOINT 镜像</h3>
        <el-tag size="small" type="success" effect="plain">v0.2+</el-tag>
        <span class="text-xs text-slate-500">让 <code class="text-slate-300">huggingface_hub</code> 一键走我们的缓存</span>
      </div>
      <p class="text-xs text-slate-400 leading-relaxed">
        把 <code class="text-mint">HF_ENDPOINT</code> 设为下面地址后，<code class="text-slate-300">snapshot_download</code> /
        <code class="text-slate-300">hf_hub_download</code> 等所有下载会透明走 CNCacheHub：
        tree API 透传，文件走缓存 + Range + token 注入。
      </p>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-sm">
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1 text-xs">Python（huggingface_hub）</div>
          <pre v-pre class="text-slate-200 whitespace-pre-wrap break-all font-mono text-xs">import os
os.environ['HF_ENDPOINT'] = '{{ mirrorBaseUrl }}/hf'
from huggingface_hub import snapshot_download
snapshot_download('Qwen/Qwen2.5-1.5B-Instruct')</pre>
        </div>
        <div class="rounded-xl bg-white/[.04] p-3">
          <div class="text-slate-400 mb-1 text-xs">curl 单文件</div>
          <pre v-pre class="text-slate-200 whitespace-pre-wrap break-all font-mono text-xs">curl -L -o config.json \
  '{{ mirrorBaseUrl }}/hf/Qwen/Qwen2.5-1.5B-Instruct/resolve/main/config.json'</pre>
          <div class="text-slate-400 mt-2 text-xs">镜像地址</div>
          <code class="text-mint break-all">{{ mirrorBaseUrl }}/hf</code>
          <el-button
            size="small"
            plain
            class="ml-2"
            @click="copyMirrorUrl"
          >复制</el-button>
        </div>
      </div>
      <details class="text-xs text-slate-400">
        <summary class="cursor-pointer hover:text-slate-200">支持的 endpoint 路由</summary>
        <ul class="mt-2 ml-4 list-disc space-y-1 font-mono">
          <li><code>GET /hf/api/models/&lt;id&gt;/tree/&lt;rev&gt;</code> — 透传 HF tree API</li>
          <li><code>GET /hf/&lt;org&gt;/&lt;name&gt;/resolve/&lt;rev&gt;/&lt;file&gt;</code> — 走缓存 + Range</li>
          <li><code>HEAD</code> 也支持（huggingface_hub 用 HEAD 探 size）</li>
        </ul>
      </details>
    </div>

    <!-- 模型文件树 + 全量预热 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center justify-between flex-wrap gap-2">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#94a3b8"><FolderOpened /></el-icon>
          <h3 class="text-base font-semibold">按模型 ID 全量下载</h3>
          <el-tag size="small" type="warning" effect="plain">v0.2+</el-tag>
        </div>
        <el-button :icon="Refresh" size="small" plain :loading="treeLoading" @click="loadTree">
          刷新文件树
        </el-button>
      </div>
      <p class="text-xs text-slate-400 leading-relaxed">
        输入模型 ID，CNCacheHub 会去 HF 拉文件树（按 patterns 过滤），然后后台异步拉所有匹配文件 → 写入本地缓存。
        之后客户端再访问 <code class="text-mint">/r/huggingface-models/&lt;id&gt;/resolve/&lt;rev&gt;/&lt;file&gt;</code> 即可秒下。
      </p>

      <!-- 安全 preset（小 VPS 推荐） -->
      <div class="flex items-center gap-2 flex-wrap text-xs">
        <span class="text-slate-500">快速 preset：</span>
        <el-button size="small" plain @click="applyPreset('configs-only')">Configs only</el-button>
        <el-button size="small" plain @click="applyPreset('configs-tokenizer')">Configs+Tokenizer（推荐）</el-button>
        <el-button size="small" plain @click="applyPreset('small-files')">小文件 (&lt;100MB)</el-button>
        <el-button size="small" plain @click="applyPreset('all')">全部文件</el-button>
        <span v-if="cacheCapGb > 0" class="text-slate-500 ml-2">缓存上限：{{ cacheCapGb }} GB</span>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
        <el-input v-model="treeModel" placeholder="Qwen/Qwen2.5-1.5B-Instruct" clearable>
          <template #prepend>模型 ID</template>
        </el-input>
        <el-input v-model="treeRevision" placeholder="main" clearable>
          <template #prepend>Revision</template>
        </el-input>
        <el-input v-model.number="maxFiles" type="number" placeholder="500">
          <template #prepend>文件上限</template>
        </el-input>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-sm">
        <el-input
          v-model="patternInput"
          type="textarea"
          :rows="4"
          placeholder="*.safetensors&#10;*.json&#10;tokenizer*"
        >
          <template #prepend>Patterns（每行一个 glob）</template>
        </el-input>
        <el-input
          v-model="preheatTaskName"
          placeholder="留空自动生成：hf: &lt;model&gt; @ &lt;rev&gt;"
        >
          <template #prepend>任务名（可选）</template>
        </el-input>
      </div>
      <div class="flex items-center gap-2">
        <el-button
          type="primary"
          :icon="VideoPlay"
          :loading="preheating"
          :disabled="!auth.isAdmin"
          @click="onFullPreheat"
        >
          全量预热（后台）
        </el-button>
        <span class="text-xs text-slate-500">
          Patterns 留空 = 全部文件（受 maxFiles 限制）
        </span>
      </div>

      <!-- 文件树结果 -->
      <div v-if="treeError" class="rounded-xl border border-rose-500/30 bg-rose-500/10 p-3 text-sm space-y-2">
        <div class="text-rose-200 font-medium">
          <span v-if="treeErrorCode === 'hf_token_required'">可能 gated / 限流</span>
          <span v-else>拉取失败</span>
        </div>
        <div class="text-rose-300/80 text-xs whitespace-pre-wrap break-all font-mono">{{ treeError }}</div>
        <div v-if="treeErrorCode === 'hf_token_required'" class="text-xs text-rose-200/80 space-y-1">
          <div>可能原因（按概率）：</div>
          <ul class="ml-4 list-disc space-y-0.5">
            <li>模型是 <strong>gated</strong>（Llama / Gemma 等） — 需要先在
              <a href="https://huggingface.co/settings/gated-repos" target="_blank" class="text-sky-400 hover:underline">HF 官网</a>
              接受许可，再在
              <router-link to="/settings" class="text-sky-400 hover:underline">系统设置</router-link>
              配 <code>huggingface_token</code>
            </li>
            <li>HF 突发 rate limit — 等几秒再点刷新</li>
            <li>HF 临时不可达</li>
          </ul>
        </div>
      </div>
      <div v-else-if="treeFiles.length > 0" class="rounded-xl bg-white/[.04] overflow-hidden">
        <div class="flex items-center justify-between px-4 py-2 border-b border-white/[.06] text-xs text-slate-400 flex-wrap gap-2">
          <span>共 {{ treeFiles.length }} 个文件 · 合计 {{ treeTotalHuman }}</span>
          <span v-if="willExceedCap" class="text-rose-300">
            ⚠ 超出缓存上限 {{ cacheCapGb }} GB（{{ (oversizeRatio * 100).toFixed(0) }}%）
          </span>
          <span v-else-if="cacheCapGb > 0 && treeTotalBytes > cacheCapBytes * 0.7" class="text-amber-300">
            ⚠ 占缓存上限 {{ (oversizeRatio * 100).toFixed(0) }}%
          </span>
          <span class="text-slate-500">按 HF 返回顺序</span>
        </div>
        <div class="max-h-80 overflow-y-auto scrollbar">
          <table class="w-full text-sm">
            <thead class="text-xs text-slate-500 sticky top-0 bg-black/40 backdrop-blur">
              <tr>
                <th class="text-left px-4 py-2 font-medium">Path</th>
                <th class="text-right px-4 py-2 font-medium">大小</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="f in treeFiles"
                :key="f.path"
                class="border-b border-white/[.04] hover:bg-white/[.02]"
              >
                <td class="px-4 py-1.5 font-mono text-xs text-slate-200 truncate max-w-0" :title="f.path">
                  {{ f.path }}
                </td>
                <td class="px-4 py-1.5 text-right text-xs text-slate-400 font-mono whitespace-nowrap">
                  {{ f.size ? formatBytes(f.size) : '—' }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- HF preheat 任务列表 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-3">
      <div class="flex items-center justify-between">
        <h3 class="text-base font-semibold">HF 预热任务</h3>
        <el-button :icon="Refresh" size="small" plain :loading="preheat.loading" @click="preheat.fetch()">刷新</el-button>
      </div>
      <div v-if="hfTasks.length === 0" class="text-sm text-slate-500 py-4 text-center">
        暂无 HF 预热任务。在上方"全量预热"创建任务后会出现在这里。
      </div>
      <div v-else class="space-y-2">
        <div
          v-for="t in hfTasks"
          :key="t.id"
          class="rounded-xl border border-white/[.06] bg-white/[.02] p-3"
        >
          <div class="flex items-center justify-between gap-3 flex-wrap">
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-mono text-sm text-slate-200">#{{ t.id }}</span>
                <span class="text-sm text-slate-100 truncate">{{ t.name }}</span>
                <el-tag size="small" :type="statusType(t.status)" effect="plain">{{ t.status }}</el-tag>
                <span class="text-xs text-slate-500">{{ t.targets.length }} 文件</span>
              </div>
              <div class="mt-1 text-xs text-slate-500 flex items-center gap-3 flex-wrap">
                <span>进度：{{ t.progressDone }}/{{ t.progressTotal }} · {{ formatBytes(t.progressBytes) }}</span>
                <span v-if="t.lastDurationMs > 0">耗时 {{ Math.round(t.lastDurationMs / 1000) }}s</span>
                <span v-if="t.errorMessage" class="text-rose-300">⚠ {{ t.errorMessage }}</span>
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <el-button
                size="small"
                :icon="VideoPlay"
                plain
                :disabled="!auth.isAdmin || t.status === 'running'"
                @click="runTask(t.id)"
              >
                触发
              </el-button>
              <el-button
                :icon="Delete"
                size="small"
                type="danger"
                plain
                :disabled="!auth.isAdmin"
                @click="deleteTask(t.id)"
              />
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="!auth.isAdmin" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm text-amber-200">
      只读模式，登录管理员后才可触发全量预热。
    </div>
  </section>
</template>
