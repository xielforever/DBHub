<script setup lang="ts">
import AppBackdrop from './components/base/AppBackdrop.vue'
</script>

<template>
  <AppBackdrop />
  <RouterView v-slot="{ Component }">
    <Transition name="page-fade" mode="out-in">
      <component :is="Component" />
    </Transition>
  </RouterView>
</template>

<style scoped>
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity var(--db-motion-normal) var(--db-motion-ease);
}
.page-fade-enter-from,
.page-fade-leave-to {
  opacity: 0;
}
</style>
