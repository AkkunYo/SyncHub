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
          <h3>暂无执行结果</h3>
          <p>任务未返回逐项结果，仍可通过上方摘要确认整体状态。</p>
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
.resource-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.platform-badge {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  border-radius: 999px;
  padding: 3px 7px;
  color: #3f3f46;
  background: #f4f4f5;
  font-size: 11px;
  font-weight: 650;
}

.page-subtitle {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.detail-state {
  display: grid;
  min-height: 240px;
  place-content: center;
  justify-items: center;
  gap: 9px;
  padding: 24px;
  border: 1px solid var(--line);
  border-radius: 8px;
  color: var(--muted);
  background: var(--surface);
  text-align: center;
}

.detail-state h2 {
  margin: 0;
  color: var(--ink);
  font-size: 15px;
}

.task-detail-error p {
  max-width: 52ch;
  margin: 0;
  overflow-wrap: anywhere;
}

.task-summary-strip {
  display: grid;
  margin: 0 0 16px;
  padding: 0;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.task-summary-strip > div {
  display: grid;
  min-width: 0;
  align-content: center;
  gap: 3px;
  padding: 10px 12px;
  border-right: 1px solid var(--line);
}

.task-summary-strip > div:last-child {
  border-right: 0;
}

.task-summary-strip dt {
  color: var(--muted);
  font-size: 10px;
  font-weight: 700;
}

.task-summary-strip dd {
  min-width: 0;
  margin: 0;
  color: var(--ink);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.task-summary-strip code {
  white-space: normal;
  overflow-wrap: anywhere;
}

.task-results-panel {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: none;
}

.panel-toolbar {
  display: flex;
  min-height: 54px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--line);
}

.panel-toolbar > div {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
}

.panel-toolbar h2 {
  margin: 0;
  font-size: 14px;
}

.panel-toolbar span,
.task-summary-counts {
  color: var(--muted);
  font-size: 12px;
}

.detail-table-wrap {
  width: 100%;
  overflow-x: auto;
}

.task-results-table {
  width: 100%;
  min-width: 680px;
  border-collapse: collapse;
  table-layout: fixed;
}

.task-results-table th,
.task-results-table td {
  min-width: 0;
  padding: 9px 12px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 12px;
  text-align: left;
  vertical-align: top;
  overflow-wrap: anywhere;
}

.task-results-table th {
  height: 36px;
  color: #52525b;
  background: var(--surface-subtle);
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}

.task-results-table th:nth-child(1) { width: 22%; }
.task-results-table th:nth-child(2) { width: 16%; }
.task-results-table th:nth-child(3) { width: 14%; }
.task-results-table th:nth-child(4) { width: 34%; }
.task-results-table th:nth-child(5) { width: 14%; }
.task-results-table tbody tr:last-child td { border-bottom: 0; }

.task-results-table code {
  color: var(--muted);
  font-size: 11px;
}

.task-status {
  display: inline-flex;
  min-height: 22px;
  width: fit-content;
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

.task-results-table details {
  position: relative;
  min-width: 0;
}

.task-results-table summary {
  cursor: pointer;
  color: var(--muted);
  font-size: 11px;
}

.task-results-table pre {
  position: absolute;
  z-index: 1;
  right: 0;
  max-width: min(420px, 80vw);
  max-height: 240px;
  overflow: auto;
  margin: 4px 0 0;
  padding: 8px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface, #fff);
  color: var(--ink);
  font-size: 11px;
  white-space: pre-wrap;
}

.detail-empty {
  display: grid;
  min-height: 220px;
  place-content: center;
  justify-items: center;
  gap: 7px;
  padding: 24px;
  color: var(--muted);
  text-align: center;
}

.detail-empty h3 {
  margin: 0;
  color: var(--ink);
  font-size: 15px;
}

.detail-empty p {
  max-width: 48ch;
  margin: 0;
  font-size: 12px;
}

@media (max-width: 620px) {
  .resource-header {
    align-items: stretch;
  }

  .resource-header .secondary-button {
    width: 100%;
    justify-content: center;
  }

  .task-summary-strip {
    grid-template-columns: minmax(0, 1fr);
  }

  .task-summary-strip > div {
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }

  .task-summary-strip > div:last-child {
    border-bottom: 0;
  }

  .panel-toolbar {
    min-height: 0;
    align-items: flex-start;
    flex-direction: column;
  }

  .detail-table-wrap {
    overflow: visible;
  }

  .task-results-table {
    display: block;
    width: 100%;
    min-width: 0;
    table-layout: auto;
  }

  .task-results-table thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .task-results-table tbody {
    display: block;
    width: 100%;
  }

  .task-results-table tbody tr {
    display: grid;
    width: 100%;
    padding: 0 12px;
    border-bottom: 1px solid var(--line);
    grid-template-columns: minmax(0, 1fr);
  }

  .task-results-table tbody tr:last-child {
    border-bottom: 0;
  }

  .task-results-table tbody td {
    display: grid;
    width: 100%;
    min-width: 0;
    padding: 9px 0;
    border-bottom: 1px solid var(--line);
    grid-template-columns: 76px minmax(0, 1fr);
    gap: 8px;
    overflow-wrap: anywhere;
  }

  .task-results-table tbody td:last-child {
    border-bottom: 0;
  }

  .task-results-table tbody td::before {
    color: var(--muted);
    content: attr(data-label);
    font-size: 10px;
    font-weight: 700;
  }

  .task-results-table code {
    min-width: 0;
    white-space: normal;
    overflow-wrap: anywhere;
    word-break: break-word;
  }

  .task-results-table details {
    width: 100%;
    min-width: 0;
  }

  .task-results-table pre {
    position: static;
    width: 100%;
    max-width: 100%;
    max-height: 220px;
    box-sizing: border-box;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    word-break: break-word;
  }
}
</style>
