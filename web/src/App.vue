<script setup lang="ts">
import AppLayout from '@/layouts/AppLayout.vue'
</script>

<template>
  <AppLayout>
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
