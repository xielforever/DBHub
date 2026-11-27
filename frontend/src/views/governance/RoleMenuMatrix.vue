<script setup lang="ts">
import type { Menu, MenuItem, Role, RoleMenuVisibility } from '../../types/iam'

type Choice = 'show' | 'hide' | ''

const props = defineProps<{
  items: MenuItem[]
  catalog: Menu[]
  roles: Role[]
  visibility: RoleMenuVisibility
  reservedActions: string[]
  disabled: boolean
}>()
const emit = defineEmits<{ change: [roleId: string, menuId: string, choice: Choice] }>()

function capability(menuId: string) {
  return props.catalog.find(item => item.id === menuId)?.capability
}

function mayShow(role: Role, menuId: string) {
  const action = capability(menuId)
  return role.enabled && !!action && role.actions.includes(action)
}

function reserved(menuId: string) {
  const action = capability(menuId)
  return !!action && props.reservedActions.includes(action)
}

function change(roleId: string, menuId: string, event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (value === '' || value === 'show' || value === 'hide') emit('change', roleId, menuId, value)
}
</script>

<template>
  <div class="space-y-3">
    <p class="text-sm text-muted">具备页面许可的任一有效角色允许显示时，该菜单可见。空间统一隐藏仍然有效。</p>
    <div v-if="roles.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="角色菜单矩阵">
      <table class="editorial-table" aria-label="角色菜单矩阵">
        <thead>
          <tr>
            <th scope="col" class="whitespace-nowrap">菜单</th>
            <th v-for="role in roles" :key="role.id" scope="col" class="min-w-40">
              {{ role.name }}
              <span v-if="!role.enabled" class="block text-xs text-muted">已停用</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="menu in items" :key="menu.id">
            <th scope="row">
              <span class="whitespace-nowrap">{{ menu.label }}</span>
              <span v-if="!menu.visible" class="block text-xs text-muted">空间统一隐藏</span>
            </th>
            <td v-for="role in roles" :key="role.id">
              <span v-if="reserved(menu.id)" class="text-sm text-muted">超级管理员专属</span>
              <template v-else>
                <select
                  :value="visibility[role.id]?.[menu.id] ?? ''"
                  class="glass-input w-full"
                  :aria-label="`${role.name}的${menu.label}菜单`"
                  :disabled="disabled || !role.enabled"
                  @change="change(role.id, menu.id, $event)"
                >
                  <option value="">继承</option>
                  <option value="show" :disabled="!mayShow(role, menu.id)">显示</option>
                  <option value="hide">不显示</option>
                </select>
                <span v-if="role.enabled && !mayShow(role, menu.id)" class="block text-xs text-muted">角色暂无页面许可</span>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="text-sm text-muted">暂无可按角色配置的菜单。创建获准角色后可设置。</p>
  </div>
</template>
