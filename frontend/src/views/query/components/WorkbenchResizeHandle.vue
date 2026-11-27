<script setup lang="ts">
const props = defineProps<{ direction: 'horizontal' | 'vertical'; min: number; max: number; label: string; reverse?: boolean }>()
const model = defineModel<number>({ required: true })
let initial: { pointer: number; coordinate: number; size: number } | undefined

function clamp(value: number) {
  model.value = Math.round(Math.max(props.min, Math.min(props.max, value)))
}

function coordinate(event: PointerEvent) {
  return props.direction === 'vertical' ? event.clientX : event.clientY
}

function begin(event: PointerEvent) {
  if (event.button !== 0 || initial) return
  const handle = event.currentTarget as HTMLElement
  handle.focus({ preventScroll: true })
  handle.setPointerCapture(event.pointerId)
  initial = { pointer: event.pointerId, coordinate: coordinate(event), size: model.value }
}

function move(event: PointerEvent) {
  if (initial?.pointer !== event.pointerId) return
  clamp(initial.size + (coordinate(event) - initial.coordinate) * (props.reverse ? -1 : 1))
}

function finish(event: PointerEvent) {
  if (initial?.pointer === event.pointerId) initial = undefined
}

function adjust(event: KeyboardEvent) {
  const [before, after] = props.direction === 'vertical' ? ['ArrowLeft', 'ArrowRight'] : ['ArrowUp', 'ArrowDown']
  if (![before, after, 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  if (event.key === 'Home') clamp(props.min)
  else if (event.key === 'End') clamp(props.max)
  else clamp(model.value + (event.key === before ? -16 : 16) * (props.reverse ? -1 : 1))
}
</script>

<template>
  <div class="workbench-resize ui-focus-target" :class="direction" role="separator" tabindex="0"
    :aria-label="label" :aria-orientation="direction" :aria-valuemin="min" :aria-valuemax="max" :aria-valuenow="model"
    @pointerdown="begin" @pointermove="move" @pointerup="finish" @lostpointercapture="finish" @pointercancel="finish" @keydown="adjust" />
</template>

<style scoped>
.workbench-resize { display: grid; place-items: center; touch-action: none; user-select: none; }
.workbench-resize::before { content: ''; display: block; border-radius: 3px; background: var(--db-divider); }
.workbench-resize:hover::before, .workbench-resize:focus-visible::before { background: var(--db-accent); }
.vertical { cursor: col-resize; min-width: 8px; }
.vertical::before { width: 3px; height: 24px; }
.horizontal { cursor: row-resize; min-height: 8px; }
.horizontal::before { height: 3px; width: 24px; }
</style>
