<script setup lang="ts">
/**
 * 状态点
 * - ok: 绿（健康）
 * - warn: 琥珀（降级/告警）
 * - down: 红（不可用）
 * - unknown: 灰（未连接/未知）
 *
 * props.status 必填；pulse 默认 true，对 ok/warn 启用呼吸动画
 */
type Status = 'ok' | 'warn' | 'down' | 'unknown'

withDefaults(
  defineProps<{
    status: Status
    pulse?: boolean
    size?: 'sm' | 'md' | 'lg'
    label?: string
  }>(),
  { pulse: true, size: 'md', label: '' },
)

const sizeMap: Record<'sm' | 'md' | 'lg', string> = {
  sm: 'h-1.5 w-1.5',
  md: 'h-2 w-2',
  lg: 'h-2.5 w-2.5',
}

const colorMap: Record<Status, string> = {
  ok: 'bg-mint shadow-[0_0_8px_rgba(45,212,191,.65)]',
  warn: 'bg-amber-300 shadow-[0_0_8px_rgba(252,211,77,.55)]',
  down: 'bg-rose-400 shadow-[0_0_8px_rgba(251,113,133,.55)]',
  unknown: 'bg-slate-500',
}
</script>

<template>
  <span class="inline-flex items-center gap-2 align-middle">
    <span
      :class="[
        'inline-block rounded-full',
        sizeMap[size],
        colorMap[status],
        pulse && (status === 'ok' || status === 'warn') ? 'animate-pulse' : '',
      ]"
    />
    <span v-if="label" class="text-xs text-slate-300">{{ label }}</span>
  </span>
</template>
