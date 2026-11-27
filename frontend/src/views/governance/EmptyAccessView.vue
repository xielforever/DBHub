<script setup lang="ts">
import { useRoute } from 'vue-router'
import AccessStatusView from './AccessStatusView.vue'

const route = useRoute()
</script>

<template>
  <AccessStatusView :kind="route.name === 'access-denied' ? 'denied' : 'no-menu'" />
</template>
