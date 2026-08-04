<script setup lang="ts">
import { ListChecks } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'

import TableSkeleton from '@/components/TableSkeleton.vue'

withDefaults(defineProps<{ loading?: boolean }>(), {
  loading: false,
})
</script>

<template>
  <section class="page" aria-labelledby="tasks-heading">
    <header class="page-header">
      <div>
        <h1 id="tasks-heading">任务记录</h1>
      </div>
    </header>

    <section class="workspace-panel route-panel" aria-label="同步任务记录" :aria-busy="loading">
      <TableSkeleton v-if="loading" label="正在加载任务记录" :columns="4" />
      <div v-else class="state-panel route-empty-state">
        <ListChecks :size="28" aria-hidden="true" />
        <h2>暂无任务记录</h2>
        <RouterLink class="primary-button" to="/sync">返回同步工作台</RouterLink>
      </div>
    </section>
  </section>
</template>
