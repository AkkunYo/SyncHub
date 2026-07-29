import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { api, safeErrorMessage } from '@/api/client'
import type {
  AssetSyncResult,
  Channel,
  DriftItem,
  MatrixData,
  RuntimeInfo,
  SanitizedConfig,
  SyncTargetResult,
  TargetConfig,
  UpstreamConfig,
  ViewName,
} from '@/types'

type LoadState = 'idle' | 'loading' | 'ready' | 'error'

export const useConsoleStore = defineStore('console', () => {
  const config = ref<SanitizedConfig | null>(null)
  const runtimeInfo = ref<RuntimeInfo | null>(null)
  const matrix = ref<MatrixData | null>(null)
  const selectedUpstreamId = ref('')
  const selectedTargetId = ref('')
  const channels = ref<Channel[]>([])
  const activeView = ref<ViewName>('matrix')
  const initialState = ref<LoadState>('idle')
  const matrixState = ref<LoadState>('idle')
  const channelState = ref<LoadState>('idle')
  const initialError = ref('')
  const matrixError = ref('')
  const channelError = ref('')
  let matrixRequestId = 0
  let channelRequestId = 0
  let matrixController: AbortController | null = null
  let channelController: AbortController | null = null

  const targets = computed(() => config.value?.targets ?? [])
  const upstreams = computed(() => config.value?.upstreams ?? [])
  const driftItems = computed<DriftItem[]>(() => {
    if (!matrix.value) return []
    const targetNames = new Map(matrix.value.targets.map((target) => [target.id, target.name]))
    return matrix.value.rows.flatMap((row) =>
      row.cells
        .filter((cell) => cell.status === 'drifted' && Boolean(cell.channel_id))
        .map((cell) => ({
          assetId: row.asset.id,
          assetName: row.asset.name,
          targetId: cell.target_id,
          targetName: targetNames.get(cell.target_id) ?? cell.target_id,
          channelId: cell.channel_id ?? '',
          differences: cell.differences ?? [],
        })),
    )
  })

  async function loadConsole(): Promise<void> {
    initialState.value = 'loading'
    initialError.value = ''
    try {
      const healthRequest = api.getHealth().catch(() => null)
      config.value = await api.getConfig()
      runtimeInfo.value = await healthRequest
      selectedTargetId.value = targets.value[0]?.id ?? ''
      const firstUpstream = upstreams.value[0]?.id ?? ''
      selectedUpstreamId.value = firstUpstream
      if (firstUpstream) await loadMatrix(firstUpstream)
      else {
        matrix.value = null
        matrixState.value = 'ready'
      }
      initialState.value = 'ready'
    } catch (error) {
      initialError.value = safeErrorMessage(error)
      initialState.value = 'error'
    }
  }

  async function loadMatrix(upstreamId = selectedUpstreamId.value): Promise<void> {
    const requestId = ++matrixRequestId
    matrixController?.abort()
    matrixController = null
    if (!upstreamId) {
      matrix.value = null
      matrixState.value = 'ready'
      return
    }
    const controller = new AbortController()
    matrixController = controller
    selectedUpstreamId.value = upstreamId
    matrixState.value = 'loading'
    matrixError.value = ''
    try {
      const response = await api.getMatrix(upstreamId, controller.signal)
      if (requestId !== matrixRequestId || selectedUpstreamId.value !== upstreamId) return
      matrix.value = response
      matrixState.value = 'ready'
    } catch (error) {
      if (requestId !== matrixRequestId || selectedUpstreamId.value !== upstreamId) return
      matrixError.value = safeErrorMessage(error)
      matrixState.value = 'error'
      throw error
    } finally {
      if (requestId === matrixRequestId) matrixController = null
    }
  }

  async function refreshAssets(): Promise<void> {
    const upstreamId = selectedUpstreamId.value
    if (!upstreamId) return
    matrixState.value = 'loading'
    matrixError.value = ''
    try {
      await api.refreshUpstream(upstreamId)
      if (selectedUpstreamId.value !== upstreamId) return
      await loadMatrix(upstreamId)
    } catch (error) {
      if (selectedUpstreamId.value !== upstreamId) return
      matrixError.value = safeErrorMessage(error)
      matrixState.value = 'error'
    }
  }

  async function loadChannels(targetId = selectedTargetId.value): Promise<void> {
    const requestId = ++channelRequestId
    channelController?.abort()
    channelController = null
    if (!targetId) {
      channels.value = []
      channelState.value = 'ready'
      return
    }
    const controller = new AbortController()
    channelController = controller
    selectedTargetId.value = targetId
    channelState.value = 'loading'
    channelError.value = ''
    try {
      const response = await api.getChannels(targetId, controller.signal)
      if (requestId !== channelRequestId || selectedTargetId.value !== targetId) return
      channels.value = response.channels
      channelState.value = 'ready'
    } catch (error) {
      if (requestId !== channelRequestId || selectedTargetId.value !== targetId) return
      channelError.value = safeErrorMessage(error)
      channelState.value = 'error'
    } finally {
      if (requestId === channelRequestId) channelController = null
    }
  }

  function navigate(view: ViewName): void {
    activeView.value = view
    if (view === 'channels') void loadChannels()
  }

  async function refreshMatrixAfterConfigChange(): Promise<void> {
    if (!selectedUpstreamId.value) {
      matrix.value = null
      matrixState.value = 'ready'
      return
    }
    try {
      await loadMatrix(selectedUpstreamId.value)
    } catch {
      // The matrix exposes its own sanitized retry state after a config write succeeds.
    }
  }

  function applySyncResults(results: AssetSyncResult[]): void {
    if (!matrix.value) return
    const resultsByAsset = new Map(results.map((result) => [result.assetId, result.targets]))
    for (const row of matrix.value.rows) {
      const targetResults = resultsByAsset.get(row.asset.id)
      if (!targetResults) continue
      const byTarget = new Map(targetResults.map((result) => [result.target_id, result]))
      row.cells = row.cells.map((cell) => {
        const result = byTarget.get(cell.target_id)
        return result
          ? {
              ...cell,
              status: result.status === 'failed' ? cell.status : result.status,
              channel_id: result.channel_id ?? cell.channel_id,
              code: result.code,
              retryable: result.retryable,
              differences: result.status === 'failed' ? cell.differences : undefined,
            }
          : cell
      })
    }
  }

  function markDriftAccepted(assetId: string, targetId: string): void {
    const row = matrix.value?.rows.find((candidate) => candidate.asset.id === assetId)
    const cell = row?.cells.find((candidate) => candidate.target_id === targetId)
    if (!cell) return
    cell.status = 'synced'
    cell.differences = undefined
    cell.code = undefined
  }

  async function upsertTarget(target: TargetConfig): Promise<void> {
    if (!config.value) return
    const index = config.value.targets.findIndex((candidate) => candidate.id === target.id)
    if (index === -1) config.value.targets.push(target)
    else config.value.targets[index] = target
    if (!selectedTargetId.value) selectedTargetId.value = target.id
    if (matrix.value) await refreshMatrixAfterConfigChange()
  }

  async function removeTarget(targetId: string): Promise<void> {
    if (!config.value) return
    config.value.targets = config.value.targets.filter((target) => target.id !== targetId)
    if (selectedTargetId.value === targetId) selectedTargetId.value = config.value.targets[0]?.id ?? ''
    if (matrix.value) await refreshMatrixAfterConfigChange()
  }

  async function upsertUpstream(upstream: UpstreamConfig): Promise<void> {
    if (!config.value) return
    const index = config.value.upstreams.findIndex((candidate) => candidate.id === upstream.id)
    if (index === -1) config.value.upstreams.push(upstream)
    else config.value.upstreams[index] = upstream
    if (!selectedUpstreamId.value) {
      selectedUpstreamId.value = upstream.id
      await refreshMatrixAfterConfigChange()
    }
  }

  async function removeUpstream(upstreamId: string): Promise<void> {
    if (!config.value) return
    config.value.upstreams = config.value.upstreams.filter((upstream) => upstream.id !== upstreamId)
    if (selectedUpstreamId.value === upstreamId) {
      selectedUpstreamId.value = config.value.upstreams[0]?.id ?? ''
      matrix.value = null
      await refreshMatrixAfterConfigChange()
    }
  }

  function replaceAppSettings(settings: SanitizedConfig['app']): void {
    if (config.value) config.value.app = settings
  }

  function replaceChannel(channel: Channel): void {
    const index = channels.value.findIndex((candidate) => candidate.id === channel.id)
    if (index !== -1) channels.value[index] = channel
  }

  function removeChannel(channelId: string): void {
    channels.value = channels.value.filter((channel) => channel.id !== channelId)
  }

  function markChannelDeleted(assetId: string, targetId: string, channelId: string): void {
    const row = matrix.value?.rows.find((candidate) => candidate.asset.id === assetId)
    const cell = row?.cells.find(
      (candidate) => candidate.target_id === targetId && candidate.channel_id === channelId,
    )
    if (!cell) return
    cell.status = 'unsynced'
    cell.channel_id = undefined
    cell.code = undefined
    cell.retryable = undefined
    cell.differences = undefined
  }

  function targetName(targetId: string): string {
    return targets.value.find((target) => target.id === targetId)?.name ?? targetId
  }

  function updateCell(assetId: string, result: SyncTargetResult): void {
    applySyncResults([{ assetId, assetName: assetId, targets: [result] }])
  }

  return {
    config,
    runtimeInfo,
    matrix,
    channels,
    activeView,
    initialState,
    matrixState,
    channelState,
    initialError,
    matrixError,
    channelError,
    selectedUpstreamId,
    selectedTargetId,
    targets,
    upstreams,
    driftItems,
    loadConsole,
    loadMatrix,
    refreshAssets,
    loadChannels,
    navigate,
    applySyncResults,
    markDriftAccepted,
    upsertTarget,
    removeTarget,
    upsertUpstream,
    removeUpstream,
    replaceAppSettings,
    replaceChannel,
    removeChannel,
    markChannelDeleted,
    targetName,
    updateCell,
  }
})
