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
  TargetValidationStatus,
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
  const healthState = ref<LoadState>('idle')
  const matrixState = ref<LoadState>('idle')
  const channelState = ref<LoadState>('idle')
  const initialError = ref('')
  const matrixError = ref('')
  const channelError = ref('')
  let matrixGeneration = 0
  let channelRequestId = 0
  let consoleRequestId = 0
  let matrixController: AbortController | null = null
  let channelController: AbortController | null = null

  const targets = computed(() => config.value?.targets ?? [])
  const upstreams = computed(() => config.value?.upstreams ?? [])
  const driftItems = computed<DriftItem[]>(() => {
    const currentMatrix = matrix.value
    if (
      matrixState.value !== 'ready'
      || !currentMatrix
      || currentMatrix.upstream_id !== selectedUpstreamId.value
    ) return []
    const targetNames = new Map(currentMatrix.targets.map((target) => [target.id, target.name]))
    return currentMatrix.rows.flatMap((row) =>
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

  function invalidateMatrixRequest(): number {
    const generation = ++matrixGeneration
    matrixController?.abort()
    matrixController = null
    return generation
  }

  function isCurrentMatrixRequest(generation: number, upstreamId: string): boolean {
    return generation === matrixGeneration && selectedUpstreamId.value === upstreamId
  }

  async function loadMatrixForGeneration(upstreamId: string, generation: number): Promise<void> {
    if (!isCurrentMatrixRequest(generation, upstreamId)) return
    const controller = new AbortController()
    matrixController = controller
    matrixState.value = 'loading'
    matrixError.value = ''
    try {
      const response = await api.getMatrix(upstreamId, controller.signal)
      if (!isCurrentMatrixRequest(generation, upstreamId)) return
      matrix.value = response
      matrixState.value = 'ready'
    } catch (error) {
      if (!isCurrentMatrixRequest(generation, upstreamId)) return
      matrixError.value = safeErrorMessage(error)
      matrixState.value = 'error'
      throw error
    } finally {
      if (generation === matrixGeneration && matrixController === controller) matrixController = null
    }
  }

  async function loadConsole(): Promise<void> {
    const requestId = ++consoleRequestId
    invalidateMatrixRequest()
    initialState.value = 'loading'
    healthState.value = 'loading'
    initialError.value = ''
    runtimeInfo.value = null

    void api.getHealth().then(
      (response) => {
        if (requestId !== consoleRequestId) return
        runtimeInfo.value = response
        healthState.value = 'ready'
      },
      () => {
        if (requestId !== consoleRequestId) return
        healthState.value = 'error'
      },
    )

    try {
      const response = await api.getConfig()
      if (requestId !== consoleRequestId) return
      config.value = response
      selectedTargetId.value = targets.value[0]?.id ?? ''
      const firstUpstream = upstreams.value[0]?.id ?? ''
      selectedUpstreamId.value = firstUpstream
      initialState.value = 'ready'

      if (firstUpstream) {
        void loadMatrix(firstUpstream).catch(() => undefined)
      } else {
        matrix.value = null
        matrixError.value = ''
        matrixState.value = 'ready'
      }
    } catch (error) {
      if (requestId !== consoleRequestId) return
      initialError.value = safeErrorMessage(error)
      initialState.value = 'error'
    }
  }

  async function loadMatrix(upstreamId = selectedUpstreamId.value): Promise<void> {
    const generation = invalidateMatrixRequest()
    selectedUpstreamId.value = upstreamId
    if (!upstreamId) {
      matrix.value = null
      matrixError.value = ''
      matrixState.value = 'ready'
      return
    }
    await loadMatrixForGeneration(upstreamId, generation)
  }

  async function refreshAssets(): Promise<void> {
    const upstreamId = selectedUpstreamId.value
    if (!upstreamId) return
    const generation = invalidateMatrixRequest()
    matrixState.value = 'loading'
    matrixError.value = ''
    try {
      await api.refreshUpstream(upstreamId)
      if (!isCurrentMatrixRequest(generation, upstreamId)) return
      await loadMatrixForGeneration(upstreamId, generation)
    } catch (error) {
      if (!isCurrentMatrixRequest(generation, upstreamId)) return
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
      invalidateMatrixRequest()
      matrix.value = null
      matrixError.value = ''
      matrixState.value = 'ready'
      return
    }
    try {
      await loadMatrix(selectedUpstreamId.value)
    } catch {
      // The matrix exposes its own sanitized retry state after a config write succeeds.
    }
  }

  function applySyncResults(
    results: AssetSyncResult[],
    expectedUpstreamId = selectedUpstreamId.value,
  ): boolean {
    const currentMatrix = matrix.value
    if (
      !currentMatrix
      || selectedUpstreamId.value !== expectedUpstreamId
      || currentMatrix.upstream_id !== expectedUpstreamId
    ) return false
    const resultsByAsset = new Map(results.map((result) => [result.assetId, result.targets]))
    for (const row of currentMatrix.rows) {
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
    return true
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

  function setTargetValidation(
    targetId: string,
    status: TargetValidationStatus,
    details: { validatedAt?: string; capabilities?: Record<string, unknown> } = {},
  ): void {
    const target = config.value?.targets.find((candidate) => candidate.id === targetId)
    if (!target) return
    target.validation_status = status
    if (status === 'verified') {
      target.validated_at = details.validatedAt
      target.validation_capabilities = details.capabilities
      return
    }
    target.validated_at = undefined
    target.validation_capabilities = undefined
  }

  async function removeTarget(targetId: string): Promise<void> {
    if (!config.value) return
    config.value.targets = config.value.targets.filter((target) => target.id !== targetId)
    if (selectedTargetId.value === targetId) selectedTargetId.value = config.value.targets[0]?.id ?? ''
    if (matrix.value) await refreshMatrixAfterConfigChange()
  }

  async function upsertUpstream(upstream: UpstreamConfig): Promise<void> {
    if (!config.value) return
    const refreshSelected = !selectedUpstreamId.value || selectedUpstreamId.value === upstream.id
    const index = config.value.upstreams.findIndex((candidate) => candidate.id === upstream.id)
    if (index === -1) config.value.upstreams.push(upstream)
    else config.value.upstreams[index] = upstream
    if (!selectedUpstreamId.value) selectedUpstreamId.value = upstream.id
    if (refreshSelected) {
      matrix.value = null
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
    healthState,
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
    setTargetValidation,
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
