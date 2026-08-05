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
    <header class="page-header tasks-page-header">
      <div>
        <h1 id="tasks-heading">任务记录</h1>
      </div>
    </header>

    <section class="workspace-panel route-panel tasks-workspace" aria-label="同步任务记录" :aria-busy="loading">
      <TableSkeleton v-if="loading" label="正在加载任务记录" :columns="4" />
      <div v-else class="table-scroll tasks-table-scroll">
        <table class="data-table tasks-table" aria-label="任务状态列表">
          <thead>
            <tr>
              <th>任务类型</th>
              <th>范围</th>
              <th>状态</th>
              <th>开始时间</th>
            </tr>
          </thead>
          <tbody>
            <tr class="task-empty-row">
              <td colspan="4" class="task-empty-cell">
                <div class="task-empty-content">
                  <ListChecks :size="24" aria-hidden="true" />
                  <strong>暂无任务记录</strong>
                  <RouterLink class="primary-button" to="/sync">返回同步工作台</RouterLink>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </section>
</template>

<style scoped>
.tasks-page-header {
  min-height: 38px;
}

.tasks-workspace,
.tasks-table-scroll {
  box-shadow: none;
}

.tasks-table {
  min-width: 680px;
}

.tasks-table th:first-child {
  width: 28%;
}

.task-empty-cell {
  height: 220px;
  padding: 20px;
}

.task-empty-content {
  display: grid;
  justify-items: center;
  gap: 10px;
  color: var(--muted);
}

.task-empty-content strong {
  color: var(--ink);
  font-size: 15px;
}

@media (max-width: 620px) {
  .tasks-table {
    min-width: 0;
  }

  .tasks-table thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .tasks-table tbody,
  .tasks-table tbody tr,
  .tasks-table tbody td {
    display: block;
    width: 100%;
  }

  .task-empty-cell {
    min-height: 190px;
    border-bottom: 0;
  }
}
</style>
