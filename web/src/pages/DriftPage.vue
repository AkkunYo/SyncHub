<script setup lang="ts">
import { computed, ref } from 'vue'
import { Check, Info, RefreshCw, RotateCcw, ScanSearch, TriangleAlert } from 'lucide-vue-next'
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
const currentUpstreamName = computed(() => {
  if (!store.selectedUpstreamId) return '未选择上游'
  return store.config?.upstreams.find((upstream) => upstream.id === store.selectedUpstreamId)?.name
    ?? store.selectedUpstreamId
})

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
        <p class="page-context">当前上游：{{ currentUpstreamName }}</p>
      </div>
      <div class="drift-header-actions" role="toolbar" aria-label="漂移扫描">
        <div class="pending-summary" aria-live="polite">
          <TriangleAlert v-if="visibleDriftItems.length" :size="17" aria-hidden="true" />
          <Check v-else :size="17" aria-hidden="true" />
          <strong>{{ driftSummaryReady ? `${visibleDriftItems.length} 项待处理` : '--' }}</strong>
          <span>{{ store.targets.length }} 个目标</span>
        </div>
        <button
          class="secondary-button drift-scan-button"
          type="button"
          aria-label="校验全部目标"
          :disabled="reconciling || store.targets.length === 0 || !store.selectedUpstreamId"
          @click="reconcileAll"
        >
          <RefreshCw v-if="reconciling" :size="16" aria-hidden="true" />
          <ScanSearch v-else :size="16" aria-hidden="true" />
          {{ reconciling ? '扫描中' : '扫描漂移' }}
        </button>
      </div>
    </header>

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
      <span class="state-icon state-icon-success" aria-hidden="true">
        <Check :size="22" />
      </span>
      <h2>当前没有配置漂移</h2>
      <p>最近一次扫描未发现差异，当前配置与目标状态一致。</p>
      <small>已检查 {{ store.targets.length }} 个目标实例</small>
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
        </header>
        <dl class="difference-list">
          <div v-for="difference in item.differences" :key="difference.field" class="difference-row">
            <dt>{{ fieldLabels[difference.field] ?? difference.field }}</dt>
            <dd class="difference-values">
              <span>
                <small>期望值</small>
                <strong>{{ displayValue(difference.expected) }}</strong>
                <span class="sr-only">
                  {{ displayValue(difference.expected) }} -> {{ displayValue(difference.actual) }}
                </span>
              </span>
              <span>
                <small>当前值</small>
                <strong>{{ displayValue(difference.actual) }}</strong>
              </span>
            </dd>
          </div>
        </dl>
        <footer class="drift-row-action">
          <p>
            <Info :size="15" aria-hidden="true" />
            采纳后将以目标平台当前值作为后续同步基线。
          </p>
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
        </footer>
      </article>
    </div>
  </section>
</template>

<style scoped>
.drift-page-header {
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.page-context {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
}

.drift-header-actions {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
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

.drift-scan-button {
  min-height: 44px;
}

.state-panel p {
  max-width: 48ch;
  margin: 0;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}

.state-panel small {
  color: var(--muted);
  font-size: 11px;
}

.state-icon {
  display: inline-grid;
  width: 42px;
  height: 42px;
  place-items: center;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface-subtle, #f8fafc);
}

.state-icon-success {
  color: var(--green, #15803d);
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
  display: grid;
  margin: 12px 0 0;
  border: 1px solid var(--line);
  border-radius: 6px;
  overflow: hidden;
}

.difference-row {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(100px, 0.55fr) minmax(0, 2fr);
  border-bottom: 1px solid var(--line);
}

.difference-row:last-child {
  border-bottom: 0;
}

.difference-row > dt {
  display: flex;
  min-width: 0;
  align-items: center;
  margin: 0;
  padding: 11px 12px;
  background: var(--surface-subtle, #f8fafc);
  color: var(--ink);
  font-size: 12px;
  font-weight: 650;
}

.difference-values {
  display: grid;
  min-width: 0;
  margin: 0;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.difference-values > span {
  display: grid;
  min-width: 0;
  align-content: center;
  gap: 3px;
  padding: 9px 12px;
  border-left: 1px solid var(--line);
}

.difference-values small {
  color: var(--muted);
  font-size: 10px;
}

.difference-values strong {
  min-width: 0;
  color: var(--ink);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  font-weight: 500;
  overflow-wrap: anywhere;
}

.drift-row-action {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-top: 10px;
}

.drift-row-action p {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
}

.drift-row-action p svg {
  flex: 0 0 auto;
}

.drift-row-action .secondary-button {
  min-height: 44px;
  flex: 0 0 auto;
}

@media (max-width: 620px) {
  .drift-page-header {
    align-items: stretch;
    flex-direction: column;
    padding-bottom: 12px;
  }

  .drift-header-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .drift-header-actions .secondary-button {
    width: 100%;
  }

  .drift-row {
    display: grid;
    padding: 12px 10px;
  }

  .pending-summary {
    min-height: 36px;
  }

  .difference-row {
    grid-template-columns: 1fr;
  }

  .difference-row > dt {
    border-bottom: 1px solid var(--line);
  }

  .difference-values > span:first-child {
    border-left: 0;
  }

  .drift-row-action {
    align-items: stretch;
    flex-direction: column;
  }

  .drift-row-action .secondary-button {
    width: 100%;
  }
}
</style>
