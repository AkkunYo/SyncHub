<script setup lang="ts">
import { computed, inject, ref } from 'vue'
import { Check, Info, RefreshCw, RotateCcw, ScanSearch, TriangleAlert } from 'lucide-vue-next'
import {
  RouterLink,
  routeLocationKey,
  routerKey,
  type LocationQueryRaw,
} from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import { useConsoleStore } from '@/stores/console'
import type { DriftItem } from '@/types'

type DriftAction = 'restore' | 'accept'

interface DriftConfirmation {
  action: DriftAction
  item: DriftItem
}

const store = useConsoleStore()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)
const pendingKeys = ref(new Set<string>())
const selectedTargetId = ref('')
const confirmation = ref<DriftConfirmation | null>(null)
const reconciling = ref(false)
const notice = ref('')
const error = ref('')

function queryValue(key: string): string {
  const value = route?.query[key]
  if (value !== undefined) return Array.isArray(value) ? value[0] ?? '' : value ?? ''
  return new URL(window.location.href).searchParams.get(key) ?? ''
}

function configuredUpstream(upstreamId: string): boolean {
  return store.upstreams.some((upstream) => upstream.id === upstreamId)
}

function configuredTarget(targetId: string): boolean {
  return store.targets.some((target) => target.id === targetId)
}

function updateFilterQuery(upstreamId: string, targetId: string): void {
  if (router) {
    const query: LocationQueryRaw = {}
    if (upstreamId) query.upstream = upstreamId
    if (targetId) query.target = targetId
    void router.replace({ query })
    return
  }

  const url = new URL(window.location.href)
  url.search = ''
  if (upstreamId) url.searchParams.set('upstream', upstreamId)
  if (targetId) url.searchParams.set('target', targetId)
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
}

const requestedUpstreamId = queryValue('upstream')
const initialUpstreamId = configuredUpstream(requestedUpstreamId)
  ? requestedUpstreamId
  : configuredUpstream(store.selectedUpstreamId)
    ? store.selectedUpstreamId
    : store.upstreams[0]?.id ?? ''
const requestedTargetId = queryValue('target')
selectedTargetId.value = configuredTarget(requestedTargetId)
  ? requestedTargetId
  : configuredTarget(store.selectedTargetId)
    ? store.selectedTargetId
    : store.targets[0]?.id ?? ''
store.selectedTargetId = selectedTargetId.value
updateFilterQuery(initialUpstreamId, selectedTargetId.value)
if (initialUpstreamId !== store.selectedUpstreamId) {
  void store.loadMatrix(initialUpstreamId).catch(() => undefined)
} else {
  store.selectedUpstreamId = initialUpstreamId
}

const currentMatrix = computed(() =>
  store.matrix?.upstream_id === store.selectedUpstreamId ? store.matrix : null,
)
const visibleDriftItems = computed(() =>
  store.matrixState === 'ready' && currentMatrix.value && selectedTargetId.value
    ? store.driftItems.filter((item) => item.targetId === selectedTargetId.value)
    : [],
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

function actionMeaning(action: DriftAction): string {
  return action === 'restore'
    ? '将上游期望状态重新写入目标平台，覆盖目标端当前状态。'
    : '将目标平台当前状态采纳为后续同步基线，不会写回上游期望值。'
}

function openConfirmation(item: DriftItem, action: DriftAction): void {
  const key = driftKey(item)
  if (pendingKeys.value.has(key) || !currentMatrix.value) return
  confirmation.value = { action, item }
  notice.value = ''
  error.value = ''
}

function closeConfirmation(): void {
  confirmation.value = null
}

async function applyAction(item: DriftItem, action: DriftAction): Promise<void> {
  const key = driftKey(item)
  if (pendingKeys.value.has(key)) return
  pendingKeys.value.add(key)
  notice.value = ''
  error.value = ''
  try {
    const input = {
      upstream_asset_id: item.assetId,
      channel_id: item.channelId,
    }
    if (action === 'restore') await api.restoreDrift(item.targetId, input)
    else await api.acceptDrift(item.targetId, input)

    const currentUpstreamId = store.selectedUpstreamId
    if (currentUpstreamId) await store.loadMatrix(currentUpstreamId)
    notice.value = action === 'restore' ? '期望状态已恢复' : '目标状态已采纳'
  } catch (reason) {
    error.value = safeErrorMessage(reason)
  } finally {
    pendingKeys.value.delete(key)
  }
}

async function confirmAction(): Promise<void> {
  const pending = confirmation.value
  if (!pending) return
  confirmation.value = null
  await applyAction(pending.item, pending.action)
}

async function onUpstreamChange(event: Event): Promise<void> {
  const upstreamId = (event.target as HTMLSelectElement).value
  if (!configuredUpstream(upstreamId) || upstreamId === store.selectedUpstreamId) return
  notice.value = ''
  error.value = ''
  updateFilterQuery(upstreamId, selectedTargetId.value)
  try {
    await store.loadMatrix(upstreamId)
  } catch {
    // The store exposes the sanitized error in the matrix state.
  }
}

function onTargetChange(event: Event): void {
  const targetId = (event.target as HTMLSelectElement).value
  if (!configuredTarget(targetId)) return
  selectedTargetId.value = targetId
  store.selectedTargetId = targetId
  notice.value = ''
  error.value = ''
  updateFilterQuery(store.selectedUpstreamId, targetId)
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

    <div class="drift-filter-bar" role="group" aria-label="漂移筛选">
      <label class="drift-filter-control">
        <span>上游</span>
        <select
          aria-label="选择上游"
          :value="store.selectedUpstreamId"
          :disabled="store.upstreams.length === 0 || store.matrixState === 'loading'"
          @change="onUpstreamChange"
        >
          <option v-if="store.upstreams.length === 0" value="">暂无上游</option>
          <option v-for="upstream in store.upstreams" :key="upstream.id" :value="upstream.id">
            {{ upstream.name }}
          </option>
        </select>
      </label>
      <label class="drift-filter-control">
        <span>目标</span>
        <select
          aria-label="选择目标"
          :value="selectedTargetId"
          :disabled="store.targets.length === 0"
          @change="onTargetChange"
        >
          <option v-if="store.targets.length === 0" value="">暂无目标</option>
          <option v-for="target in store.targets" :key="target.id" :value="target.id">
            {{ target.name }}
          </option>
        </select>
      </label>
    </div>

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
            恢复会覆盖目标当前值；采纳会更新后续同步基线。
          </p>
          <div class="drift-row-buttons">
            <button
              class="secondary-button"
              type="button"
              :disabled="pendingKeys.has(driftKey(item))"
              :aria-label="`恢复 ${item.assetName} 在 ${item.targetName} 的期望状态`"
              @click="openConfirmation(item, 'restore')"
            >
              <RotateCcw :size="16" aria-hidden="true" />
              恢复期望状态
            </button>
            <button
              class="secondary-button"
              type="button"
              :disabled="pendingKeys.has(driftKey(item))"
              :aria-label="`采纳 ${item.assetName} 在 ${item.targetName} 的目标状态`"
              @click="openConfirmation(item, 'accept')"
            >
              <Check :size="16" aria-hidden="true" />
              采纳目标状态
            </button>
          </div>
        </footer>
      </article>
    </div>

    <ModalDialog
      v-if="confirmation"
      :title="confirmation.action === 'restore' ? '确认恢复期望状态' : '确认采纳目标状态'"
      :close-label="confirmation.action === 'restore' ? '取消恢复期望状态' : '取消采纳目标状态'"
      @close="closeConfirmation"
    >
      <div class="drift-confirmation">
        <p>执行前请核对本次漂移处理范围。</p>
        <dl class="drift-confirmation-details">
          <div>
            <dt>资产</dt>
            <dd>{{ confirmation.item.assetName }}</dd>
          </div>
          <div>
            <dt>目标</dt>
            <dd>{{ confirmation.item.targetName }}</dd>
          </div>
          <div>
            <dt>操作含义</dt>
            <dd>{{ actionMeaning(confirmation.action) }}</dd>
          </div>
          <div>
            <dt>字段差异</dt>
            <dd>{{ confirmation.item.differences.length }} 项字段差异</dd>
          </div>
        </dl>
        <footer class="drift-confirmation-actions">
          <button class="secondary-button" type="button" @click="closeConfirmation">取消</button>
          <button class="primary-button" type="button" @click="confirmAction">
            {{ confirmation.action === 'restore' ? '确认恢复期望状态' : '确认采纳目标状态' }}
          </button>
        </footer>
      </div>
    </ModalDialog>
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

.drift-filter-bar {
  display: flex;
  align-items: end;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
}

.drift-filter-control {
  display: grid;
  min-width: min(240px, 100%);
  gap: 5px;
  color: var(--muted);
  font-size: 11px;
}

.drift-filter-control select {
  min-height: 40px;
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

.drift-row-buttons {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

.drift-confirmation {
  display: grid;
  gap: 14px;
}

.drift-confirmation > p {
  margin: 0;
  color: var(--ink);
  font-size: 13px;
}

.drift-confirmation-details {
  display: grid;
  gap: 8px;
  margin: 0;
}

.drift-confirmation-details > div {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 8px;
  align-items: baseline;
}

.drift-confirmation-details dt {
  color: var(--muted);
  font-size: 11px;
}

.drift-confirmation-details dd {
  min-width: 0;
  margin: 0;
  color: var(--ink);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.drift-confirmation-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
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

  .drift-filter-bar {
    align-items: stretch;
    flex-direction: column;
  }

  .drift-filter-control {
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

  .drift-row-buttons {
    align-items: stretch;
    flex-direction: column;
  }

  .drift-row-action .secondary-button,
  .drift-row-buttons {
    width: 100%;
  }

  .drift-confirmation-details > div {
    grid-template-columns: 1fr;
    gap: 3px;
  }
}
</style>
