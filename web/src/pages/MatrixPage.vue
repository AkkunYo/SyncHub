<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  CheckCircle2,
  Circle,
  RefreshCw,
  RotateCcw,
  Search,
  Send,
  SlidersHorizontal,
  X,
} from 'lucide-vue-next'

import { api } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import TableSkeleton from '@/components/TableSkeleton.vue'
import { useConsoleStore } from '@/stores/console'
import type { AssetSyncResult, MatrixRow, MatrixStatus, SyncTargetResult } from '@/types'

const store = useConsoleStore()
const selectedAssets = ref<string[]>([])
const syncOpen = ref(false)
const models = ref('')
const group = ref('default')
const priority = ref(0)
const weight = ref(100)
const selectedTargets = ref<string[]>([])
const submitting = ref(false)
const validationError = ref('')
const results = ref<AssetSyncResult[]>([])
const assetQuery = ref('')
const statusFilter = ref<MatrixStatus | 'all'>('all')

interface SubmittedRow {
  row: MatrixRow
  targetIds: string[]
}

const activeMatrix = computed(() =>
  store.matrix?.upstream_id === store.selectedUpstreamId ? store.matrix : null,
)
const rows = computed(() => activeMatrix.value?.rows ?? [])
const matrixTargets = computed(() => activeMatrix.value?.targets ?? store.targets)
const filteredRows = computed(() => {
  const query = assetQuery.value.trim().toLocaleLowerCase()
  return rows.value.filter((row) => {
    const matchesQuery = !query || [
      row.asset.name,
      row.asset.id,
      row.asset.provider,
      row.asset.kind,
      row.asset.raw_type,
      row.asset.source_type,
      ...row.asset.models,
    ].some((value) => value.toLocaleLowerCase().includes(query))
    const matchesStatus = statusFilter.value === 'all'
      || row.cells.some((cell) => cell.status === statusFilter.value)
    return matchesQuery && matchesStatus
  })
})
const selectedRows = computed(() => rows.value.filter((row) => selectedAssets.value.includes(row.asset.id)))
const selectableRows = computed(() => filteredRows.value.filter(isSelectable))
const isRefreshing = computed(() => store.matrixState === 'loading' && activeMatrix.value !== null)
const isInitialLoading = computed(() => store.matrixState === 'loading' && activeMatrix.value === null)
const isInitialError = computed(() => store.matrixState === 'error' && activeMatrix.value === null)
const hasFilters = computed(() => Boolean(assetQuery.value.trim()) || statusFilter.value !== 'all')
const syncableTargetIds = computed(
  () =>
    new Set(
      selectedRows.value.flatMap((row) =>
        row.cells.filter((cell) => cell.status === 'unsynced').map((cell) => cell.target_id),
      ),
    ),
)
const syncedCount = computed(() => rows.value.flatMap((row) => row.cells).filter((cell) => cell.status === 'synced').length)
const driftCount = computed(() => rows.value.flatMap((row) => row.cells).filter((cell) => cell.status === 'drifted').length)
const attentionCount = computed(
  () => rows.value.flatMap((row) => row.cells).filter((cell) => ['incompatible', 'needs_reconcile'].includes(cell.status)).length,
)
const allSelected = computed(
  () => selectableRows.value.length > 0 && selectableRows.value.every((row) => selectedAssets.value.includes(row.asset.id)),
)
const partiallySelected = computed(() => {
  const selectedCount = selectableRows.value.filter((row) => selectedAssets.value.includes(row.asset.id)).length
  return selectedCount > 0 && selectedCount < selectableRows.value.length
})
const resultSummary = computed(() => {
  const targets = results.value.flatMap((result) => result.targets)
  const successes = targets.filter((target) => target.status === 'synced').length
  if (successes === targets.length && targets.length > 0) return '同步完成'
  if (successes > 0) return '部分完成'
  return '同步失败'
})
const retryableFailureCount = computed(() =>
  results.value.flatMap((result) => result.targets).filter((target) => target.status === 'failed' && target.retryable).length,
)
const setupSteps = computed(() => [
  { label: '配置目标实例', complete: store.targets.length > 0 },
  { label: '配置上游连接', complete: store.upstreams.length > 0 },
  { label: '刷新来源资产', complete: rows.value.length > 0 },
  { label: '选择资产并同步', complete: syncedCount.value > 0 },
])
const completedSetupSteps = computed(() => setupSteps.value.filter((step) => step.complete).length)

function isSelectable(row: MatrixRow): boolean {
  if (!row.asset.enabled || !row.asset.secret_readable) return false
  return row.cells.some((cell) => cell.status === 'unsynced')
}

function assetAvailability(row: MatrixRow): { label: string; description: string } | null {
  if (!row.asset.enabled) return { label: '已禁用', description: '已禁用' }
  if (!row.asset.secret_readable) return { label: '仅发现', description: '仅发现：秘密不可读取' }
  return null
}

function assetReasonId(row: MatrixRow): string {
  return `asset-reason-${row.asset.id.replace(/[^A-Za-z0-9_-]/g, '-')}`
}

async function onUpstreamChange(event: Event): Promise<void> {
  selectedAssets.value = []
  try {
    await store.loadMatrix((event.target as HTMLSelectElement).value)
  } catch {
    // The store exposes the already-sanitized error state.
  }
}

function toggleAll(): void {
  const visibleIds = new Set(selectableRows.value.map((row) => row.asset.id))
  selectedAssets.value = allSelected.value
    ? selectedAssets.value.filter((assetId) => !visibleIds.has(assetId))
    : [...new Set([...selectedAssets.value, ...visibleIds])]
}

function clearFilters(): void {
  assetQuery.value = ''
  statusFilter.value = 'all'
}

function clearSelection(): void {
  selectedAssets.value = []
}

function openSync(): void {
  const modelSet = new Set(selectedRows.value.flatMap((row) => row.asset.models))
  models.value = [...modelSet].join(', ')
  selectedTargets.value = matrixTargets.value
    .filter((target) => syncableTargetIds.value.has(target.id))
    .map((target) => target.id)
  group.value = 'default'
  priority.value = 0
  weight.value = 100
  validationError.value = ''
  results.value = []
  syncOpen.value = true
}

function closeSync(): void {
  if (submitting.value) return
  syncOpen.value = false
}

function parseModels(): string[] {
  return [...new Set(models.value.split(',').map((model) => model.trim()).filter(Boolean))]
}

function failedTarget(targetId: string, code = 'request_failed'): SyncTargetResult {
  return {
    target_id: targetId,
    status: 'failed',
    code,
    retryable: true,
  }
}

function failedTargets(targetIds: readonly string[], code = 'request_failed'): SyncTargetResult[] {
  return targetIds.map((targetId) => failedTarget(targetId, code))
}

async function submitSync(): Promise<void> {
  const normalizedModels = parseModels()
  if (normalizedModels.length === 0) {
    validationError.value = '至少填写一个模型'
    return
  }
  if (selectedTargets.value.length === 0) {
    validationError.value = '至少选择一个目标实例'
    return
  }
  const submittedTargetIds = [...selectedTargets.value]
  const submittedRows: SubmittedRow[] = selectedRows.value
    .map((row) => ({
      row,
      targetIds: submittedTargetIds.filter((targetId) =>
        row.cells.some((cell) => cell.target_id === targetId && cell.status === 'unsynced'),
      ),
    }))
    .filter(({ targetIds }) => targetIds.length > 0)
  if (submittedRows.length === 0) {
    validationError.value = '所选资产没有可同步的目标'
    return
  }
  const submittedUpstreamId = store.selectedUpstreamId
  validationError.value = ''
  results.value = []
  await runSync(submittedRows, false, submittedUpstreamId)
}

function mergeSyncResults(current: AssetSyncResult[], completed: AssetSyncResult[]): AssetSyncResult[] {
  const merged = new Map(current.map((result) => [result.assetId, { ...result, targets: [...result.targets] }]))
  for (const result of completed) {
    const existing = merged.get(result.assetId)
    if (!existing) {
      merged.set(result.assetId, result)
      continue
    }
    const targets = new Map(existing.targets.map((target) => [target.target_id, target]))
    for (const target of result.targets) targets.set(target.target_id, target)
    existing.targets = [...targets.values()]
  }
  return [...merged.values()]
}

async function runSync(
  submittedRows: SubmittedRow[],
  merge: boolean,
  submittedUpstreamId: string,
): Promise<void> {
  if (submittedRows.length === 0) {
    validationError.value = merge ? '没有可重试的失败目标' : '所选资产没有可同步的目标'
    return
  }
  submitting.value = true
  try {
    const normalizedModels = parseModels()
    let sequence = 0
    const units = submittedRows.flatMap(({ row, targetIds }) =>
      targetIds.map((targetId) => ({
        unit_id: `sync-${++sequence}`,
        asset_id: row.asset.id,
        target_id: targetId,
        upstream_group: row.asset.raw_type === 'newapi-token'
          ? row.asset.metadata.upstream_group?.trim()
          : undefined,
        settings: {
          models: normalizedModels,
          target_group: group.value.trim() || 'default',
          priority: priority.value,
          weight: weight.value,
        },
      })),
    )
    let completed: AssetSyncResult[]
    try {
      const response = await api.sync({
        upstream_id: submittedUpstreamId,
        units,
      })
      const byTuple = new Map(response.units.map((unit) => [`${unit.asset_id}\u0000${unit.target_id}`, unit]))
      completed = submittedRows.map(({ row, targetIds }) => ({
        assetId: row.asset.id,
        assetName: row.asset.name,
        targets: targetIds.map((targetId) =>
          byTuple.get(`${row.asset.id}\u0000${targetId}`) ?? failedTarget(targetId, 'upstream_failure'),
        ),
      }))
    } catch (error) {
      completed = submittedRows.map(({ row, targetIds }) => ({
        assetId: row.asset.id,
        assetName: row.asset.name,
        targets: failedTargets(targetIds, error instanceof Error ? 'request_failed' : 'internal_error'),
      }))
    }
    results.value = merge ? mergeSyncResults(results.value, completed) : completed
    if (store.applySyncResults(completed, submittedUpstreamId)) {
      const selectableIds = new Set(rows.value.filter(isSelectable).map((row) => row.asset.id))
      selectedAssets.value = selectedAssets.value.filter((assetId) => selectableIds.has(assetId))
    }
  } finally {
    submitting.value = false
  }
}

async function retryFailedTargets(): Promise<void> {
  const submittedRows: SubmittedRow[] = results.value.flatMap((result) => {
    const row = rows.value.find((candidate) => candidate.asset.id === result.assetId)
    const targetIds = result.targets
      .filter((target) => target.status === 'failed' && target.retryable)
      .map((target) => target.target_id)
    return row && targetIds.length > 0 ? [{ row, targetIds }] : []
  })
  if (submittedRows.length === 0) {
    validationError.value = '没有可重试的失败目标'
    return
  }
  const submittedUpstreamId = store.selectedUpstreamId
  validationError.value = ''
  await runSync(submittedRows, true, submittedUpstreamId)
}

function targetLabel(targetId: string): string {
  return matrixTargets.value.find((target) => target.id === targetId)?.name ?? targetId
}

function resultLabel(result: SyncTargetResult): string {
  if (result.status === 'synced') return '已同步'
  if (result.status === 'failed') return '同步失败'
  if (result.status === 'incompatible') return '目标不兼容'
  if (result.status === 'needs_reconcile') return '需要校验'
  if (result.status === 'drifted') return '有漂移'
  return '未同步'
}

async function retryMatrix(): Promise<void> {
  try {
    await store.loadMatrix()
  } catch {
    // The store exposes the already-sanitized error state.
  }
}

function matrixStatusLabel(status: string): string {
  return status === 'loading' ? '正在读取资产矩阵' : '资产矩阵状态'
}

</script>

<template>
  <section class="page sync-page" aria-labelledby="matrix-heading">
    <header class="page-header workspace-page-header">
      <div>
        <h1 id="matrix-heading" aria-label="资产矩阵">
          <span aria-hidden="true">同步工作台</span>
          <span class="sr-only">资产矩阵</span>
        </h1>
        <p class="page-context">{{ rows.length }} 个来源资产 / {{ matrixTargets.length }} 个目标实例</p>
      </div>
    </header>

    <div
      class="workspace-panel sync-workspace"
      :class="{ 'data-refreshing': isRefreshing }"
      :aria-busy="store.matrixState === 'loading'"
    >
      <div class="workspace-toolbar">
        <div class="page-actions matrix-page-actions">
          <div class="toolbar-filters">
            <label class="compact-field">
              <span>上游实例</span>
              <select :value="store.selectedUpstreamId" @change="onUpstreamChange">
                <option v-for="source in store.upstreams" :key="source.id" :value="source.id">
                  {{ source.name }}
                </option>
              </select>
            </label>

            <label class="search-field">
              <Search :size="16" aria-hidden="true" />
              <span class="sr-only">搜索资产</span>
              <input
                v-model="assetQuery"
                type="search"
                aria-label="搜索资产"
                placeholder="搜索资产"
                autocomplete="off"
              />
            </label>

            <label class="filter-field">
              <span class="sr-only">同步状态</span>
              <select v-model="statusFilter" aria-label="同步状态">
                <option value="all">全部状态</option>
                <option value="unsynced">未同步</option>
                <option value="synced">已同步</option>
                <option value="drifted">有漂移</option>
                <option value="incompatible">不兼容</option>
                <option value="needs_reconcile">待校验</option>
              </select>
            </label>
          </div>

          <button
            class="secondary-button"
            type="button"
            :disabled="!store.selectedUpstreamId || store.matrixState === 'loading'"
            @click="store.refreshAssets"
          >
            <RefreshCw :size="16" aria-hidden="true" />
            刷新资产
          </button>
        </div>
      </div>

      <div v-if="activeMatrix" class="metric-strip" aria-label="矩阵摘要">
        <div><strong>{{ rows.length }}</strong><span>资产</span></div>
        <div><strong>{{ syncedCount }}</strong><span>已同步</span></div>
        <div :class="{ 'metric-alert': driftCount > 0 }"><strong>{{ driftCount }}</strong><span>漂移</span></div>
        <div :class="{ 'metric-alert': attentionCount > 0 }"><strong>{{ attentionCount }}</strong><span>需处理</span></div>
      </div>

      <TableSkeleton
        v-if="isInitialLoading"
        :label="matrixStatusLabel(store.matrixState)"
        :columns="matrixTargets.length + 3"
      />

      <div v-else-if="isInitialError" class="table-state state-error" role="alert">
        <p>{{ store.matrixError }}</p>
        <button class="secondary-button" type="button" @click="retryMatrix">
          <RotateCcw :size="16" aria-hidden="true" />
          重试
        </button>
      </div>

      <div
        v-else-if="store.upstreams.length === 0 || store.targets.length === 0"
        class="setup-state"
      >
        <p v-if="store.upstreams.length === 0" class="sr-only">尚未配置上游实例</p>
        <p v-if="store.targets.length === 0" class="sr-only">尚未配置目标实例</p>
        <header class="setup-header">
          <span class="setup-icon"><SlidersHorizontal :size="19" aria-hidden="true" /></span>
          <div>
            <h2>首次同步设置</h2>
            <p>{{ completedSetupSteps }} / {{ setupSteps.length }} 已完成</p>
          </div>
        </header>
        <ol class="setup-checklist" aria-label="首次同步步骤">
          <li v-for="(step, index) in setupSteps" :key="step.label" :class="{ complete: step.complete }">
            <CheckCircle2 v-if="step.complete" :size="18" aria-hidden="true" />
            <Circle v-else :size="18" aria-hidden="true" />
            <span><small>步骤 {{ index + 1 }}</small>{{ step.label }}</span>
          </li>
        </ol>
        <div class="setup-actions">
          <RouterLink v-if="store.targets.length === 0" class="primary-button" to="/targets">
            配置目标实例
          </RouterLink>
          <RouterLink v-if="store.upstreams.length === 0" class="primary-button" to="/upstreams">
            配置上游连接
          </RouterLink>
        </div>
      </div>

      <div v-else-if="rows.length === 0" class="state-panel">
        <h2>暂无上游资产</h2>
        <p>最近一次完整快照为空。</p>
        <button class="secondary-button" type="button" @click="store.refreshAssets">
          <RefreshCw :size="16" aria-hidden="true" />
          刷新资产
        </button>
      </div>

      <template v-else>
        <div
          v-if="store.matrixState === 'error'"
          class="table-state state-error"
          role="alert"
        >
          <p>{{ store.matrixError }}</p>
          <button class="secondary-button" type="button" @click="retryMatrix">
            <RotateCcw :size="16" aria-hidden="true" />
            重试
          </button>
        </div>

        <div class="table-result-count" aria-live="polite">
          显示 {{ filteredRows.length }} / {{ rows.length }} 项资产
        </div>

        <div v-if="filteredRows.length === 0" class="table-state">
          <h2>未找到匹配资产</h2>
          <button v-if="hasFilters" class="secondary-button" type="button" @click="clearFilters">
            清除筛选
          </button>
        </div>

        <div v-else class="table-scroll matrix-table-scroll">
          <table class="data-table matrix-table">
            <thead>
              <tr>
                <th class="selection-cell">
                  <input
                    type="checkbox"
                    aria-label="选择全部可同步资产"
                    :checked="allSelected"
                    :disabled="selectableRows.length === 0"
                    :indeterminate="partiallySelected"
                    :aria-checked="partiallySelected ? 'mixed' : allSelected ? 'true' : 'false'"
                    @change="toggleAll"
                  />
                </th>
                <th class="asset-cell">资产</th>
                <th class="provider-cell">供应商 / 形态</th>
                <th v-for="target in matrixTargets" :key="target.id" class="target-cell">{{ target.name }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in filteredRows" :key="row.asset.id">
                <td class="selection-cell" data-label="选择">
                  <input
                    v-model="selectedAssets"
                    type="checkbox"
                    :value="row.asset.id"
                    :disabled="!isSelectable(row)"
                    :aria-label="`选择资产 ${row.asset.name}`"
                    :aria-describedby="assetAvailability(row) ? assetReasonId(row) : undefined"
                  />
                </td>
                <td class="asset-cell" data-label="资产">
                  <strong>{{ row.asset.name }}</strong>
                  <small>{{ row.asset.id }}</small>
                  <span v-if="assetAvailability(row)" class="inline-warning">{{ assetAvailability(row)!.label }}</span>
                  <span v-if="assetAvailability(row)" :id="assetReasonId(row)" class="sr-only">
                    {{ assetAvailability(row)!.description }}
                  </span>
                </td>
                <td class="provider-cell" data-label="供应商 / 形态">
                  <span class="provider-name">{{ row.asset.provider }}</span>
                  <small>{{ row.asset.kind }}</small>
                  <small class="raw-type">{{ row.asset.raw_type }}</small>
                </td>
                <td
                  v-for="target in matrixTargets"
                  :key="target.id"
                  class="target-cell"
                  :data-label="target.name"
                >
                  <StatusBadge
                    v-if="row.cells.find((cell) => cell.target_id === target.id)"
                    :status="row.cells.find((cell) => cell.target_id === target.id)!.status"
                  />
                  <span v-else class="muted">--</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>

    <div class="selection-dock">
      <div v-if="selectedRows.length" class="selection-toolbar" role="toolbar" aria-label="批量操作">
        <span>{{ selectedRows.length }} 个已选择</span>
        <button class="secondary-button" type="button" aria-label="清除选择" @click="clearSelection">
          <X :size="16" aria-hidden="true" />
          清除选择
        </button>
        <button
          class="primary-button"
          type="button"
          :aria-label="`批量同步 ${selectedRows.length} 个资产`"
          @click="openSync"
        >
          <Send :size="16" aria-hidden="true" />
          批量同步
        </button>
      </div>
    </div>

    <ModalDialog v-if="syncOpen" title="批量同步设置" close-label="关闭批量同步" @close="closeSync">
      <form class="form-stack" @submit.prevent="submitSync">
        <div class="form-grid">
          <label class="field field-wide">
            <span>模型</span>
            <input v-model="models" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>分组</span>
            <input v-model="group" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>优先级</span>
            <input v-model.number="priority" type="number" step="1" />
          </label>
          <label class="field">
            <span>权重</span>
            <input v-model.number="weight" type="number" min="0" step="1" />
          </label>
        </div>

        <fieldset class="choice-fieldset">
          <legend>目标实例</legend>
          <label v-for="target in matrixTargets" :key="target.id" class="check-row">
            <input
              v-model="selectedTargets"
              type="checkbox"
              :value="target.id"
              :disabled="!syncableTargetIds.has(target.id)"
            />
            <span>{{ target.name }}</span>
          </label>
        </fieldset>

        <p v-if="validationError" class="form-error" role="alert">{{ validationError }}</p>

        <section v-if="results.length" class="result-section" aria-live="polite">
          <h3>{{ resultSummary }}</h3>
          <div v-for="result in results" :key="result.assetId" class="result-group">
            <strong>{{ result.assetName }}</strong>
            <ul class="result-list">
              <li v-for="target in result.targets" :key="target.target_id">
                <span>{{ targetLabel(target.target_id) }}</span>
                <span class="result-outcome">
                  <strong :class="`result-${target.status}`">{{ resultLabel(target) }}</strong>
                  <small v-if="target.channel_id">#{{ target.channel_id }}</small>
                  <code v-if="target.code">{{ target.code }}</code>
                  <small v-if="target.retryable">可重试</small>
                </span>
              </li>
            </ul>
          </div>
          <button
            v-if="retryableFailureCount"
            class="secondary-button"
            type="button"
            :disabled="submitting"
            @click="retryFailedTargets"
          >
            重试失败目标
          </button>
        </section>

        <footer class="form-actions">
          <button class="secondary-button" type="button" :disabled="submitting" @click="closeSync">关闭</button>
          <button class="primary-button" type="submit" :disabled="submitting">
            <span v-if="submitting" class="spinner spinner-small" aria-hidden="true"></span>
            <Send v-else :size="16" aria-hidden="true" />
            {{ submitting ? '同步中' : '开始同步' }}
          </button>
        </footer>
      </form>
    </ModalDialog>
  </section>
</template>

<style scoped>
.workspace-page-header {
  min-height: 38px;
}

.page-context {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
}

.sync-workspace {
  display: flex;
  min-height: 420px;
  flex: 1 1 auto;
  flex-direction: column;
  box-shadow: none;
}

.sync-page {
  display: flex;
  min-height: calc(100svh - var(--app-header-height) - 48px);
  flex-direction: column;
}

.sync-workspace :deep(.table-scroll) {
  flex: 1 1 auto;
}

.sync-workspace :deep(.workspace-toolbar) {
  min-height: 54px;
  padding: 8px 10px;
  background: var(--surface);
}

.sync-workspace :deep(.metric-strip > div) {
  min-height: 52px;
}

.sync-workspace :deep(.metric-strip strong) {
  font-size: 17px;
}

.setup-state {
  display: grid;
  gap: 0;
  min-height: 270px;
  align-content: center;
  padding: 24px;
  background: var(--surface);
}

.setup-header {
  display: flex;
  width: min(100%, 680px);
  align-items: center;
  gap: 12px;
  margin: 0 auto 12px;
}

.setup-header h2,
.setup-header p {
  margin: 0;
}

.setup-header p {
  margin-top: 3px;
  color: var(--muted);
  font-size: 12px;
}

.setup-icon {
  display: grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  border: 1px solid #bfdbfe;
  border-radius: 8px;
  color: var(--blue);
  background: var(--blue-soft);
}

.setup-checklist {
  display: grid;
  width: min(100%, 680px);
  margin: 0 auto;
  padding: 0;
  border-top: 1px solid var(--line);
  list-style: none;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.setup-checklist li {
  display: flex;
  min-height: 58px;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-right: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  color: var(--muted);
}

.setup-checklist li:nth-child(even) {
  border-right: 0;
}

.setup-checklist li.complete {
  color: #047857;
}

.setup-checklist span {
  display: grid;
  gap: 2px;
  color: var(--ink);
  font-size: 13px;
  font-weight: 650;
}

.setup-checklist small {
  color: var(--muted);
  font-size: 10px;
  font-weight: 500;
}

.setup-actions {
  display: flex;
  justify-self: center;
  gap: 8px;
  margin-top: 16px;
}

.selection-dock {
  position: sticky;
  z-index: 12;
  bottom: calc(10px + env(safe-area-inset-bottom));
  display: flex;
  min-height: 58px;
  align-items: flex-end;
  pointer-events: none;
}

.selection-dock .selection-toolbar {
  width: 100%;
  min-height: 50px;
  border: 1px solid #bfdbfe;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
  pointer-events: auto;
}

@media (max-width: 620px) {
  .sync-page {
    display: block;
    min-height: 0;
  }

  .sync-workspace {
    display: block;
    min-height: 0;
  }

  .workspace-page-header {
    display: block;
  }

  .setup-state {
    padding: 18px 12px;
  }

  .setup-checklist {
    grid-template-columns: 1fr;
  }

  .setup-checklist li,
  .setup-checklist li:nth-child(even) {
    border-right: 0;
  }

  .setup-actions {
    width: 100%;
    flex-direction: column;
  }

  .setup-actions .primary-button {
    width: 100%;
  }

  .matrix-table tbody tr {
    border-left: 3px solid var(--line);
  }

  .selection-dock {
    bottom: calc(6px + env(safe-area-inset-bottom));
    min-height: 66px;
  }

  .selection-dock .selection-toolbar {
    padding: 8px;
  }
}
</style>
