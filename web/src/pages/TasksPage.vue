<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CircleAlert, ListChecks, RotateCcw } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import TableSkeleton from '@/components/TableSkeleton.vue'
import type { TaskHistoryRecord } from '@/types'

export interface TaskRecord {
  id: string
  type: string
  scope: string
  status: 'running' | 'succeeded' | 'partially_failed' | 'failed'
  startedAt: string
  completedAt?: string
  completed?: boolean
  summary?: { total: number; succeeded: number; failed: number }
  detail?: string
}

// `undefined` distinguishes route-owned auto-loading from legacy controlled tests.
const props = withDefaults(defineProps<{
  loading?: boolean
  error?: string
  tasks?: TaskRecord[]
}>(), {
  loading: undefined,
  error: '',
  tasks: () => undefined as unknown as TaskRecord[],
})

const emit = defineEmits<{ retry: [] }>()
const taskRows = ref<TaskRecord[]>([])
const loadState = ref<'loading' | 'ready' | 'error'>(
  props.tasks === undefined && props.loading === undefined && !props.error ? 'loading' : 'ready',
)
const loadError = ref('')
const displayedTasks = computed(() => props.tasks ?? taskRows.value)
const displayedLoading = computed(() => props.loading === true || (props.tasks === undefined && loadState.value === 'loading'))
const displayedError = computed(() => props.error || (props.tasks === undefined ? loadError.value : ''))

function taskStatusLabel(status: TaskRecord['status']): string {
  if (status === 'running') return '进行中'
  if (status === 'succeeded') return '已完成'
  if (status === 'partially_failed') return '部分失败'
  return '失败'
}

function taskTypeLabel(type: string): string {
  if (type === 'sync') return '资产同步'
  if (type === 'reconcile') return '目标校验'
  if (type === 'discover') return '模型发现'
  return type
}

function retryTasks(): void {
  if (props.tasks !== undefined || props.error || props.loading !== undefined) {
    emit('retry')
    return
  }
  void loadTasks()
}

function taskRow(task: TaskHistoryRecord): TaskRecord {
  return {
    id: task.task_id,
    type: task.type,
    scope: task.scope,
    status: task.status,
    startedAt: task.started_at,
    completedAt: task.completed_at ?? '',
    completed: task.completed,
    summary: task.summary,
  }
}

async function loadTasks(): Promise<void> {
  if (props.tasks !== undefined || props.error || props.loading !== undefined) return
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const response = await api.getTasks()
    taskRows.value = response.tasks.map(taskRow)
    loadState.value = 'ready'
  } catch (error) {
    loadError.value = safeErrorMessage(error)
    loadState.value = 'error'
  }
}

function summaryLabel(task: TaskRecord): string {
  if (!task.summary) return ''
  return `${task.summary.succeeded}/${task.summary.total} 成功，${task.summary.failed} 失败`
}

onMounted(() => {
  if (props.tasks === undefined && !props.error && props.loading === undefined) void loadTasks()
})
</script>

<template>
  <section class="page" aria-labelledby="tasks-heading">
    <header class="page-header tasks-page-header">
      <div>
        <h1 id="tasks-heading">任务记录</h1>
      </div>
    </header>

    <section class="workspace-panel route-panel tasks-workspace" aria-label="同步任务记录" :aria-busy="displayedLoading">
      <TableSkeleton v-if="displayedLoading" label="正在加载任务记录" :columns="5" />
      <div v-else-if="displayedError" class="task-state task-error" role="alert">
        <CircleAlert :size="24" aria-hidden="true" />
        <strong>任务记录加载失败</strong>
        <p>{{ displayedError }}</p>
        <button class="secondary-button" type="button" aria-label="重试任务记录" @click="retryTasks">
          <RotateCcw :size="16" aria-hidden="true" />
          重试
        </button>
      </div>
      <div v-else class="table-scroll tasks-table-scroll">
        <table class="data-table tasks-table" aria-label="任务状态列表">
          <thead>
            <tr>
              <th>任务类型</th>
              <th>范围</th>
              <th>状态</th>
              <th>开始时间</th>
              <th>完成时间</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="displayedTasks.length === 0" class="task-empty-row">
              <td colspan="5" class="task-empty-cell">
                <div class="task-empty-content">
                  <ListChecks :size="24" aria-hidden="true" />
                  <strong>暂无任务记录</strong>
                  <RouterLink class="primary-button" to="/sync">返回同步工作台</RouterLink>
                </div>
              </td>
            </tr>
            <tr v-for="task in displayedTasks" :key="task.id" :aria-label="`${taskTypeLabel(task.type)} ${task.scope}`">
              <td data-label="任务类型">
                <div class="task-primary-cell">
                  <RouterLink :to="{ name: 'task-detail', params: { id: task.id } }">
                    {{ taskTypeLabel(task.type) }}
                  </RouterLink>
                  <code>{{ task.id }}</code>
                </div>
              </td>
              <td data-label="范围">{{ task.scope }}</td>
              <td data-label="状态">
                <span class="task-status" :class="`is-${task.status}`">{{ taskStatusLabel(task.status) }}</span>
                <small v-if="task.detail" class="task-detail">{{ task.detail }}</small>
                <small v-if="summaryLabel(task)" class="task-detail">{{ summaryLabel(task) }}</small>
              </td>
              <td data-label="开始时间">{{ task.startedAt }}</td>
              <td data-label="完成时间">{{ task.completedAt || '--' }}</td>
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

.task-state {
  display: grid;
  min-height: 220px;
  place-content: center;
  justify-items: center;
  gap: 8px;
  padding: 24px;
  color: var(--muted);
  text-align: center;
}

.task-state strong {
  color: var(--ink);
  font-size: 15px;
}

.task-state p {
  max-width: 52ch;
  margin: 0;
  font-size: 12px;
}

.task-error {
  color: var(--danger, #b91c1c);
}

.task-primary-cell {
  display: grid;
  gap: 3px;
}

.task-primary-cell a {
  color: var(--ink);
  font-weight: 650;
  text-decoration: none;
}

.task-primary-cell a:hover {
  text-decoration: underline;
}

.task-primary-cell code {
  color: var(--muted);
  font-size: 11px;
}

.task-status {
  display: inline-flex;
  min-height: 22px;
  align-items: center;
  padding: 2px 7px;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 650;
}

.task-status.is-running {
  color: var(--blue, #1d4ed8);
  border-color: color-mix(in srgb, var(--blue, #1d4ed8) 30%, var(--line));
}

.task-status.is-succeeded {
  color: var(--green, #15803d);
  border-color: color-mix(in srgb, var(--green, #15803d) 30%, var(--line));
}

.task-status.is-partially_failed,
.task-status.is-failed {
  color: var(--danger, #b91c1c);
  border-color: color-mix(in srgb, var(--danger, #b91c1c) 30%, var(--line));
}

.task-detail {
  display: block;
  margin-top: 4px;
  color: var(--muted);
  font-size: 11px;
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

  .task-state {
    min-height: 190px;
  }
}
</style>
