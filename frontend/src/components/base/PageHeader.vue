<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string; eyebrow?: string; heading?: 'h1' | 'h2' }>(), {
  heading: 'h1',
})
</script>

<template>
  <header class="page-header">
    <div class="page-header__content">
      <p v-if="eyebrow" class="page-header__eyebrow">{{ eyebrow }}</p>
      <component :is="heading" class="page-header__title">{{ title }}</component>
      <p v-if="description" class="page-header__description">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="page-header__actions"><slot name="actions" /></div>
  </header>
</template>
