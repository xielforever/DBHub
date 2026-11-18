<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import type { PageCategory } from '../../composables/usePageCategory'

const props = defineProps<{
  label: string
  categories: readonly PageCategory[]
  activeId?: string
  hideTabs?: boolean
}>()
const emit = defineEmits<{ select: [id: string] }>()
const id = useId()
const tabList = ref<HTMLDivElement>()
const focusedId = ref<string>()
const tabStop = computed(() =>
  props.categories.find(item => item.id === focusedId.value)?.id ??
  props.activeId ?? props.categories[0]?.id,
)

function moveFocus(event: KeyboardEvent, index: number) {
  const count = props.categories.length
  let next: number
  switch (event.key) {
    case 'ArrowRight': next = (index + 1) % count; break
    case 'ArrowLeft': next = (index + count - 1) % count; break
    case 'Home': next = 0; break
    case 'End': next = count - 1; break
    default: return
  }
  event.preventDefault()
  tabList.value?.querySelectorAll<HTMLButtonElement>('button')[next]?.focus()
}

function resetFocus(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node) || !tabList.value?.contains(event.relatedTarget)) {
    focusedId.value = undefined
  }
}
</script>

<template>
  <div v-if="categories.length && !hideTabs" ref="tabList" class="segmented-control" role="tablist" :aria-label="label" @focusout="resetFocus">
    <button v-for="(item, index) in categories" :id="`${id}-tab-${item.id}`" :key="item.id"
      type="button" role="tab" class="ghost-button ui-control-target"
      :aria-controls="`${id}-panel-${item.id}`" :aria-selected="item.id === activeId"
      :tabindex="item.id === tabStop ? 0 : -1" @focus="focusedId = item.id"
      @keydown="moveFocus($event, index)" @click="emit('select', item.id)">
      {{ item.name }}
    </button>
  </div>
  <div v-for="item in categories" :id="`${id}-panel-${item.id}`" :key="item.id"
    :role="hideTabs ? undefined : 'tabpanel'" :aria-labelledby="hideTabs ? undefined : `${id}-tab-${item.id}`"
    :hidden="item.id !== activeId" :tabindex="hideTabs ? undefined : 0">
    <slot v-if="item.id === activeId" />
  </div>
  <slot v-if="!activeId" name="unavailable" />
</template>
