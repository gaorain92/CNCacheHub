<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Lock, User } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import type { Registry, RegistryPatch } from '@/types/api'
import { useRegistriesStore } from '@/stores/registries'

const props = defineProps<{
  modelValue: boolean
  registry: Registry | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
}>()

const registries = useRegistriesStore()

// 本地 form state
const username = ref('')
const password = ref('')
const token = ref('')
const clearPassword = ref(false)
const clearToken = ref(false)
const saving = ref(false)

// 打开对话框时同步 server state
watch(
  () => [props.modelValue, props.registry],
  () => {
    if (props.modelValue && props.registry) {
      username.value = props.registry.username || ''
      password.value = '' // 永远不 prefill
      token.value = '' // 永远不 prefill
      clearPassword.value = false
      clearToken.value = false
    }
  },
  { immediate: true }
)

const dirty = computed(() => {
  if (!props.registry) return false
  if (username.value !== (props.registry.username || '')) return true
  if (password.value !== '') return true
  if (token.value !== '') return true
  if (clearPassword.value) return true
  if (clearToken.value) return true
  return false
})

const close = (): void => emit('update:modelValue', false)

async function save(): Promise<void> {
  if (!props.registry) return
  saving.value = true
  const patch: RegistryPatch = {}
  if (username.value !== (props.registry.username || '')) {
    patch.username = username.value
  }
  if (password.value !== '') {
    patch.password = password.value
  }
  if (token.value !== '') {
    patch.token = token.value
  }
  if (clearPassword.value) {
    patch.clearPassword = true
  }
  if (clearToken.value) {
    patch.clearToken = true
  }
  const updated = await registries.patch(props.registry.name, patch)
  saving.value = false
  if (updated) {
    ElMessage.success(`凭据已更新（${props.registry.name}）`)
    close()
  } else {
    ElMessage.error(registries.errorMessage || '保存失败')
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="编辑上游凭据"
    width="560px"
    :close-on-click-modal="false"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <div v-if="registry" class="space-y-4">
      <!-- Registry name (read-only) -->
      <div>
        <label class="text-xs text-slate-400 mb-1 block">Registry</label>
        <div class="text-sm text-slate-200 font-mono">
          {{ registry.name }} <span class="text-slate-500">→ {{ registry.upstreamUrl }}</span>
        </div>
      </div>

      <!-- Username -->
      <div class="space-y-1">
        <label class="text-xs text-slate-400 flex items-center gap-1">
          <el-icon :size="12"><User /></el-icon>
          用户名
        </label>
        <el-input v-model="username" placeholder="留空 = 不变" clearable />
      </div>

      <!-- Password -->
      <div class="space-y-1">
        <label class="text-xs text-slate-400 flex items-center gap-1">
          <el-icon :size="12"><Lock /></el-icon>
          密码
        </label>
        <el-input
          v-model="password"
          type="password"
          show-password
          :placeholder="registry.hasPassword ? '已设置 — 留空保持原值' : '未设置'"
        />
        <div v-if="registry.hasPassword" class="flex items-center gap-2 mt-1">
          <el-checkbox v-model="clearPassword" size="small" />
          <span class="text-xs text-rose-300">同时清空已有密码</span>
        </div>
      </div>

      <!-- Token -->
      <div class="space-y-1">
        <label class="text-xs text-slate-400 flex items-center gap-1">
          <el-icon :size="12"><Lock /></el-icon>
          Bearer Token（GHCR / 私有 registry 用）
        </label>
        <el-input
          v-model="token"
          type="password"
          show-password
          :placeholder="registry.hasToken ? '已设置 — 留空保持原值' : '未设置'"
        />
        <div v-if="registry.hasToken" class="flex items-center gap-2 mt-1">
          <el-checkbox v-model="clearToken" size="small" />
          <span class="text-xs text-rose-300">同时清空已有 token</span>
        </div>
      </div>

      <div class="rounded-xl bg-amber-500/5 border border-amber-500/20 p-3 text-xs text-amber-200 leading-relaxed">
        ⚠ 凭据用 AES-256-GCM 加密后存到 SQLite（master key 在
        <code>data_dir/.master_key</code>，权限 0600）。控制台 GET 永远不回显明文。
      </div>

      <div v-if="registries.errorMessage" class="rounded-xl bg-rose-500/5 border border-rose-500/20 p-3 text-xs text-rose-300">
        {{ registries.errorMessage }}
      </div>
    </div>

    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button
        type="primary"
        :loading="saving"
        :disabled="!dirty"
        @click="save"
      >
        保存
      </el-button>
    </template>
  </el-dialog>
</template>
