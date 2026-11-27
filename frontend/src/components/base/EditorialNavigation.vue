<script setup lang="ts">
import { nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { Menu } from 'lucide-vue-next'
import type { EditorialGroup } from '../../utils/editorial-navigation'

const props = defineProps<{ groups: EditorialGroup[]; activePath: string; contextKey?: string }>()
const emit = defineEmits<{ navigate: [path: string]; openChange: [open: boolean] }>()
const open = ref(false)
const trigger = ref<HTMLButtonElement>()
const expandedGroup = ref('')
let focusMainOnClose = false
function navigate(path: string) { focusMainOnClose = path !== props.activePath; open.value = false; emit('navigate', path) }
function restoreFocus() {
  void nextTick(() => {
    if (focusMainOnClose) document.getElementById('main-content')?.focus()
    else trigger.value?.focus()
    focusMainOnClose = false
  })
}
watch(open, value => emit('openChange', value))
watch(() => [props.activePath, props.contextKey], () => { open.value = false })
let desktop: MediaQueryList | undefined
function breakpoint(event: MediaQueryListEvent) { if (event.matches) open.value = false }
onMounted(() => { desktop = window.matchMedia('(min-width: 1024px)'); desktop.addEventListener('change', breakpoint) })
onScopeDispose(() => desktop?.removeEventListener('change', breakpoint))
</script>

<template>
  <nav class="editorial-navigation" aria-label="工作空间导航">
    <div class="editorial-navigation__desktop">
      <el-dropdown v-for="group in groups" :key="group.name" trigger="click" placement="bottom-start"
        @visible-change="expandedGroup = $event ? group.name : ''" @command="navigate">
        <button type="button" class="editorial-nav-trigger" :class="{ 'is-active': group.items.some(item => item.path === activePath) }"
          :aria-expanded="expandedGroup === group.name" aria-haspopup="menu">{{ group.name }}</button>
        <template #dropdown>
          <el-dropdown-menu :aria-label="group.name">
            <el-dropdown-item v-for="item in group.items" :key="item.id" :command="item.path"
              :class="{ 'editorial-menu-current': item.path === activePath }" :aria-current="item.path === activePath ? 'page' : undefined">{{ item.label }}</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
    <button ref="trigger" type="button" class="ghost-button editorial-navigation__mobile" aria-label="打开导航"
      aria-controls="editorial-mobile-navigation" :aria-expanded="open" @click="open = true"><Menu class="w-4 h-4" aria-hidden="true" /><span>导航</span></button>
    <el-drawer v-model="open" title="工作空间导航" direction="ltr" size="min(320px, 100vw)" append-to-body
      @closed="restoreFocus">
      <nav id="editorial-mobile-navigation" aria-label="移动端空间功能" class="editorial-mobile-nav">
        <section v-for="group in groups" :key="group.name">
          <h2>{{ group.name }}</h2>
          <button v-for="item in group.items" :key="item.id" type="button" class="nav-item"
            :aria-current="item.path === activePath ? 'page' : undefined" @click="navigate(item.path)">{{ item.label }}</button>
        </section>
        <p v-if="!groups.length" class="text-muted">没有可显示的菜单，请从账号入口查看当前权限。</p>
      </nav>
    </el-drawer>
  </nav>
</template>
