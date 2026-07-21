<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/layouts/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()

// 公开页面（login / init / not-found）不显示侧栏
const useBareLayout = computed(() => {
  if (route.meta?.public) return true
  if (!auth.isAuthenticated) return true
  return false
})
</script>

<template>
  <router-view v-if="useBareLayout" />
  <AppLayout v-else>
    <router-view v-slot="{ Component }">
      <transition name="rise" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>
  </AppLayout>
</template>

<style>
.rise-enter-active,
.rise-leave-active {
  transition: opacity 0.22s ease, transform 0.22s ease;
}
.rise-enter-from {
  opacity: 0;
  transform: translateY(8px);
}
.rise-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
