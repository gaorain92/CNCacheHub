<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()

const auth = useAuthStore()

const form = reactive({
  oldPassword: '',
  newPassword: '',
  confirm: '',
})

const submitting = ref(false)
const formRef = ref()

const rules = {
  oldPassword: [
    { required: true, message: '请输入当前密码', trigger: 'blur' as const },
  ],
  newPassword: [
    { required: true, message: '请输入新密码', trigger: 'blur' as const },
    { min: 8, message: '密码至少 8 位', trigger: 'blur' as const },
  ],
  confirm: [
    { required: true, message: '请再次输入新密码', trigger: 'blur' as const },
    {
      validator: (_: unknown, v: string, cb: (e?: Error) => void) => {
        if (v !== form.newPassword) cb(new Error('两次输入的密码不一致'))
        else cb()
      },
      trigger: 'blur' as const,
    },
  ],
}

watch(
  () => props.modelValue,
  (v) => {
    if (!v) {
      form.oldPassword = ''
      form.newPassword = ''
      form.confirm = ''
      formRef.value?.clearValidate()
    }
  }
)

async function onSubmit(): Promise<void> {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  if (form.newPassword === form.oldPassword) {
    ElMessage.error('新密码不能与旧密码相同')
    return
  }
  submitting.value = true
  const ok = await auth.changePassword(form.oldPassword, form.newPassword)
  submitting.value = false
  if (ok) {
    ElMessage.success('密码已修改')
    emit('update:modelValue', false)
  } else {
    ElMessage.error(auth.errorMessage || '修改失败')
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="修改密码"
    width="420px"
    :close-on-click-modal="false"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="onSubmit"
    >
      <el-form-item label="当前密码" prop="oldPassword">
        <el-input
          v-model="form.oldPassword"
          type="password"
          show-password
          autocomplete="current-password"
          placeholder="当前登录密码"
        />
      </el-form-item>
      <el-form-item label="新密码（至少 8 位）" prop="newPassword">
        <el-input
          v-model="form.newPassword"
          type="password"
          show-password
          autocomplete="new-password"
          placeholder="新密码"
        />
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirm">
        <el-input
          v-model="form.confirm"
          type="password"
          show-password
          autocomplete="new-password"
          placeholder="再次输入新密码"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="onSubmit">确认修改</el-button>
    </template>
  </el-dialog>
</template>
