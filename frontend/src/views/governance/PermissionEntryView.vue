<script setup lang="ts">
import {computed} from 'vue'
import {useRoute} from 'vue-router'
import {useUserStore} from '../../stores/user'
import ReportsView from '../report/ReportsView.vue'
import DashboardsView from '../dashboard/ManagedDashboardsView.vue'
import QueryView from '../query/QueryView.vue'
import MyConnectionsView from './MyConnectionsView.vue'
import AccessStatusView from './AccessStatusView.vue'
const route=useRoute(),user=useUserStore()
const entries={reports:{label:'报表',component:ReportsView,actions:['report.view','report.design','report.review','report.publish','report.offline','report.export','report.exportReview']},dashboards:{label:'仪表盘',component:DashboardsView,actions:['dashboard.view','dashboard.design','dashboard.review','dashboard.publish','dashboard.offline']},query:{label:'数据工作台',component:QueryView,actions:['query.read','query.write']},connections:{label:'我的连接',component:MyConnectionsView,actions:['connection.page']}}
const entry=computed(()=>entries[String(route.name) as keyof typeof entries]||entries.reports),allowed=computed(()=>!user.actionControlled||entry.value.actions.some(user.can))
</script>

<template>
  <component v-if="allowed" :is="entry.component" />
  <AccessStatusView v-else kind="unavailable" :entry-label="entry.label" />
</template>
