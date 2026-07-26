<script setup lang="ts">
import { computed, ref } from 'vue'
import { RefreshCw, RotateCcw, Send, SlidersHorizontal } from 'lucide-vue-next'

import { api } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useConsoleStore } from '@/stores/console'
import type { AssetSyncResult, MatrixRow, SyncTargetResult } from '@/types'

const store = useConsoleStore()
const selectedAssets = ref<string[]>([])
const syncOpen = ref(false)
const models = ref('')
const group = ref('default')
const priority = ref(0)
const weight = ref(100)
const selectedTargets = ref<string[]>([])
const securityProof = ref('')
const allowAuthFile = ref(false)
const submitting = ref(false)
const validationError = ref('')
const results = ref<AssetSyncResult[]>([])

interface SubmittedRow {
  row: MatrixRow
  targetIds: string[]
}

const rows = computed(() => store.matrix?.rows ?? [])
const matrixTargets = computed(() => store.matrix?.targets ?? store.targets)
const selectedRows = computed(() => rows.value.filter((row) => selectedAssets.value.includes(row.asset.id)))
const selectableRows = computed(() => rows.value.filter(isSelectable))
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

function onUpstreamChange(event: Event): void {
  selectedAssets.value = []
  void store.loadMatrix((event.target as HTMLSelectElement).value)
}

function toggleAll(): void {
  selectedAssets.value = allSelected.value ? [] : selectableRows.value.map((row) => row.asset.id)
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
  securityProof.value = ''
  allowAuthFile.value = false
  validationError.value = ''
  results.value = []
  syncOpen.value = true
}

function closeSync(): void {
  securityProof.value = ''
  syncOpen.value = false
}

function parseModels(): string[] {
  return [...new Set(models.value.split(',').map((model) => model.trim()).filter(Boolean))]
}

function failedTargets(targetIds: readonly string[], code = 'request_failed'): SyncTargetResult[] {
  return targetIds.map((targetId) => ({
    target_id: targetId,
    status: 'failed',
    code,
    retryable: true,
  }))
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
  validationError.value = ''
  results.value = []
  await runSync(submittedRows, false)
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

async function runSync(submittedRows: SubmittedRow[], merge: boolean): Promise<void> {
  submitting.value = true
  let requestProof = securityProof.value
  securityProof.value = ''
  try {
    const normalizedModels = parseModels()
    const completed = await Promise.all(
      submittedRows.map(async ({ row, targetIds }): Promise<AssetSyncResult> => {
        try {
          const response = await api.sync({
            upstream_id: store.selectedUpstreamId,
            asset_id: row.asset.id,
            target_ids: targetIds,
            settings: {
              models: normalizedModels,
              group: group.value.trim() || 'default',
              priority: priority.value,
              weight: weight.value,
            },
            grant: {
              security_proof: requestProof,
              allow_auth_file: allowAuthFile.value,
            },
          })
          return { assetId: row.asset.id, assetName: row.asset.name, targets: response.targets }
        } catch (error) {
          return {
            assetId: row.asset.id,
            assetName: row.asset.name,
            targets: failedTargets(targetIds, error instanceof Error ? 'request_failed' : 'internal_error'),
          }
        }
      }),
    )
    results.value = merge ? mergeSyncResults(results.value, completed) : completed
    store.applySyncResults(completed)
  } finally {
    requestProof = ''
    submitting.value = false
  }
}

async function retryFailedTargets(): Promise<void> {
  if (!securityProof.value.trim()) {
    validationError.value = '重试前请重新输入一次性安全证明'
    return
  }
  const submittedRows: SubmittedRow[] = results.value.flatMap((result) => {
    const row = rows.value.find((candidate) => candidate.asset.id === result.assetId)
    const targetIds = result.targets
      .filter((target) => target.status === 'failed' && target.retryable)
      .map((target) => target.target_id)
    return row && targetIds.length > 0 ? [{ row, targetIds }] : []
  })
  if (submittedRows.length === 0) return
  validationError.value = ''
  await runSync(submittedRows, true)
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
  <section class="page" aria-labelledby="matrix-heading">
    <header class="page-header">
      <div>
        <p class="eyebrow">上游资产</p>
        <h1 id="matrix-heading">资产同步矩阵</h1>
      </div>
      <div class="page-actions matrix-page-actions">
        <label class="compact-field">
          <span>上游实例</span>
          <select :value="store.selectedUpstreamId" @change="onUpstreamChange">
            <option v-for="source in store.upstreams" :key="source.id" :value="source.id">
              {{ source.name }}
            </option>
          </select>
        </label>
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
    </header>

    <div v-if="store.matrix" class="metric-strip" aria-label="矩阵摘要">
      <div><strong>{{ rows.length }}</strong><span>资产</span></div>
      <div><strong>{{ syncedCount }}</strong><span>已同步</span></div>
      <div><strong>{{ driftCount }}</strong><span>漂移</span></div>
      <div><strong>{{ attentionCount }}</strong><span>需处理</span></div>
    </div>

    <div
      v-if="store.matrixState === 'loading'"
      class="state-panel"
      role="status"
      :aria-label="matrixStatusLabel(store.matrixState)"
    >
      <span class="spinner" aria-hidden="true"></span>
      <p>正在读取完整资产矩阵</p>
    </div>

    <div v-else-if="store.matrixState === 'error'" class="state-panel state-error" role="alert">
      <p>{{ store.matrixError }}</p>
      <button class="secondary-button" type="button" @click="retryMatrix">
        <RotateCcw :size="16" aria-hidden="true" />
        重试
      </button>
    </div>

    <div v-else-if="store.upstreams.length === 0" class="state-panel">
      <SlidersHorizontal :size="24" aria-hidden="true" />
      <h2>尚未配置上游实例</h2>
      <button class="primary-button" type="button" @click="store.navigate('settings')">前往设置</button>
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
      <div class="table-toolbar">
        <span>{{ selectedAssets.length }} 个已选择</span>
        <button
          class="primary-button"
          type="button"
          :disabled="selectedAssets.length === 0"
          :aria-label="`批量同步 ${selectedAssets.length} 个资产`"
          @click="openSync"
        >
          <Send :size="16" aria-hidden="true" />
          批量同步
        </button>
      </div>
      <div class="table-scroll">
        <table class="data-table matrix-table">
          <thead>
            <tr>
              <th class="selection-cell">
                <input
                  type="checkbox"
                  aria-label="选择全部可同步资产"
                  :checked="allSelected"
                  @change="toggleAll"
                />
              </th>
              <th>资产</th>
              <th>供应商 / 形态</th>
              <th v-for="target in matrixTargets" :key="target.id">{{ target.name }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.asset.id">
              <td class="selection-cell">
                <input
                  v-model="selectedAssets"
                  type="checkbox"
                  :value="row.asset.id"
                  :disabled="!isSelectable(row)"
                  :aria-label="`选择资产 ${row.asset.name}`"
                  :aria-describedby="assetAvailability(row) ? assetReasonId(row) : undefined"
                />
              </td>
              <td>
                <strong>{{ row.asset.name }}</strong>
                <small>{{ row.asset.id }}</small>
                <span v-if="assetAvailability(row)" class="inline-warning">{{ assetAvailability(row)!.label }}</span>
                <span v-if="assetAvailability(row)" :id="assetReasonId(row)" class="sr-only">
                  {{ assetAvailability(row)!.description }}
                </span>
              </td>
              <td>
                <span class="provider-name">{{ row.asset.provider }}</span>
                <small>{{ row.asset.kind }}</small>
                <small class="raw-type">{{ row.asset.raw_type }}</small>
              </td>
              <td v-for="target in matrixTargets" :key="target.id">
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

        <label class="field">
          <span>一次性安全证明</span>
          <input
            v-model="securityProof"
            type="password"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </label>
        <label class="check-row">
          <input v-model="allowAuthFile" type="checkbox" />
          <span>允许兼容认证文件迁移</span>
        </label>

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
          <button class="secondary-button" type="button" @click="closeSync">关闭</button>
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
