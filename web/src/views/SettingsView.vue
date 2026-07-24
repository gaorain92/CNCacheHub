<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Brush, Coin, Connection, Lock, Promotion, SetUp, Warning } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useSettingsStore } from '@/stores/settings'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'

const settings = useSettingsStore()
const auth = useAuthStore()
const access = useAccessStore()

const smallVpsOpt = ref(false)
const reserveSpaceGb = ref(5)
const maxObjectSizeMb = ref(1024)
const cacheTotalGb = ref(20)
const cleanupTriggerPct = ref(80)
const cleanupTargetPct = ref(60)

const saving = ref(false)

// === P2#4 代理访问控制本地 state ===
const acEnabled = ref(false)
const acToken = ref('')
const acTokenSet = ref(false)
const acIpWhitelistText = ref('')
const acLoopbackBypass = ref(true)
const acSaving = ref(false)

onMounted(async () => {
  if (!settings.data) await settings.fetch()
  syncFromStore()
  await access.fetch()
  syncAccessFromStore()
})

function syncFromStore(): void {
  if (!settings.data) return
  smallVpsOpt.value = settings.data.smallVpsOpt
  reserveSpaceGb.value = settings.data.reserveSpaceGb
  maxObjectSizeMb.value = settings.data.maxObjectSizeMb
  cacheTotalGb.value = settings.data.cacheTotalGb
  cleanupTriggerPct.value = settings.data.cleanupTriggerPct
  cleanupTargetPct.value = settings.data.cleanupTargetPct
}

function syncAccessFromStore(): void {
  if (!access.data) return
  acEnabled.value = access.data.enabled
  acTokenSet.value = access.data.tokenSet
  acToken.value = '' // 永远不要在 UI 显示已存的 token
  acIpWhitelistText.value = access.data.ipWhitelist.join('\n')
  acLoopbackBypass.value = access.data.loopbackBypass
}

watch(() => settings.data, syncFromStore)
watch(() => access.data, syncAccessFromStore)

// 派生：触发水位必须 > 目标水位
const validCleanupWater = computed(
  () => cleanupTriggerPct.value > cleanupTargetPct.value && cleanupTargetPct.value > 0
)

const dirty = computed(() => {
  if (!settings.data) return false
  return (
    smallVpsOpt.value !== settings.data.smallVpsOpt ||
    reserveSpaceGb.value !== settings.data.reserveSpaceGb ||
    maxObjectSizeMb.value !== settings.data.maxObjectSizeMb ||
    cacheTotalGb.value !== settings.data.cacheTotalGb ||
    cleanupTriggerPct.value !== settings.data.cleanupTriggerPct ||
    cleanupTargetPct.value !== settings.data.cleanupTargetPct
  )
})

// 派生的 access control dirty：任意字段被改
const acDirty = computed(() => {
  if (!access.data) return false
  if (acEnabled.value !== access.data.enabled) return true
  if (acLoopbackBypass.value !== access.data.loopbackBypass) return true
  // token 在 UI 永远 ""，但是如果用户输入了新值就认为 dirty
  if (acToken.value !== '') return true
  // ip whitelist 跟 store 对比
  const cur = acIpWhitelistText.value
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
  const orig = access.data.ipWhitelist
  if (cur.length !== orig.length) return true
  for (let i = 0; i < cur.length; i++) {
    if (cur[i] !== orig[i]) return true
  }
  return false
})

// 解析 IP 白名单（支持多行 / 逗号 / 空格）
function parseIpWhitelist(): string[] {
  return acIpWhitelistText.value
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

async function onSave(): Promise<void> {
  if (!validCleanupWater.value) {
    ElMessage.error('清理触发水位必须 > 目标水位')
    return
  }
  saving.value = true
  const ok = await settings.patch({
    smallVpsOpt: smallVpsOpt.value,
    reserveSpaceGb: reserveSpaceGb.value,
    maxObjectSizeMb: maxObjectSizeMb.value,
    cacheTotalGb: cacheTotalGb.value,
    cleanupTriggerPct: cleanupTriggerPct.value,
    cleanupTargetPct: cleanupTargetPct.value,
  })
  saving.value = false
  if (ok) {
    ElMessage.success('设置已保存')
  } else {
    ElMessage.error(settings.errorMessage || '保存失败')
  }
}

async function onAccessSave(): Promise<void> {
  acSaving.value = true
  // 只在用户输入了 token 时才传（避免意外清空）
  const patch: Parameters<typeof access.save>[0] = {
    enabled: acEnabled.value,
    ipWhitelist: parseIpWhitelist(),
    loopbackBypass: acLoopbackBypass.value,
  }
  if (acToken.value !== '') {
    patch.token = acToken.value
  }
  const ok = await access.save(patch)
  acSaving.value = false
  if (ok) {
    ElMessage.success('访问控制已更新（新策略立即生效）')
    // 重新 sync 一次（store 已是最新，watch 会触发，但保险手动调一次）
    syncAccessFromStore()
  } else {
    ElMessage.error(access.errorMessage || '保存失败')
  }
}

async function onAccessReset(): Promise<void> {
  syncAccessFromStore()
}

async function onReset(): Promise<void> {
  syncFromStore()
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function generateRandomToken(): void {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  let s = ''
  for (let i = 0; i < 32; i++) {
    s += chars.charAt(Math.floor(Math.random() * chars.length))
  }
  acToken.value = s
  ElMessage.info('已生成 32 位随机 token，点保存生效')
}
</script>

<template>
  <section class="space-y-6">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
          <el-icon :size="22" color="#020617"><SetUp /></el-icon>
        </div>
        <div>
          <h2 class="text-2xl font-semibold">系统设置</h2>
          <p class="text-sm text-slate-400">小容量 VPS 优化 · 缓存上限 · 清理策略</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <el-button v-if="dirty" plain @click="onReset">放弃修改</el-button>
        <el-button
          type="primary"
          :loading="saving"
          :disabled="!dirty"
          @click="onSave"
        >
          保存
        </el-button>
      </div>
    </header>

    <div v-if="settings.errorMessage" class="rounded-2xl border border-rose-500/20 bg-rose-500/5 p-4 text-sm text-rose-300">
      {{ settings.errorMessage }}
    </div>

    <!-- 小容量 VPS 优化 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#a3e635"><Promotion /></el-icon>
          <h3 class="text-base font-semibold">小容量 VPS 优化</h3>
          <span class="text-xs text-slate-500">单 VPS 1-2GB 内存 + 10-40GB 磁盘</span>
        </div>
        <el-switch
          v-model="smallVpsOpt"
          size="large"
          inline-prompt
          active-text="开启"
          inactive-text="关闭"
        />
      </div>
      <p class="text-sm text-slate-400 leading-relaxed">
        开启后将限制单对象落盘为 {{ formatBytes(maxObjectSizeMb * 1024 * 1024) }}、
        缓存总上限为 {{ cacheTotalGb }} GB，系统保底空间 {{ reserveSpaceGb }} GB。
        低于保底空间时进入只读旁路状态（仍转发请求，但不写缓存）。
      </p>
      <div v-if="smallVpsOpt" class="rounded-xl bg-mint/5 border border-mint/20 p-3 flex items-start gap-2">
        <el-icon :size="16" color="#a3e635" class="mt-0.5"><Warning /></el-icon>
        <div class="text-xs text-slate-300">
          启用期间，SteamCMD / Hugging Face 等超大文件预热会被阻止；超过单对象上限的请求会标记为
          <code class="text-mint">BYPASS_SIZE_LIMIT</code>，不写入缓存。
        </div>
      </div>
    </div>

    <!-- 缓存上限 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-5">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Coin /></el-icon>
        <h3 class="text-base font-semibold">缓存上限</h3>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-5">
        <div>
          <label class="text-xs text-slate-400 mb-2 block">缓存总上限（GB）</label>
          <el-input-number
            v-model="cacheTotalGb"
            :min="1"
            :max="1000"
            :step="1"
            controls-position="right"
            class="w-full"
          />
          <p class="text-xs text-slate-500 mt-1">超过后清理到目标水位</p>
        </div>
        <div>
          <label class="text-xs text-slate-400 mb-2 block">系统保底空间（GB）</label>
          <el-input-number
            v-model="reserveSpaceGb"
            :min="1"
            :max="100"
            :step="1"
            controls-position="right"
            class="w-full"
          />
          <p class="text-xs text-slate-500 mt-1">低于则只读旁路</p>
        </div>
        <div>
          <label class="text-xs text-slate-400 mb-2 block">最大单对象（MB）</label>
          <el-input-number
            v-model="maxObjectSizeMb"
            :min="1"
            :max="10240"
            :step="64"
            controls-position="right"
            class="w-full"
          />
          <p class="text-xs text-slate-500 mt-1">超出走旁路</p>
        </div>
      </div>
    </div>

    <!-- 清理策略 -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-5">
      <div class="flex items-center gap-2">
        <el-icon :size="18" color="#94a3b8"><Brush /></el-icon>
        <h3 class="text-base font-semibold">清理策略</h3>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-5">
        <div>
          <label class="text-xs text-slate-400 mb-2 block">
            触发水位（%）— 超过后自动清理
          </label>
          <el-slider
            v-model="cleanupTriggerPct"
            :min="50"
            :max="95"
            :step="5"
            show-input
            :show-input-controls="true"
            input-size="small"
          />
        </div>
        <div>
          <label class="text-xs text-slate-400 mb-2 block">
            目标水位（%）— 清理到此水位
          </label>
          <el-slider
            v-model="cleanupTargetPct"
            :min="30"
            :max="cleanupTriggerPct - 5"
            :step="5"
            show-input
            :show-input-controls="true"
            input-size="small"
          />
        </div>
      </div>
      <div v-if="!validCleanupWater" class="rounded-xl bg-rose-500/5 border border-rose-500/20 p-3 text-xs text-rose-300">
        ⚠ 触发水位必须大于目标水位
      </div>
    </div>

    <!-- 代理访问控制（P2#4 / PRD §9.7.2） -->
    <div class="rounded-2xl border border-white/[.08] bg-black/20 p-6 space-y-5">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <el-icon :size="18" color="#a3e635"><Connection /></el-icon>
          <h3 class="text-base font-semibold">代理访问控制</h3>
          <span class="text-xs text-slate-500">/v2/* + /r/* 鉴权</span>
        </div>
        <el-switch
          v-model="acEnabled"
          size="large"
          inline-prompt
          active-text="开启"
          inactive-text="关闭"
        />
      </div>
      <p class="text-sm text-slate-400 leading-relaxed">
        公网部署时建议开启。开启后所有
        <code class="text-mint">/v2/*</code>
        和
        <code class="text-mint">/r/*</code>
        请求必须满足至少一项凭据（Token 匹配 OR IP 在白名单中）。
        关闭时完全开放。
      </p>

      <div v-if="acEnabled" class="rounded-xl bg-amber-500/5 border border-amber-500/20 p-3 flex items-start gap-2">
        <el-icon :size="16" color="#f59e0b" class="mt-0.5"><Warning /></el-icon>
        <div class="text-xs text-amber-200 leading-relaxed">
          开启后立即生效。如果既没配 Token 又没配 IP 白名单、又关闭了 Loopback 放行，所有访问都会被拒。
          请先配至少一项凭据再点保存。
        </div>
      </div>

      <!-- Token -->
      <div class="space-y-2">
        <div class="flex items-center justify-between">
          <label class="text-xs text-slate-400">访问 Token（Bearer / X-CNCacheHub-Token）</label>
          <div class="flex items-center gap-2">
            <span v-if="acTokenSet" class="text-xs text-mint">已设置</span>
            <span v-else class="text-xs text-slate-500">未设置</span>
            <el-button size="small" plain @click="generateRandomToken">生成随机</el-button>
          </div>
        </div>
        <el-input
          v-model="acToken"
          type="password"
          show-password
          placeholder="留空 = 保持原值；填入新值 = 替换（不会回显）"
          :disabled="!auth.isAdmin"
        />
        <p class="text-xs text-slate-500">客户端请求时带 <code class="text-mint">Authorization: Bearer &lt;token&gt;</code> 或 <code class="text-mint">X-CNCacheHub-Token: &lt;token&gt;</code> 头即可通过。</p>
      </div>

      <!-- IP 白名单 -->
      <div class="space-y-2">
        <label class="text-xs text-slate-400">IP 白名单（CIDR，每行一个，例 <code class="text-mint">10.0.0.0/8</code>）</label>
        <el-input
          v-model="acIpWhitelistText"
          type="textarea"
          :rows="4"
          placeholder="10.0.0.0/8&#10;192.168.0.0/16&#10;172.16.0.0/12"
          :disabled="!auth.isAdmin"
        />
      </div>

      <!-- Loopback Bypass -->
      <div class="flex items-center justify-between rounded-xl border border-white/[.05] bg-black/10 p-3">
        <div>
          <div class="text-sm">Loopback 放行（127.0.0.1 / ::1 永过）</div>
          <p class="text-xs text-slate-500 mt-1">关闭后 localhost 也要带 token，方便严格模式部署</p>
        </div>
        <el-switch
          v-model="acLoopbackBypass"
          :disabled="!auth.isAdmin"
        />
      </div>

      <!-- 操作 -->
      <div class="flex items-center justify-end gap-2 pt-2">
        <el-button v-if="acDirty" plain @click="onAccessReset">放弃修改</el-button>
        <el-button
          type="primary"
          :loading="acSaving"
          :disabled="!acDirty"
          @click="onAccessSave"
        >
          保存访问控制
        </el-button>
      </div>
    </div>

    <!-- 权限提示 -->
    <div v-if="!auth.isAdmin" class="rounded-2xl border border-amber-500/20 bg-amber-500/5 p-4 flex items-start gap-3">
      <el-icon :size="20" color="#f59e0b"><Lock /></el-icon>
      <div class="text-sm text-amber-200">
        只有管理员可以修改设置。当前以只读模式显示。
      </div>
    </div>

    <!-- 最后修改时间 -->
    <div v-if="settings.data?.updatedAt" class="text-xs text-slate-500 text-right">
      最后修改：{{ new Date(settings.data.updatedAt * 1000).toLocaleString('zh-CN', { hour12: false }) }}
    </div>
  </section>
</template>
