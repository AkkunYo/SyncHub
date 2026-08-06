<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ArrowLeft, CircleAlert, ListChecks, RotateCcw } from 'lucide-vue-next'
import { RouterLink, useRoute } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import type { TaskHistoryDetail, TaskHistoryItem, TaskHistoryStatus } from '@/types'

type LoadState = 'loading' | 'ready' | 'error'

const route = useRoute()
const task = ref<TaskHistoryDetail | null>(null)
const state = ref<LoadState>('loading')
const error = ref('')
const taskId = computed(() => {
  const value = route.params.id
  return Array.isArray(value) ? value[0] ?? '' : value ?? ''
})

function statusLabel(status: TaskHistoryStatus | string): string {
  if (status === 'running') return '进行中'
  if (status === 'succeeded') return '已完成'
  if (status === 'partially_failed') return '部分失败'
  if (status === 'failed') return '失败'
  return status
}

function typeLabel(type: string): string {
  if (type === 'sync') return '资产同步'
  if (type === 'reconcile') return '目标校验'
  if (type === 'discover') return '模型发现'
  return type
}

function itemIdentifier(item: TaskHistoryItem): string {
  return String(item.item_id ?? item.asset_id ?? item.target_id ?? item.scope ?? '--')
}

function itemMessage(item: TaskHistoryItem): string {
  return String(item.message ?? item.error_code ?? item.code ?? '--')
}

function itemFields(item: TaskHistoryItem): string {
  return JSON.stringify(item, null, 2)
}

async function loadTask(): Promise<void> {
  if (!taskId.value) return
  state.value = 'loading'
  error.value = ''
  try {
    task.value = await api.getTask(taskId.value)
    state.value = 'ready'
  } catch (reason) {
    task.value = null
    error.value = safeErrorMessage(reason)
    state.value = 'error'
  }
}

onMounted(() => void loadTask())
watch(taskId, (next, previous) => {
  if (next && next !== previous) void loadTask()
})
</script>

<template>
  <section class="page task-detail-page" aria-labelledby="task-detail-heading">
    <header class="page-header resource-header">
      <div>
        <div class="resource-title-row">
          <h1 id="task-detail-heading">任务详情</h1>
          <span v-if="task" class="platform-badge">{{ typeLabel(task.type) }}</span>
        </div>
        <p v-if="task" class="page-subtitle">{{ task.scope }}</p>
      </div>
      <RouterLink class="secondary-button" to="/tasks">
        <ArrowLeft :size="16" aria-hidden="true" />
        返回任务记录
      </RouterLink>
    </header>

    <section v-if="state === 'loading'" class="detail-state" role="status" aria-label="正在加载任务详情">
      <span class="spinner" aria-hidden="true"></span>
      <strong>正在加载任务详情</strong>
    </section>

    <section v-else-if="state === 'error'" class="detail-state task-detail-error" role="alert">
      <CircleAlert :size="24" aria-hidden="true" />
      <h2>任务详情加载失败</h2>
      <p>{{ error }}</p>
      <button class="secondary-button" type="button" aria-label="重试任务详情" @click="loadTask">
        <RotateCcw :size="16" aria-hidden="true" />
        重试
      </button>
    </section>

    <template v-else-if="task">
      <dl class="resource-strip task-summary-strip">
        <div><dt>任务 ID</dt><dd><code>{{ task.task_id }}</code></dd></div>
        <div><dt>状态</dt><dd><span class="task-status" :class="`is-${task.status}`">{{ statusLabel(task.status) }}</span></dd></div>
        <div><dt>开始时间</dt><dd>{{ task.started_at }}</dd></div>
        <div><dt>完成时间</dt><dd>{{ task.completed_at || '未完成' }}</dd></div>
      </dl>

      <section class="detail-panel task-results-panel" aria-labelledby="task-results-heading">
        <header class="panel-toolbar">
          <div>
            <h2 id="task-results-heading">执行结果</h2>
            <span>{{ task.summary.total }} 个结果</span>
          </div>
          <span class="task-summary-counts">{{ task.summary.succeeded }} 成功，{{ task.summary.failed }} 失败</span>
        </header>

        <div v-if="!task.items?.length" class="detail-empty">
          <ListChecks :size="22" aria-hidden="true" />
          <p>暂无执行结果</p>
        </div>
        <div v-else class="detail-table-wrap">
          <table class="task-results-table" aria-label="任务结果">
            <thead><tr><th scope="col">结果 ID</th><th scope="col">目标</th><th scope="col">状态</th><th scope="col">信息</th><th scope="col"><span class="sr-only">其他字段</span></th></tr></thead>
            <tbody>
              <tr v-for="item in task.items" :key="itemIdentifier(item)">
                <td data-label="结果 ID"><code>{{ itemIdentifier(item) }}</code></td>
                <td data-label="目标">{{ item.target_id || '--' }}</td>
                <td data-label="状态"><span class="task-status" :class="`is-${item.status || 'unknown'}`">{{ statusLabel(item.status || '未知') }}</span></td>
                <td data-label="信息">{{ itemMessage(item) }}</td>
                <td data-label="其他字段">
                  <details v-if="Object.keys(item).length > 1">
                    <summary>查看字段</summary>
                    <pre>{{ itemFields(item) }}</pre>
                  </details>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </section>
</template>

<style scoped>
.task-detail-error p { max-width: 52ch; margin: 0; }
.task-summary-strip { margin-bottom: 16px; }
.task-results-panel { box-shadow: none; }
.task-summary-counts { color: var(--muted); font-size: 12px; }
.task-results-table { width: 100%; min-width: 680px; }
.task-results-table code { color: var(--muted); font-size: 11px; }
.task-results-table details { position: relative; }
.task-results-table summary { cursor: pointer; color: var(--muted); font-size: 11px; }
.task-results-table pre { position: absolute; z-index: 1; right: 0; max-width: min(420px, 80vw); max-height: 240px; overflow: auto; margin: 4px 0 0; padding: 8px; border: 1px solid var(--line); background: var(--surface, #fff); color: var(--ink); font-size: 11px; white-space: pre-wrap; }
@media (max-width: 620px) {
  .task-results-table { min-width: 0; }
  .task-results-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .task-results-table tbody, .task-results-table tbody tr, .task-results-table tbody td { display: block; width: 100%; }
}
</style>
