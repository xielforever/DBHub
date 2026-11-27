<script setup lang="ts">
import { computed } from 'vue'
import { Check, ChevronDown, Monitor, Moon, Sun } from 'lucide-vue-next'
import { isThemePreference, useTheme } from '../../utils/theme'

withDefaults(defineProps<{ compact?: boolean }>(), { compact: false })
const { themePreference, resolvedTheme, setThemePreference } = useTheme()
const label = computed(() => themePreference.value === 'system' ? '跟随系统' : resolvedTheme.value === 'dark' ? '深色' : '浅色')
const icon = computed(() => themePreference.value === 'system' ? Monitor : resolvedTheme.value === 'dark' ? Moon : Sun)
const choices = [
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
] as const

function selectTheme(value: unknown) {
  if (isThemePreference(value)) setThemePreference(value)
}
</script>

<template>
  <el-dropdown trigger="click" @command="selectTheme">
    <button type="button" class="ghost-button theme-switcher ui-control-target" :aria-label="`外观：${label}，选择主题`">
      <component :is="icon" class="w-4 h-4" aria-hidden="true" />
      <span v-if="!compact" class="theme-switcher__label">{{ label }}</span>
      <ChevronDown v-if="!compact" class="theme-switcher__chevron w-3.5 h-3.5" aria-hidden="true" />
    </button>
    <template #dropdown>
      <el-dropdown-menu aria-label="选择外观主题">
        <el-dropdown-item v-for="choice in choices" :key="choice.value" :command="choice.value">
          <component :is="choice.icon" class="w-4 h-4 mr-2" aria-hidden="true" />
          <span>{{ choice.label }}</span>
          <span v-if="themePreference === choice.value" class="sr-only">，当前选择</span>
          <Check v-if="themePreference === choice.value" class="w-4 h-4 ml-4 text-accent" aria-hidden="true" />
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>
