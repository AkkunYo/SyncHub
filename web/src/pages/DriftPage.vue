<script setup lang="ts">
import { computed, ref } from 'vue'
import { Check, RefreshCw, RotateCcw, ScanSearch, TriangleAlert } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { DriftItem } from '@/types'

const store = useConsoleStore()
const acceptingKeys = ref(new Set<string>())
const reconciling = ref(false)
const notice = ref('')
const error = ref('')

const currentMatrix = computed(() =>
  store.matrix?.upstream_id === store.selectedUpstreamId ? store.matrix : null,
)
const visibleDriftItems = computed(() =>
  store.matrixState === 'ready' && currentMatrix.value ? store.driftItems : [],
)
const driftSummaryReady = computed(() => store.matrixState === 'ready' && currentMatrix.value !== null)

const fieldLabels: Record<string, string> = {
  models: '模型',
  group: '分组',
  priority: '优先级',
  weight: '权重',
}

function displayValue(value: unknown): string {
  if (Array.isArray(value)) return value.join(', ')
  if (value === null || value === undefined || value === '') return '空'
  return String(value)
}

function driftKey(item: DriftItem): string {
  return `${item.assetId}\u0000${item.targetId}`
}

async function accept(item: DriftItem): Promise<void> {
  const key = driftKey(item)
  if (acceptingKeys.value.has(key) || !currentMatrix.value) return
  const matrix = currentMatrix.value
  const upstreamId = store.selectedUpstreamId
  acceptingKeys.value.add(key)
  notice.value = ''
  error.value = ''
  try {
    await api.acceptDrift(item.targetId, {
      upstream_asset_id: item.assetId,
      channel_id: item.channelId,
    })
    if (store.selectedUpstreamId === upstreamId && currentMatrix.value === matrix) {
      store.markDriftAccepted(item.assetId, item.targetId)
      notice.value = '漂移已接受'
    }
  } catch (reason) {
    if (store.selectedUpstreamId === upstreamId && currentMatrix.value === matrix) {
      error.value = safeErrorMessage(reason)
    }
  } finally {
    acceptingKeys.value.delete(key)
  }
}

async function retryMatrix(): Promise<void> {
  try {
    await store.loadMatrix()
  } catch {
    // The store exposes the sanitized error in the matrix state.
  }
}

async function reconcileAll(): Promise<void> {
  if (store.targets.length === 0) return
  reconciling.value = true
  notice.value = ''
  error.value = ''
  const outcomes = await Promise.allSettled(store.targets.map((target) => api.reconcile(target.id)))
  if (outcomes.some((outcome) => outcome.status === 'rejected')) {
    error.value = '部分目标校验失败，请重试'
  } else {
    notice.value = '目标校验完成'
  }
  try {
    await store.loadMatrix()
  } catch {
    // The matrix error state below provides the retry action.
  } finally {
    reconciling.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="drift-heading">
    <header class="page-header drift-page-header">
      <div>
        <h1 id="drift-heading" aria-label="配置漂移">
          <span aria-hidden="true">漂移修复</span>
          <span class="sr-only">配置漂移</span>
        </h1>
        <p class="page-context">{{ store.selectedUpstreamId || '未选择上游' }}</p>
      </div>
    </header>

    <section class="drift-command-bar" role="toolbar" aria-label="漂移扫描">
      <div class="pending-summary" aria-live="polite">
        <TriangleAlert v-if="visibleDriftItems.length" :size="17" aria-hidden="true" />
        <Check v-else :size="17" aria-hidden="true" />
        <strong>{{ driftSummaryReady ? `${visibleDriftItems.length} 项待处理` : '--' }}</strong>
        <span>{{ store.targets.length }} 个目标</span>
      </div>
      <button
        class="secondary-button"
        type="button"
        aria-label="校验全部目标"
        :disabled="reconciling || store.targets.length === 0 || !store.selectedUpstreamId"
        @click="reconcileAll"
      >
        <RefreshCw v-if="reconciling" :size="16" aria-hidden="true" />
        <ScanSearch v-else :size="16" aria-hidden="true" />
        {{ reconciling ? '扫描中' : '扫描漂移' }}
      </button>
    </section>

    <p v-if="notice" class="notice notice-success" role="status">{{ notice }}</p>
    <p v-if="error" class="notice notice-error" role="alert">{{ error }}</p>

    <div
      v-if="store.matrixState === 'loading'"
      class="state-panel"
      role="status"
      aria-label="正在读取配置漂移"
    >
      <span class="spinner" aria-hidden="true"></span>
      <p>正在读取当前上游矩阵</p>
    </div>

    <div v-else-if="store.matrixState === 'error'" class="state-panel state-error" role="alert">
      <p>{{ store.matrixError }}</p>
      <button class="secondary-button" type="button" @click="retryMatrix">
        <RotateCcw :size="16" aria-hidden="true" />
        重试
      </button>
    </div>

    <div v-else-if="!store.selectedUpstreamId" class="state-panel">
      <h2>尚未配置上游实例</h2>
      <RouterLink class="primary-button" to="/upstreams">管理上游连接</RouterLink>
    </div>

    <div v-else-if="store.targets.length === 0" class="state-panel">
      <h2>尚未配置目标实例</h2>
      <RouterLink class="primary-button" to="/targets">管理目标实例</RouterLink>
    </div>

    <div
      v-else-if="!currentMatrix"
      class="state-panel state-error"
      role="alert"
    >
      <p>当前上游矩阵尚未就绪</p>
      <button class="secondary-button" type="button" @click="retryMatrix">
        <RotateCcw :size="16" aria-hidden="true" />
        重试
      </button>
    </div>

    <div v-else-if="visibleDriftItems.length === 0" class="state-panel">
      <Check :size="24" aria-hidden="true" />
      <h2>当前没有配置漂移</h2>
    </div>

    <div v-else class="drift-list">
      <article
        v-for="item in visibleDriftItems"
        :key="driftKey(item)"
        class="drift-row"
        :aria-label="`${item.assetName} / ${item.targetName} 配置漂移`"
      >
        <header>
          <div>
            <strong>{{ item.assetName }}</strong>
            <small>{{ item.targetName }} / #{{ item.channelId }}</small>
          </div>
          <button
            class="secondary-button"
            type="button"
            :disabled="acceptingKeys.has(driftKey(item))"
            :aria-label="`接受 ${item.assetName} 在 ${item.targetName} 的目标端状态`"
            @click="accept(item)"
          >
            <Check :size="16" aria-hidden="true" />
            {{ acceptingKeys.has(driftKey(item)) ? '采纳中' : '采纳目标状态' }}
          </button>
        </header>
        <dl class="difference-list">
          <div v-for="difference in item.differences" :key="difference.field">
            <dt>{{ fieldLabels[difference.field] ?? difference.field }}</dt>
            <dd>{{ displayValue(difference.expected) }} -> {{ displayValue(difference.actual) }}</dd>
          </div>
        </dl>
      </article>
    </div>
  </section>
</template>

<style scoped>
.drift-page-header {
  min-height: 38px;
}

.page-context {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
}

.drift-command-bar {
  display: flex;
  min-height: 54px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
  padding: 8px 10px;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
}

.pending-summary {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  color: var(--amber);
}

.pending-summary strong {
  color: var(--ink);
  font-size: 13px;
}

.pending-summary span {
  color: var(--muted);
  font-size: 11px;
}

.drift-list {
  gap: 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
}

.drift-row {
  padding: 14px 12px;
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 0;
  box-shadow: none;
}

.drift-row:last-child {
  border-bottom: 0;
}

.difference-list {
  border-radius: 4px;
}

@media (max-width: 620px) {
  .drift-command-bar {
    align-items: stretch;
    flex-direction: column;
  }

  .drift-command-bar .secondary-button {
    width: 100%;
  }

  .drift-row {
    display: grid;
    padding: 12px 10px;
  }

  .pending-summary {
    min-height: 32px;
  }
}
</style>
