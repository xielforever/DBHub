<script setup lang="ts">
import LoadingState from './LoadingState.vue'
import { CircleAlert, Info } from 'lucide-vue-next'

withDefaults(defineProps<{
  title: string
  description: string
  tone?: 'neutral' | 'info' | 'warning' | 'danger'
  loading?: boolean
  heading?: 'h1' | 'h2' | 'h3'
}>(), { tone: 'neutral', loading: false, heading: 'h2' })
</script>

<template>
  <section class="glass-panel page-state" :class="`page-state--${tone}`" :role="!loading && tone === 'danger' ? 'alert' : undefined">
    <LoadingState v-if="loading" variant="detail" :label="title" />
    <component v-else :is=" tone === 'danger' || tone === 'warning' ? CircleAlert : Info"
      class="page-state__icon" :class="{ 'text-danger': tone === 'danger', 'text-warning': tone === 'warning' }" aria-hidden="true" />
    <component v-if="!loading" :is="heading" class="page-state__title">{{ title }}</component>
    <p class="page-state__description">{{ description }}</p>
    <div v-if="$slots.actions" class="page-state__actions"><slot name="actions" /></div>
  </section>
</template>
