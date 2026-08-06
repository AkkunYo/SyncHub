<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Eye,
  FlaskConical,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Settings2,
  Trash2,
} from 'lucide-vue-next'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import SidePanel from '@/components/SidePanel.vue'
import { useConsoleStore } from '@/stores/console'
import type {
  ConnectionTestResult,
  KeyModelObservation,
  KeyModelsResponse,
  ModelDiscoveryItem,
  ModelProbeProtocol,
  ModelProbeStatus,
  TargetConfig,
  UpstreamConfig,
  UpstreamGroupsResponse,
  UpstreamKey,
  UpstreamKeyUpdateInput,
} from '@/types'

type ResourceKind = 'upstream' | 'target' | 'drift' | 'task'
type DetailTab = 'keys' | 'groups' | 'overview' | 'settings'
type ConnectionState = 'unverified' | 'testing' | 'verified' | 'failed'
type LoadState = 'idle' | 'loading' | 'ready' | 'error'
type ProbeFilter = 'all' | 'untested' | ModelProbeStatus
type DiscoveryFilter = 'all' | KeyModelObservation['discovery_status']
type ModelSort = 'name_asc' | 'name_desc' | 'discovery' | 'probe'

const props = defineProps<{
  kind: ResourceKind
  title: string
  backTo: string
  backLabel: string
}>()

const route = useRoute()
const router = useRouter()
const store = useConsoleStore()
const connectionState = ref<ConnectionState>('unverified')
const connectionResult = ref<ConnectionTestResult | null>(null)
const connectionError = ref('')
const keyPanelOpen = ref(false)
const editingKeyId = ref('')
const keySaving = ref(false)
const keyDeleting = ref(false)
const keyDeleteArmed = ref(false)
const keyError = ref('')
const keys = ref<UpstreamKey[]>([])
const keysState = ref<LoadState>('idle')
const keysError = ref('')
const groupsState = ref<LoadState>('idle')
const groupsSnapshot = ref<UpstreamGroupsResponse | null>(null)
const groupsError = ref('')
const modelPanelOpen = ref(false)
const selectedKey = ref<UpstreamKey | null>(null)
const modelsState = ref<LoadState>('idle')
const modelSnapshot = ref<KeyModelsResponse | null>(null)
const modelsError = ref('')
const modelQuery = ref('')
const probeFilter = ref<ProbeFilter>('all')
const discoveryFilter = ref<DiscoveryFilter>('all')
const modelSort = ref<ModelSort>('name_asc')
const protocolByModel = reactive<Record<string, ModelProbeProtocol>>({})
const probingModels = ref(new Set<string>())
const probeConfirmation = ref<{
  model: KeyModelObservation
  protocol: ModelProbeProtocol
} | null>(null)
const discoveryRunning = ref(false)
const discoveryNotice = ref('')
const discoveryWarning = ref('')
const keyForm = reactive({
  id: '',
  name: '',
  apiKey: '',
  enabled: true,
  models: '',
})
const keyFormModelsKnown = ref(false)

const resourceId = computed(() => {
  const id = route.params.id
  return Array.isArray(id) ? (id[0] ?? '') : (id ?? '')
})

const upstreamResource = computed<UpstreamConfig | null>(() => (
  props.kind === 'upstream'
    ? store.upstreams.find((upstream) => upstream.id === resourceId.value) ?? null
    : null
))
const targetResource = computed<TargetConfig | null>(() => (
  props.kind === 'target'
    ? store.targets.find((target) => target.id === resourceId.value) ?? null
    : null
))
const targetCapabilities = computed<Record<string, unknown> | null>(() => (
  connectionResult.value?.capabilities
    ?? targetResource.value?.validation_capabilities
    ?? null
))
const targetValidatedAt = computed(() => (
  connectionResult.value?.validated_at ?? targetResource.value?.validated_at ?? ''
))
const targetCapabilityPlatform = computed(() => {
  const platform = targetCapabilities.value?.platform
  return typeof platform === 'string' ? platform : ''
})
const targetProviders = computed(() => {
  const providers = targetCapabilities.value?.providers
  return providers && typeof providers === 'object' && !Array.isArray(providers)
    ? Object.entries(providers as Record<string, unknown>)
    : []
})
const targetProviderModes = computed(() => {
  const modes = new Set<string>()
  for (const [, provider] of targetProviders.value) {
    if (!provider || typeof provider !== 'object' || Array.isArray(provider)) continue
    const candidateModes = (provider as { modes?: unknown }).modes
    if (!Array.isArray(candidateModes)) continue
    for (const mode of candidateModes) if (typeof mode === 'string') modes.add(mode)
  }
  return [...modes]
})
const simpleTargetCapabilities = computed(() => Object.entries(targetCapabilities.value ?? {}).filter(
  ([name, value]) => !['platform', 'providers', 'native_auth_schema'].includes(name)
    && ['boolean', 'string', 'number'].includes(typeof value),
))
const configuredResource = computed(() => upstreamResource.value ?? targetResource.value)
const isConfiguredKind = computed(() => props.kind === 'upstream' || props.kind === 'target')
const resourceMissing = computed(() => isConfiguredKind.value && !configuredResource.value)
const resourceTypeLabel = computed(() => (
  props.kind === 'task' ? '任务 ID' : props.kind === 'drift' ? '漂移 ID' : '实例 ID'
))
const defaultTab = computed<DetailTab>(() => (props.kind === 'upstream' ? 'keys' : 'overview'))
const activeTab = computed<DetailTab>(() => {
  const queryTab = typeof route.query.tab === 'string' ? route.query.tab : ''
  if (props.kind === 'upstream' && ['keys', 'groups', 'settings'].includes(queryTab)) return queryTab as DetailTab
  if (props.kind === 'target' && ['overview', 'settings'].includes(queryTab)) return queryTab as DetailTab
  return defaultTab.value
})
const isEditingKey = computed(() => Boolean(editingKeyId.value))
const displayedKeys = computed(() => keys.value)
const filteredModels = computed(() => {
  const query = modelQuery.value.trim().toLocaleLowerCase()
  const models = (modelSnapshot.value?.models ?? []).filter((model) => {
    const matchesQuery = !query || model.id.toLocaleLowerCase().includes(query)
    const status = model.probe?.status ?? 'untested'
    const matchesProbe = probeFilter.value === 'all' || probeFilter.value === status
    const matchesDiscovery = discoveryFilter.value === 'all'
      || discoveryFilter.value === model.discovery_status
    return matchesQuery && matchesProbe && matchesDiscovery
  })
  return [...models].sort((left, right) => {
    if (modelSort.value === 'name_desc') return right.id.localeCompare(left.id)
    if (modelSort.value === 'discovery') {
      const discoveryOrder = { discovered: 0, unverified: 1 }
      const difference = discoveryOrder[left.discovery_status] - discoveryOrder[right.discovery_status]
      return difference || left.id.localeCompare(right.id)
    }
    if (modelSort.value === 'probe') {
      const leftStatus = left.probe?.status ?? 'untested'
      const rightStatus = right.probe?.status ?? 'untested'
      return leftStatus.localeCompare(rightStatus) || left.id.localeCompare(right.id)
    }
    return left.id.localeCompare(right.id)
  })
})

function syncKeys(nextKeys: UpstreamKey[]): void {
  keys.value = nextKeys.map((key) => ({
    ...key,
    models: Array.isArray(key.models) ? [...key.models] : undefined,
  }))
  if (upstreamResource.value) upstreamResource.value.keys = keys.value
}

function keyModelCount(key: UpstreamKey): number {
  return key.model_count ?? key.models?.length ?? 0
}

function keyDiscoveryLabel(key: UpstreamKey): string {
  if (key.snapshot_status === 'ready' || key.discovery_status === 'succeeded') return '已发现'
  if (key.snapshot_status === 'empty' || key.discovery_status === 'empty') return '空列表'
  if (key.snapshot_status === 'stale') return '已过期'
  if (key.discovery_status === 'authentication_failed') return '鉴权失败'
  if (key.discovery_status === 'rate_limited') return '请求受限'
  if (key.discovery_status === 'failed') return '发现失败'
  return '未验证'
}

function keyDiscoveryClass(key: UpstreamKey): string {
  if (key.snapshot_status === 'ready' || key.discovery_status === 'succeeded') return 'is-on'
  if (key.snapshot_status === 'stale' || key.discovery_status === 'rate_limited') return 'is-unverified'
  if (key.discovery_status && !['', 'empty'].includes(key.discovery_status)) return 'is-error'
  return 'is-unverified'
}

async function loadKeys(): Promise<void> {
  const upstream = upstreamResource.value
  if (!upstream) return
  keysState.value = 'loading'
  keysError.value = ''
  try {
    const response = await api.getUpstreamKeys(upstream.id)
    syncKeys(response.keys)
    keysState.value = 'ready'
  } catch (error) {
    keysError.value = safeErrorMessage(error)
    keysState.value = 'error'
  }
}

async function loadGroups(): Promise<void> {
  const upstream = upstreamResource.value
  if (!upstream || upstream.type !== 'newapi') return
  groupsState.value = 'loading'
  groupsError.value = ''
  try {
    groupsSnapshot.value = await api.getGroups(upstream.id)
    groupsState.value = 'ready'
  } catch (error) {
    groupsError.value = safeErrorMessage(error)
    groupsState.value = 'error'
  }
}

async function refreshGroups(): Promise<void> {
  const upstream = upstreamResource.value
  if (!upstream || upstream.type !== 'newapi') return
  groupsState.value = 'loading'
  groupsError.value = ''
  try {
    await api.refreshUpstream(upstream.id)
    groupsSnapshot.value = await api.getGroups(upstream.id)
    groupsState.value = 'ready'
  } catch (error) {
    groupsError.value = safeErrorMessage(error)
    groupsState.value = 'error'
  }
}

function groupRatioLabel(ratio: number | null, known: boolean): string {
  return known && ratio !== null ? `${ratio}x` : '未知'
}

function modelProbeLabel(status?: ModelProbeStatus): string {
  if (!status) return '未测试'
  const labels: Record<ModelProbeStatus, string> = {
    healthy: '健康',
    reachable_inconclusive: '可达未确认',
    authentication_failed: '鉴权失败',
    model_unavailable: '模型不可用',
    rate_limited: '请求受限',
    timeout: '超时',
    network_error: '网络错误',
    invalid_response: '响应无效',
    unsupported: '协议不支持',
  }
  return labels[status]
}

function modelDiscoveryWarning(item?: ModelDiscoveryItem): string {
  if (!item || item.status === 'succeeded' || item.status === 'empty') return ''
  const labels: Record<string, string> = {
    authentication_failed: '鉴权失败',
    rate_limited: '请求受限',
    unsupported: '模型发现不受支持',
    timeout: '请求超时',
    network_error: '网络错误',
  }
  return `部分模型刷新未完成：${labels[item.error_code ?? item.status] ?? '上游请求失败'}`
}

function resetModelPanel(): void {
  modelsState.value = 'idle'
  modelSnapshot.value = null
  modelsError.value = ''
  modelQuery.value = ''
  probeFilter.value = 'all'
  discoveryFilter.value = 'all'
  modelSort.value = 'name_asc'
  discoveryNotice.value = ''
  discoveryWarning.value = ''
  probingModels.value = new Set()
  probeConfirmation.value = null
  for (const model of Object.keys(protocolByModel)) delete protocolByModel[model]
}

async function loadModels(): Promise<void> {
  const upstream = upstreamResource.value
  const key = selectedKey.value
  if (!upstream || !key) return
  modelsState.value = 'loading'
  modelsError.value = ''
  try {
    const response = await api.getKeyModels(upstream.id, key.id)
    if (selectedKey.value?.id !== key.id) return
    modelSnapshot.value = response
    for (const model of response.models) {
      protocolByModel[model.id] ??= model.probe?.protocol ?? 'auto'
    }
    const currentKey = keys.value.find((candidate) => candidate.id === key.id)
    if (currentKey) {
      currentKey.models = response.models.map((model) => model.id)
      currentKey.model_count = response.models.length
      currentKey.snapshot_status = response.snapshot_status
      currentKey.discovery_status = response.snapshot_status === 'ready' ? 'succeeded' : response.snapshot_status
      currentKey.discovered_at = response.discovered_at
    }
    modelsState.value = 'ready'
  } catch (error) {
    if (selectedKey.value?.id !== key.id) return
    modelsError.value = safeErrorMessage(error)
    modelsState.value = 'error'
  }
}

function openModels(key: UpstreamKey): void {
  resetModelPanel()
  selectedKey.value = key
  modelPanelOpen.value = true
  void loadModels()
}

function closeModels(): void {
  if (discoveryRunning.value || probingModels.value.size > 0) return
  modelPanelOpen.value = false
  selectedKey.value = null
  resetModelPanel()
}

async function refreshModels(requestedKey?: UpstreamKey): Promise<void> {
  const upstream = upstreamResource.value
  const key = requestedKey ?? selectedKey.value
  if (!upstream || !key || discoveryRunning.value) return
  if (!modelPanelOpen.value) openModels(key)
  discoveryRunning.value = true
  discoveryNotice.value = ''
  discoveryWarning.value = ''
  try {
    const task = await api.discoverModels(upstream.id, [key.id])
    discoveryNotice.value = '模型发现任务已提交'
    discoveryWarning.value = modelDiscoveryWarning(task.items.find((item) => item.key_id === key.id))
    await loadModels()
  } catch (error) {
    discoveryWarning.value = safeErrorMessage(error)
  } finally {
    discoveryRunning.value = false
  }
}

function requestProbe(model: KeyModelObservation): void {
  const key = selectedKey.value
  if (!key || probingModels.value.has(model.id)) return
  probeConfirmation.value = {
    model,
    protocol: protocolByModel[model.id] ?? 'auto',
  }
}

function probeProtocolLabel(protocol: ModelProbeProtocol): string {
  const labels: Record<ModelProbeProtocol, string> = {
    auto: '自动',
    chat_completions: 'Chat',
    responses: 'Responses',
    completions: 'Completions',
  }
  return labels[protocol]
}

async function confirmProbe(): Promise<void> {
  const pending = probeConfirmation.value
  const upstream = upstreamResource.value
  const key = selectedKey.value
  if (!pending || !upstream || !key) return
  probeConfirmation.value = null
  const model = pending.model
  if (probingModels.value.has(model.id)) return
  const inFlight = new Set(probingModels.value)
  inFlight.add(model.id)
  probingModels.value = inFlight
  modelsError.value = ''
  try {
    const result = await api.probeModel(
      upstream.id,
      key.id,
      { model: model.id, protocol: pending.protocol },
    )
    if (selectedKey.value?.id === key.id && modelSnapshot.value) {
      modelSnapshot.value = {
        ...modelSnapshot.value,
        models: modelSnapshot.value.models.map((candidate) => (
          candidate.id === model.id ? { ...candidate, probe: result } : candidate
        )),
      }
    }
  } catch (error) {
    modelsError.value = safeErrorMessage(error)
  } finally {
    const remaining = new Set(probingModels.value)
    remaining.delete(model.id)
    probingModels.value = remaining
  }
}

function probeButtonLabel(model: KeyModelObservation): string {
  if (probingModels.value.has(model.id)) return `正在测活 ${model.id}`
  return model.probe ? `重试 ${model.id}` : `测活 ${model.id}`
}

onMounted(() => {
  if (targetResource.value) {
    const status = targetResource.value.validation_status
    connectionState.value = status === 'verified' || status === 'failed' ? status : 'unverified'
    return
  }
  if (!upstreamResource.value) return
  syncKeys(upstreamResource.value.keys ?? [])
  void loadKeys()
  if (activeTab.value === 'groups') void loadGroups()
})

function setTab(tab: DetailTab): void {
  const query = { ...route.query }
  if (tab === defaultTab.value) delete query.tab
  else query.tab = tab
  void router.replace({ query })
  if (tab === 'groups' && groupsState.value === 'idle') void loadGroups()
}

function platformLabel(resource: UpstreamConfig | TargetConfig): string {
  if (resource.type === 'newapi') return props.kind === 'upstream' ? 'New API 普通用户' : 'New API'
  if (resource.type === 'generic') return '通用 OpenAI-compatible'
  return 'CPA'
}

function resetKeyForm(): void {
  editingKeyId.value = ''
  keyForm.id = ''
  keyForm.name = ''
  keyForm.apiKey = ''
  keyForm.enabled = true
  keyForm.models = ''
  keyFormModelsKnown.value = false
  keyError.value = ''
  keyDeleteArmed.value = false
}

function openAddKey(): void {
  resetKeyForm()
  keyPanelOpen.value = true
}

function openEditKey(key: UpstreamKey): void {
  resetKeyForm()
  editingKeyId.value = key.id
  keyForm.id = key.id
  keyForm.name = key.name
  keyForm.enabled = key.enabled
  keyFormModelsKnown.value = Array.isArray(key.models)
  keyForm.models = key.models?.join(', ') ?? ''
  keyPanelOpen.value = true
}

function closeKeyPanel(): void {
  if (keySaving.value || keyDeleting.value) return
  keyForm.apiKey = ''
  keyPanelOpen.value = false
  resetKeyForm()
}

function normalizedModels(): string[] | null {
  if (!keyForm.models.trim()) return []
  const models = keyForm.models.split(',').map((model) => model.trim()).filter(Boolean)
  return new Set(models).size === models.length ? models : null
}

function validateKeyForm(): string {
  if (!keyForm.id.trim() || !keyForm.name.trim()) return 'Key ID 和别名不能为空'
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(keyForm.id.trim())) return 'Key ID 格式无效'
  if (!isEditingKey.value && !keyForm.apiKey) return 'API Key 不能为空'
  if (normalizedModels() === null) return '模型列表不能包含重复项'
  return ''
}

function replaceKey(key: UpstreamKey): void {
  const current = keys.value
  const index = current.findIndex((candidate) => candidate.id === key.id)
  syncKeys(index === -1
    ? [...current, key]
    : current.map((candidate, candidateIndex) => candidateIndex === index ? key : candidate))
}

async function saveKey(): Promise<void> {
  const upstream = upstreamResource.value
  if (!upstream) return
  const validationError = validateKeyForm()
  if (validationError) {
    keyError.value = validationError
    return
  }
  const models = normalizedModels() ?? []
  keySaving.value = true
  keyError.value = ''
  try {
    let key: UpstreamKey
    if (isEditingKey.value) {
      const input: UpstreamKeyUpdateInput = {
        name: keyForm.name.trim(),
        enabled: keyForm.enabled,
      }
      if (keyFormModelsKnown.value || keyForm.models.trim()) input.models = models
      if (keyForm.apiKey) input.api_key = keyForm.apiKey
      keyForm.apiKey = ''
      key = await api.updateUpstreamKey(upstream.id, editingKeyId.value, input)
    } else {
      const apiKey = keyForm.apiKey
      keyForm.apiKey = ''
      key = await api.createUpstreamKey(upstream.id, {
        id: keyForm.id.trim(),
        name: keyForm.name.trim(),
        api_key: apiKey,
        enabled: keyForm.enabled,
        models,
      })
    }
    replaceKey(key)
    keyPanelOpen.value = false
    resetKeyForm()
  } catch (error) {
    keyError.value = safeErrorMessage(error)
  } finally {
    keySaving.value = false
  }
}

async function deleteKey(): Promise<void> {
  const upstream = upstreamResource.value
  if (!upstream || !editingKeyId.value || keyDeleting.value) return
  if (!keyDeleteArmed.value) {
    keyDeleteArmed.value = true
    return
  }
  keyDeleting.value = true
  keyError.value = ''
  try {
    await api.deleteUpstreamKey(upstream.id, editingKeyId.value)
    syncKeys(keys.value.filter((key) => key.id !== editingKeyId.value))
    keyPanelOpen.value = false
    resetKeyForm()
  } catch (error) {
    keyError.value = safeErrorMessage(error)
  } finally {
    keyDeleting.value = false
  }
}

async function validateConnection(): Promise<void> {
  const resource = configuredResource.value
  if (!resource || connectionState.value === 'testing') return
  connectionState.value = 'testing'
  connectionResult.value = null
  connectionError.value = ''
  try {
    const result = props.kind === 'target'
      ? await api.testTargetConnection(resource.id)
      : await api.testUpstreamConnection(resource.id)
    connectionResult.value = result
    const state = result.validation_status === 'failed'
      || !result.reachable
      || !result.authenticated
      || !result.authorized
      ? 'failed'
      : 'verified'
    connectionState.value = state
    if (props.kind === 'target') {
      store.setTargetValidation(resource.id, state, {
        validatedAt: result.validated_at,
        capabilities: result.capabilities,
      })
    }
  } catch (error) {
    connectionError.value = safeErrorMessage(error)
    connectionState.value = 'failed'
    if (props.kind === 'target') store.setTargetValidation(resource.id, 'failed')
  }
}

function capabilityLabel(value: string): string {
  return value
    .replace(/^can_/, '')
    .replace(/^supports_/, '')
    .split('_')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}
</script>

<template>
  <section class="page resource-page" aria-labelledby="resource-detail-heading">
    <header class="page-header resource-header">
      <div>
        <div class="resource-title-row">
          <h1 id="resource-detail-heading">{{ title }}</h1>
          <span v-if="configuredResource" class="platform-badge">{{ platformLabel(configuredResource) }}</span>
        </div>
        <p v-if="configuredResource" class="page-subtitle">{{ configuredResource.name }}</p>
      </div>
      <RouterLink class="secondary-button" :to="backTo">
        <ArrowLeft :size="16" aria-hidden="true" />
        {{ backLabel }}
      </RouterLink>
    </header>

    <section v-if="resourceMissing" class="detail-state" :aria-label="title">
      <CircleAlert :size="24" aria-hidden="true" />
      <h2>未找到对应实例</h2>
      <RouterLink class="primary-button" :to="backTo">返回列表</RouterLink>
    </section>

    <template v-else-if="configuredResource">
      <dl class="resource-strip">
        <div>
          <dt>实例 ID</dt>
          <dd><code>{{ configuredResource.id }}</code></dd>
        </div>
        <div>
          <dt>Base URL</dt>
          <dd><span :title="configuredResource.base_url">{{ configuredResource.base_url }}</span></dd>
        </div>
        <div>
          <dt>连接状态</dt>
          <dd>
            <span class="connection-state" :class="`is-${connectionState}`">
              <CheckCircle2 v-if="connectionState === 'verified'" :size="14" aria-hidden="true" />
              <CircleAlert v-else-if="connectionState === 'failed'" :size="14" aria-hidden="true" />
              <span v-else class="state-dot" aria-hidden="true" />
              {{ connectionState === 'verified' ? '已验证' : connectionState === 'failed' ? '验证失败' : connectionState === 'testing' ? '验证中' : '未验证' }}
            </span>
          </dd>
        </div>
      </dl>

      <nav
        v-if="kind === 'upstream'"
        class="detail-tabs"
        role="tablist"
        aria-label="上游详情分区"
      >
        <button
          id="upstream-keys-tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'keys'"
          aria-controls="upstream-keys-panel"
          @click="setTab('keys')"
        >
          Key 与模型
        </button>
        <button
          v-if="upstreamResource?.type === 'newapi'"
          id="upstream-groups-tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'groups'"
          aria-controls="upstream-groups-panel"
          @click="setTab('groups')"
        >
          New API 分组
        </button>
        <button
          id="upstream-settings-tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'settings'"
          aria-controls="upstream-settings-panel"
          @click="setTab('settings')"
        >
          连接设置
        </button>
      </nav>

      <nav v-else class="detail-tabs" role="tablist" aria-label="目标详情分区">
        <button
          id="target-overview-tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'overview'"
          aria-controls="target-overview-panel"
          @click="setTab('overview')"
        >
          概览
        </button>
        <RouterLink
          role="tab"
          aria-selected="false"
          :to="{ name: 'target-channels', params: { id: resourceId } }"
        >
          渠道
        </RouterLink>
        <button
          id="target-settings-tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'settings'"
          aria-controls="target-settings-panel"
          @click="setTab('settings')"
        >
          设置
        </button>
      </nav>

      <section
        v-if="kind === 'upstream' && activeTab === 'keys'"
        id="upstream-keys-panel"
        class="detail-panel"
        role="tabpanel"
        aria-labelledby="upstream-keys-tab"
      >
        <header class="panel-toolbar">
          <div>
            <h2>Key 与模型</h2>
            <span v-if="keysState === 'loading'">正在加载</span>
            <span v-else-if="keysState === 'error'">加载失败</span>
            <span v-else>{{ displayedKeys.length }} 个 Key</span>
          </div>
          <div class="panel-actions">
            <button
              class="icon-button icon-button-small"
              type="button"
              aria-label="重新加载 Key 列表"
              title="重新加载 Key 列表"
              :disabled="keysState === 'loading'"
              @click="loadKeys"
            >
              <RefreshCw :class="{ spin: keysState === 'loading' }" :size="15" aria-hidden="true" />
            </button>
            <button
              v-if="upstreamResource?.type === 'generic'"
              class="secondary-button"
              type="button"
              aria-label="添加 Key"
              @click="openAddKey"
            >
              <Plus :size="15" aria-hidden="true" />
              添加 Key
            </button>
            <RouterLink class="primary-button" :to="{ name: 'sync' }" aria-label="进入同步工作台">
              进入同步工作台
              <ArrowRight :size="15" aria-hidden="true" />
            </RouterLink>
          </div>
        </header>

        <div
          v-if="keysState === 'loading'"
          class="key-load-state"
          role="status"
          aria-label="正在加载 Key 列表"
        >
          <span class="spinner spinner-small" aria-hidden="true"></span>
          正在加载 Key 列表
        </div>
        <div v-else-if="keysState === 'error'" class="key-load-state is-error" role="alert">
          <span>{{ keysError }}</span>
          <button class="secondary-button" type="button" aria-label="重试 Key 列表" @click="loadKeys">
            <RotateCcw :size="15" aria-hidden="true" />
            重试
          </button>
        </div>

        <div v-if="keysState === 'ready' && !displayedKeys.length" class="detail-empty">
          <KeyRound :size="22" aria-hidden="true" />
          <p>{{ upstreamResource?.type === 'newapi' ? '暂无已发现的用户 Key' : '尚未配置通用 Key' }}</p>
        </div>
        <div v-else-if="displayedKeys.length" class="detail-table-wrap">
          <table class="key-table">
            <thead>
              <tr>
                <th scope="col">Key</th>
                <th scope="col">启用</th>
                <th scope="col">模型</th>
                <th scope="col">发现状态</th>
                <th scope="col">凭证</th>
                <th scope="col"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="key in displayedKeys" :key="key.id">
                <td data-label="Key">
                  <div class="key-name">
                    <strong>{{ key.name }}</strong>
                    <code>{{ key.id }}</code>
                  </div>
                </td>
                <td data-label="启用">
                  <span class="compact-status" :class="key.enabled ? 'is-on' : 'is-off'">
                    {{ key.enabled ? '启用' : '停用' }}
                  </span>
                </td>
                <td data-label="模型"><strong>{{ keyModelCount(key) }} 个模型</strong></td>
                <td data-label="发现状态">
                  <span class="compact-status" :class="keyDiscoveryClass(key)">{{ keyDiscoveryLabel(key) }}</span>
                </td>
                <td data-label="凭证">{{ key.credential_present ? '凭证已配置' : '凭证缺失' }}</td>
                <td data-label="操作">
                  <div class="key-actions">
                    <button
                      class="icon-button icon-button-small"
                      type="button"
                      :aria-label="`刷新 ${key.name} 模型`"
                      title="刷新模型"
                      :disabled="discoveryRunning"
                      @click="refreshModels(key)"
                    >
                      <RefreshCw :size="15" aria-hidden="true" />
                    </button>
                    <button
                      class="icon-button icon-button-small"
                      type="button"
                      :aria-label="`查看 ${key.name} 模型`"
                      title="查看模型"
                      @click="openModels(key)"
                    >
                      <Eye :size="15" aria-hidden="true" />
                    </button>
                    <button
                      v-if="upstreamResource?.type === 'generic'"
                      class="icon-button icon-button-small"
                      type="button"
                      :aria-label="`编辑 Key ${key.name}`"
                      title="编辑 Key"
                      @click="openEditKey(key)"
                    >
                      <Pencil :size="15" aria-hidden="true" />
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section
        v-else-if="kind === 'upstream' && activeTab === 'groups'"
        id="upstream-groups-panel"
        class="detail-panel"
        role="tabpanel"
        aria-labelledby="upstream-groups-tab"
      >
        <header class="panel-toolbar">
          <div>
            <h2>New API 分组</h2>
            <span v-if="groupsState === 'loading'">正在加载</span>
            <span v-else-if="groupsState === 'error'">加载失败</span>
            <span v-else>{{ groupsSnapshot?.groups.length ?? 0 }} 个分组</span>
          </div>
          <button
            class="secondary-button"
            type="button"
            :disabled="groupsState === 'loading'"
            @click="refreshGroups"
          >
            <RefreshCw :class="{ spin: groupsState === 'loading' }" :size="15" aria-hidden="true" />
            刷新来源快照
          </button>
        </header>
        <div v-if="groupsState === 'loading'" class="key-load-state" role="status" aria-label="正在加载分组快照">
          <span class="spinner spinner-small" aria-hidden="true"></span>
          正在加载分组快照
        </div>
        <div v-else-if="groupsState === 'error'" class="key-load-state is-error" role="alert">
          <span>{{ groupsError }}</span>
          <button class="secondary-button" type="button" aria-label="重试分组快照" @click="loadGroups">
            <RotateCcw :size="15" aria-hidden="true" />
            重试
          </button>
        </div>
        <div v-else-if="groupsState === 'ready' && !groupsSnapshot?.refreshed" class="detail-empty">
          <CircleAlert :size="22" aria-hidden="true" />
          <p>尚未生成完整分组快照</p>
          <button class="primary-button" type="button" @click="refreshGroups">刷新来源快照</button>
        </div>
        <div v-else-if="groupsState === 'ready' && !groupsSnapshot?.groups.length" class="detail-empty">
          <CircleAlert :size="22" aria-hidden="true" />
          <p>当前用户没有可用分组</p>
        </div>
        <div v-else-if="groupsSnapshot" class="detail-table-wrap">
          <table class="group-table" aria-label="New API 分组列表">
            <thead>
              <tr>
                <th scope="col">分组</th>
                <th scope="col">说明</th>
                <th scope="col">倍率</th>
                <th scope="col">模型</th>
                <th scope="col">状态</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="group in groupsSnapshot.groups" :key="group.name">
                <td data-label="分组">
                  <div class="key-name">
                    <strong>{{ group.name }}</strong>
                    <small v-if="group.auto">自动分组</small>
                  </div>
                </td>
                <td data-label="说明">{{ group.description || '--' }}</td>
                <td data-label="倍率"><strong>{{ groupRatioLabel(group.ratio, group.ratio_known) }}</strong></td>
                <td data-label="模型">
                  <span :title="group.models.join(', ')">{{ group.model_count }} 个模型</span>
                </td>
                <td data-label="状态">
                  <span class="compact-status" :class="group.models_verified ? 'is-on' : 'is-unverified'">
                    {{ group.models_verified ? '已确证' : '未确证' }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section
        v-else-if="kind === 'upstream' && activeTab === 'settings'"
        id="upstream-settings-panel"
        class="detail-panel settings-panel"
        role="tabpanel"
        aria-labelledby="upstream-settings-tab"
      >
        <header class="panel-toolbar">
          <div>
            <h2>连接设置</h2>
            <span>{{ platformLabel(configuredResource) }}</span>
          </div>
          <button class="secondary-button" type="button" :disabled="connectionState === 'testing'" @click="validateConnection">
            <RefreshCw :class="{ spin: connectionState === 'testing' }" :size="15" aria-hidden="true" />
            验证上游连接
          </button>
        </header>
        <dl class="settings-list">
          <div><dt>连接 ID</dt><dd><code>{{ configuredResource.id }}</code></dd></div>
          <div><dt>接入预设</dt><dd>{{ platformLabel(configuredResource) }}</dd></div>
          <div><dt>Base URL</dt><dd>{{ configuredResource.base_url }}</dd></div>
          <div v-if="upstreamResource?.user_id"><dt>用户 ID</dt><dd>{{ upstreamResource.user_id }}</dd></div>
        </dl>
        <p v-if="connectionError" class="inline-error" role="alert">{{ connectionError }}</p>
      </section>

      <section
        v-else-if="kind === 'target' && activeTab === 'overview'"
        id="target-overview-panel"
        class="detail-panel overview-panel"
        role="tabpanel"
        aria-labelledby="target-overview-tab"
      >
        <div class="overview-status">
          <div class="overview-status-icon" :class="`is-${connectionState}`">
            <CheckCircle2 v-if="connectionState === 'verified'" :size="20" aria-hidden="true" />
            <CircleAlert v-else-if="connectionState === 'failed'" :size="20" aria-hidden="true" />
            <Settings2 v-else :size="20" aria-hidden="true" />
          </div>
          <div>
            <strong>{{ connectionState === 'verified' ? '连接验证通过' : connectionState === 'failed' ? '连接验证失败' : connectionState === 'testing' ? '正在验证连接' : '连接尚未验证' }}</strong>
            <span v-if="connectionResult">{{ connectionResult.resource_count }} 个渠道</span>
            <span v-if="targetValidatedAt">{{ targetValidatedAt }}</span>
            <span v-else-if="connectionError" class="error-text">{{ connectionError }}</span>
            <span v-else-if="!connectionResult">管理员凭证</span>
          </div>
          <button class="secondary-button" type="button" :disabled="connectionState === 'testing'" aria-label="验证目标连接" @click="validateConnection">
            <RefreshCw :class="{ spin: connectionState === 'testing' }" :size="15" aria-hidden="true" />
            {{ connectionState === 'testing' ? '验证中' : '验证目标连接' }}
          </button>
        </div>

        <div v-if="targetCapabilities" class="capability-grid" role="region" aria-label="目标能力">
          <div v-if="connectionResult">
            <span>地址可达</span>
            <strong>{{ connectionResult.reachable ? '通过' : '未通过' }}</strong>
          </div>
          <div v-if="connectionResult">
            <span>凭证有效</span>
            <strong>{{ connectionResult.authenticated ? '通过' : '未通过' }}</strong>
          </div>
          <div v-if="connectionResult">
            <span>管理权限</span>
            <strong>{{ connectionResult.authorized ? '通过' : '未通过' }}</strong>
          </div>
          <div v-if="targetCapabilityPlatform">
            <span>平台</span>
            <strong>{{ targetCapabilityPlatform }}</strong>
          </div>
          <div v-if="targetProviders.length">
            <span>Providers</span>
            <strong>{{ targetProviders.length }} 个 provider</strong>
          </div>
          <div v-if="targetProviderModes.length">
            <span>模式</span>
            <strong class="capability-modes">
              <code v-for="mode in targetProviderModes" :key="mode">{{ mode }}</code>
            </strong>
          </div>
          <div v-for="([capability, value]) in simpleTargetCapabilities" :key="capability">
            <span>{{ capabilityLabel(String(capability)) }}</span>
            <strong>{{ value === true ? '支持' : value === false ? '不支持' : value }}</strong>
          </div>
        </div>

        <div class="overview-entry">
          <div>
            <strong>目标渠道</strong>
            <span>{{ connectionResult ? `${connectionResult.resource_count} 个实时渠道` : '读取目标实例实时渠道' }}</span>
          </div>
          <RouterLink class="primary-button" :to="{ name: 'target-channels', params: { id: resourceId } }">
            查看渠道
            <ArrowRight :size="15" aria-hidden="true" />
          </RouterLink>
        </div>
      </section>

      <section
        v-else
        id="target-settings-panel"
        class="detail-panel settings-panel"
        role="tabpanel"
        aria-labelledby="target-settings-tab"
      >
        <header class="panel-toolbar">
          <div>
            <h2>实例设置</h2>
            <span>管理员目标</span>
          </div>
          <button class="secondary-button" type="button" :disabled="connectionState === 'testing'" @click="validateConnection">
            <RefreshCw :class="{ spin: connectionState === 'testing' }" :size="15" aria-hidden="true" />
            重新验证
          </button>
        </header>
        <dl class="settings-list">
          <div><dt>实例 ID</dt><dd><code>{{ configuredResource.id }}</code></dd></div>
          <div><dt>平台</dt><dd>{{ platformLabel(configuredResource) }}</dd></div>
          <div><dt>Base URL</dt><dd>{{ configuredResource.base_url }}</dd></div>
          <div v-if="targetResource?.user_id"><dt>用户 ID</dt><dd>{{ targetResource.user_id }}</dd></div>
        </dl>
        <p v-if="connectionError" class="inline-error" role="alert">{{ connectionError }}</p>
      </section>
    </template>

    <section v-else class="legacy-detail" :aria-label="title">
      <dl class="legacy-summary">
        <div>
          <dt>{{ resourceTypeLabel }}</dt>
          <dd><code>{{ resourceId }}</code></dd>
        </div>
      </dl>
    </section>

    <SidePanel
      v-if="keyPanelOpen"
      :title="isEditingKey ? '编辑通用 Key' : '添加通用 Key'"
      :close-label="isEditingKey ? '关闭编辑通用 Key' : '关闭添加通用 Key'"
      width="narrow"
      @close="closeKeyPanel"
    >
      <form class="key-form" @submit.prevent="saveKey">
        <label>
          <span>Key ID</span>
          <input v-model="keyForm.id" type="text" autocomplete="off" :disabled="isEditingKey" />
        </label>
        <label>
          <span>Key 别名</span>
          <input v-model="keyForm.name" type="text" autocomplete="off" />
        </label>
        <label>
          <span>API Key</span>
          <input
            v-model="keyForm.apiKey"
            type="password"
            autocomplete="new-password"
            autocapitalize="off"
            spellcheck="false"
          />
        </label>
        <label>
          <span>模型</span>
          <input v-model="keyForm.models" type="text" autocomplete="off" placeholder="model-a, model-b" />
        </label>
        <label class="switch-row">
          <input v-model="keyForm.enabled" type="checkbox" />
          <span>启用 Key</span>
        </label>
        <p v-if="keyError" class="inline-error" role="alert">{{ keyError }}</p>
        <section v-if="isEditingKey" class="key-danger">
          <strong>删除 Key</strong>
          <button class="danger-button" type="button" :disabled="keyDeleting" @click="deleteKey">
            <Trash2 :size="15" aria-hidden="true" />
            {{ keyDeleting ? '删除中' : keyDeleteArmed ? '确认删除 Key' : '删除 Key' }}
          </button>
        </section>
        <footer>
          <button class="secondary-button" type="button" :disabled="keySaving || keyDeleting" @click="closeKeyPanel">取消</button>
          <button class="primary-button" type="submit" :disabled="keySaving || keyDeleting">
            {{ keySaving ? '保存中' : '保存 Key' }}
          </button>
        </footer>
      </form>
    </SidePanel>

    <ModalDialog
      v-if="modelPanelOpen && selectedKey"
      :title="`${selectedKey.name} 模型`"
      :close-label="`关闭 ${selectedKey.name} 模型`"
      size="wide"
      @close="closeModels"
    >
      <div class="models-panel">
        <header class="models-command-bar">
          <div class="snapshot-meta">
            <strong>{{ selectedKey.name }}</strong>
            <code>{{ selectedKey.id }}</code>
            <span v-if="modelSnapshot?.snapshot_scope === 'runtime'" class="scope-badge">本次运行</span>
            <span v-else-if="modelSnapshot?.snapshot_scope === 'persisted'" class="scope-badge is-persisted">已保存</span>
          </div>
          <button
            class="secondary-button"
            type="button"
            :disabled="discoveryRunning || probingModels.size > 0"
            aria-label="刷新模型"
            @click="refreshModels()"
          >
            <RefreshCw :class="{ spin: discoveryRunning }" :size="15" aria-hidden="true" />
            {{ discoveryRunning ? '刷新中' : '刷新模型' }}
          </button>
        </header>

        <div class="probe-cost-notice" role="note">
          <FlaskConical :size="17" aria-hidden="true" />
          <div>
            <strong>本次请求可能产生真实费用</strong>
            <span>输入约 20-50 Token / 输出最多 64 Token</span>
          </div>
        </div>

        <p v-if="discoveryNotice" class="model-notice" role="status">{{ discoveryNotice }}</p>
        <p v-if="discoveryWarning" class="model-warning" role="alert">{{ discoveryWarning }}</p>
        <p v-if="modelsError" class="model-warning" role="alert">{{ modelsError }}</p>

        <div class="models-filter-bar">
          <label class="model-search">
            <Search :size="15" aria-hidden="true" />
            <span class="sr-only">搜索当前 Key 的模型</span>
            <input
              v-model="modelQuery"
              type="search"
              aria-label="搜索当前 Key 的模型"
              placeholder="搜索模型"
              autocomplete="off"
            />
          </label>
          <label class="model-filter">
            <span>测活状态</span>
            <select v-model="probeFilter" aria-label="测活状态">
              <option value="all">全部状态</option>
              <option value="untested">未测试</option>
              <option value="healthy">健康</option>
              <option value="reachable_inconclusive">可达未确认</option>
              <option value="authentication_failed">鉴权失败</option>
              <option value="model_unavailable">模型不可用</option>
              <option value="rate_limited">请求受限</option>
              <option value="timeout">超时</option>
              <option value="network_error">网络错误</option>
              <option value="invalid_response">响应无效</option>
              <option value="unsupported">协议不支持</option>
            </select>
          </label>
          <label class="model-filter">
            <span>模型发现状态</span>
            <select v-model="discoveryFilter" aria-label="模型发现状态">
              <option value="all">全部发现状态</option>
              <option value="discovered">已发现</option>
              <option value="unverified">未验证</option>
            </select>
          </label>
          <label class="model-filter">
            <span>排序</span>
            <select v-model="modelSort" aria-label="模型排序">
              <option value="name_asc">模型名称 A-Z</option>
              <option value="name_desc">模型名称 Z-A</option>
              <option value="discovery">发现状态</option>
              <option value="probe">测活状态</option>
            </select>
          </label>
        </div>

        <div
          v-if="modelsState === 'loading' && !modelSnapshot"
          class="model-state"
          role="status"
          aria-label="正在加载模型列表"
        >
          <span class="spinner" aria-hidden="true"></span>
          正在加载模型列表
        </div>
        <div v-else-if="modelsState === 'error' && !modelSnapshot" class="model-state is-error">
          <button class="secondary-button" type="button" @click="loadModels">
            <RotateCcw :size="15" aria-hidden="true" />
            重试模型列表
          </button>
        </div>
        <div v-else-if="modelSnapshot && modelSnapshot.models.length === 0" class="model-state">
          <strong>当前 Key 没有模型</strong>
        </div>
        <div v-else-if="modelSnapshot && filteredModels.length === 0" class="model-state">
          <strong>没有匹配的模型</strong>
        </div>
        <div v-else-if="modelSnapshot" class="model-table-wrap">
          <table class="model-table">
            <thead>
              <tr>
                <th scope="col">模型</th>
                <th scope="col">发现</th>
                <th scope="col">测活</th>
                <th scope="col">协议</th>
                <th scope="col"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="model in filteredModels" :key="model.id">
                <td data-label="模型"><strong>{{ model.id }}</strong></td>
                <td data-label="发现">
                  <span class="compact-status" :class="model.discovery_status === 'discovered' ? 'is-on' : 'is-unverified'">
                    {{ model.discovery_status === 'discovered' ? '已发现' : '未验证' }}
                  </span>
                </td>
                <td data-label="测活">
                  <div class="probe-result">
                    <strong :class="model.probe ? `is-${model.probe.status}` : ''">
                      {{ modelProbeLabel(model.probe?.status) }}
                    </strong>
                    <span v-if="model.probe">{{ model.probe.latency_ms }} ms</span>
                    <span v-if="model.probe" class="probe-metadata">
                      <span>checked_at</span>
                      <code>{{ model.probe.checked_at }}</code>
                    </span>
                    <span v-if="model.probe" class="probe-metadata">
                      <span>error_code</span>
                      <code>{{ model.probe.error_code || '无' }}</code>
                    </span>
                    <span v-if="model.probe" class="probe-metadata">
                      <span>template_version</span>
                      <code>模板 {{ model.probe.template_version }}</code>
                    </span>
                    <span v-if="model.probe?.retry_after_seconds !== undefined" class="probe-metadata">
                      <span>retry_after</span>
                      <code>{{ model.probe.retry_after_seconds }} 秒后可重试</code>
                    </span>
                  </div>
                </td>
                <td data-label="协议">
                  <select
                    v-model="protocolByModel[model.id]"
                    :aria-label="`${model.id} 测试协议`"
                    :disabled="probingModels.has(model.id)"
                  >
                    <option value="auto">自动</option>
                    <option value="chat_completions">Chat</option>
                    <option value="responses">Responses</option>
                    <option value="completions">Completions</option>
                  </select>
                </td>
                <td data-label="操作">
                  <button
                    class="secondary-button model-probe-button"
                    type="button"
                    :disabled="probingModels.has(model.id) || discoveryRunning"
                    :aria-label="probeButtonLabel(model)"
                    @click="requestProbe(model)"
                  >
                    <FlaskConical :size="14" aria-hidden="true" />
                    {{ probingModels.has(model.id) ? '测活中' : model.probe ? '重试' : '测活' }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </ModalDialog>

    <ModalDialog
      v-if="probeConfirmation && selectedKey && upstreamResource"
      title="确认模型测活"
      close-label="取消模型测活"
      @close="probeConfirmation = null"
    >
      <div class="probe-confirmation">
        <p class="probe-confirmation-lead">这是一次可能计费的手动请求，请确认调用范围。</p>
        <dl class="probe-confirmation-details">
          <div>
            <dt>当前上游</dt>
            <dd>{{ upstreamResource.name }}<code>{{ upstreamResource.base_url }}</code></dd>
          </div>
          <div>
            <dt>Key</dt>
            <dd>{{ selectedKey.name }}<code>{{ selectedKey.id }}</code></dd>
          </div>
          <div>
            <dt>模型</dt>
            <dd><code>{{ probeConfirmation.model.id }}</code></dd>
          </div>
          <div>
            <dt>协议</dt>
            <dd>{{ probeProtocolLabel(probeConfirmation.protocol) }}</dd>
          </div>
        </dl>
        <div class="probe-cost-notice" role="note">
          <FlaskConical :size="17" aria-hidden="true" />
          <div>
            <strong>本次请求可能产生真实费用</strong>
            <span>服务端将生成随机自然语言任务；输入约 20-50 Token / 输出最多 64 Token。</span>
          </div>
        </div>
        <footer class="probe-confirmation-actions">
          <button class="secondary-button" type="button" @click="probeConfirmation = null">取消</button>
          <button class="primary-button" type="button" @click="confirmProbe">确认测活</button>
        </footer>
      </div>
    </ModalDialog>
  </section>
</template>

<style scoped>
.resource-header {
  align-items: flex-start;
}

.resource-title-row {
  display: flex;
  align-items: center;
  gap: 9px;
}

.platform-badge,
.connection-state,
.compact-status {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 5px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 650;
}

.platform-badge {
  padding: 3px 7px;
  color: #3f3f46;
  background: #f4f4f5;
}

.page-subtitle {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
}

.resource-strip {
  display: grid;
  margin: 0;
  padding: 11px 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  grid-template-columns: minmax(140px, 0.8fr) minmax(220px, 2fr) minmax(130px, 0.8fr);
  gap: 18px;
}

.resource-strip > div,
.settings-list > div,
.legacy-summary > div {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.resource-strip dt,
.settings-list dt,
.legacy-summary dt {
  color: var(--muted);
  font-size: 10px;
  font-weight: 700;
}

.resource-strip dd,
.settings-list dd,
.legacy-summary dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--ink);
  font-size: 12px;
}

.resource-strip dd > span:not(.connection-state) {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-state {
  padding: 3px 7px;
  color: #52525b;
  background: #f4f4f5;
}

.connection-state.is-verified { color: #047857; background: var(--green-soft); }
.connection-state.is-failed { color: #b91c1c; background: var(--red-soft); }
.connection-state.is-testing { color: #1d4ed8; background: var(--blue-soft); }

.state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #a1a1aa;
}

.detail-tabs {
  display: flex;
  min-height: 44px;
  align-items: flex-end;
  gap: 22px;
  margin-top: 12px;
  padding: 0 4px;
  border-bottom: 1px solid var(--line);
}

.detail-tabs button,
.detail-tabs a {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  border: 0;
  border-bottom: 2px solid transparent;
  padding: 0 2px;
  color: var(--muted);
  background: transparent;
  font-size: 12px;
  font-weight: 650;
  text-decoration: none;
}

.detail-tabs button[aria-selected="true"] {
  border-bottom-color: var(--blue);
  color: var(--blue);
}

.detail-tabs button:hover,
.detail-tabs a:hover {
  color: var(--ink);
}

.detail-panel,
.legacy-detail,
.detail-state {
  min-height: 300px;
  border: 1px solid var(--line);
  border-top: 0;
  border-radius: 0 0 8px 8px;
  background: var(--surface);
}

.panel-toolbar {
  display: flex;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--line);
}

.panel-toolbar > div:first-child {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.panel-toolbar h2 {
  margin: 0;
  font-size: 14px;
}

.panel-toolbar span {
  color: var(--muted);
  font-size: 11px;
}

.panel-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.detail-table-wrap { overflow-x: auto; }

.key-table,
.group-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.key-table th,
.group-table th {
  height: 36px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
  color: #52525b;
  background: var(--surface-subtle);
  font-size: 11px;
  text-align: left;
}

.key-table th:nth-child(1) { width: 25%; }
.key-table th:nth-child(2) { width: 12%; }
.key-table th:nth-child(3) { width: 16%; }
.key-table th:nth-child(4) { width: 17%; }
.key-table th:nth-child(5) { width: 20%; }
.key-table th:nth-child(6) { width: 128px; }

.key-table td,
.group-table td {
  height: 62px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 12px;
}

.key-table tbody tr:last-child td { border-bottom: 0; }

.group-table th:nth-child(1) { width: 20%; }
.group-table th:nth-child(2) { width: 30%; }
.group-table th:nth-child(3) { width: 12%; }
.group-table th:nth-child(4) { width: 20%; }
.group-table th:nth-child(5) { width: 18%; }
.group-table tbody tr:last-child td { border-bottom: 0; }

.key-name {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.key-name strong { overflow-wrap: anywhere; color: var(--ink); }
.key-name code { color: var(--muted); font-size: 10px; }

.key-actions {
  display: flex;
  min-width: 116px;
  justify-content: flex-end;
  gap: 4px;
}

.key-load-state {
  display: flex;
  min-height: 46px;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: 12px;
}

.key-load-state.is-error {
  justify-content: space-between;
  color: #b91c1c;
  background: var(--red-soft);
}

.compact-status { padding: 3px 7px; }
.compact-status.is-on { color: #047857; background: var(--green-soft); }
.compact-status.is-off { color: #52525b; background: #f4f4f5; }
.compact-status.is-unverified { color: #92400e; background: var(--amber-soft); }
.compact-status.is-error { color: #b91c1c; background: var(--red-soft); }

.detail-empty,
.detail-state {
  display: flex;
  min-height: 300px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 24px;
  color: var(--muted);
  text-align: center;
}

.detail-empty p { margin: 0; font-size: 13px; }
.detail-state { border-top: 1px solid var(--line); border-radius: 8px; }
.detail-state h2 { margin: 0; color: var(--ink); font-size: 15px; }

.settings-panel { padding-bottom: 16px; }

.settings-list {
  display: grid;
  margin: 0;
  padding: 6px 14px;
}

.settings-list > div {
  grid-template-columns: 140px minmax(0, 1fr);
  align-items: center;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
  gap: 14px;
}

.settings-list > div:last-child { border-bottom: 0; }

.overview-panel { padding: 14px; }

.overview-status,
.overview-entry {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 0;
  border-bottom: 1px solid var(--line);
}

.overview-status-icon {
  display: inline-grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  place-items: center;
  border-radius: 8px;
  color: #52525b;
  background: #f4f4f5;
}

.overview-status-icon.is-verified { color: #047857; background: var(--green-soft); }
.overview-status-icon.is-failed { color: #b91c1c; background: var(--red-soft); }

.overview-status > div:nth-child(2),
.overview-entry > div {
  display: grid;
  flex: 1 1 auto;
  gap: 3px;
}

.overview-status strong,
.overview-entry strong { color: var(--ink); font-size: 13px; }
.overview-status span,
.overview-entry span { color: var(--muted); font-size: 11px; }

.capability-grid {
  display: grid;
  padding: 14px 0;
  border-bottom: 1px solid var(--line);
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  background: var(--line);
}

.capability-grid > div {
  display: grid;
  min-height: 56px;
  align-content: center;
  gap: 4px;
  padding: 8px 12px;
  background: var(--surface);
}

.capability-grid span { color: var(--muted); font-size: 10px; }
.capability-grid strong { color: var(--ink); font-size: 12px; }
.overview-entry { border-bottom: 0; }

.inline-error {
  margin: 10px 14px 0;
  padding: 9px 10px;
  border: 1px solid #fecaca;
  border-radius: 6px;
  color: #b91c1c;
  background: var(--red-soft);
  font-size: 12px;
}

.error-text { color: #b91c1c !important; }
.spin { animation: spin 900ms linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.legacy-detail {
  border-top: 1px solid var(--line);
  border-radius: 8px;
  padding: 16px;
}

.legacy-summary { margin: 0; }

.key-form {
  display: grid;
  min-height: 100%;
  align-content: start;
  gap: 13px;
}

.key-form > label:not(.switch-row) {
  display: grid;
  gap: 6px;
}

.key-form label > span,
.key-danger strong {
  color: #3f3f46;
  font-size: 12px;
  font-weight: 700;
}

.key-form input[type="text"],
.key-form input[type="password"] {
  width: 100%;
  min-height: 36px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  padding: 7px 9px;
  color: var(--ink);
  background: var(--surface);
}

.switch-row {
  display: flex;
  min-height: 40px;
  align-items: center;
  gap: 8px;
}

.key-danger,
.key-form footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.key-danger {
  padding: 12px;
  border: 1px solid #fecaca;
  border-radius: 7px;
  background: #fffafa;
}

.key-form footer {
  position: sticky;
  bottom: -18px;
  justify-content: flex-end;
  margin: auto -18px -18px;
  padding: 12px 18px;
  border-top: 1px solid var(--line);
  background: var(--surface);
}

.models-panel {
  display: grid;
  min-height: 100%;
  align-content: start;
  gap: 12px;
}

.models-command-bar,
.models-filter-bar {
  display: flex;
  min-height: 44px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.snapshot-meta {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}

.snapshot-meta strong {
  overflow-wrap: anywhere;
  color: var(--ink);
  font-size: 13px;
}

.snapshot-meta code {
  color: var(--muted);
  font-size: 10px;
}

.scope-badge {
  flex: 0 0 auto;
  padding: 3px 6px;
  border-radius: 999px;
  color: #92400e;
  background: var(--amber-soft);
  font-size: 10px;
  font-weight: 700;
}

.scope-badge.is-persisted {
  color: #047857;
  background: var(--green-soft);
}

.probe-cost-notice {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 10px;
  border: 1px solid #fde68a;
  border-radius: 6px;
  color: #92400e;
  background: #fffbeb;
}

.probe-cost-notice > div {
  display: grid;
  gap: 2px;
}

.probe-cost-notice strong { font-size: 12px; }
.probe-cost-notice span { font-size: 10px; }

.model-notice,
.model-warning {
  margin: 0;
  padding: 8px 10px;
  border-radius: 6px;
  font-size: 11px;
}

.model-notice {
  color: #047857;
  background: var(--green-soft);
}

.model-warning {
  color: #b91c1c;
  background: var(--red-soft);
}

.model-search {
  display: flex;
  min-width: 0;
  flex: 1 1 240px;
  align-items: center;
  gap: 7px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  padding: 0 9px;
  color: var(--muted);
  background: var(--surface);
}

.model-search input {
  width: 100%;
  min-width: 0;
  min-height: 36px;
  border: 0;
  outline: 0;
  color: var(--ink);
  background: transparent;
}

.model-filter {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  color: var(--muted);
  font-size: 11px;
}

.model-filter select,
.model-table select {
  min-height: 36px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  padding: 0 8px;
  color: var(--ink);
  background: var(--surface);
}

.model-state {
  display: grid;
  min-height: 180px;
  place-items: center;
  align-content: center;
  gap: 9px;
  color: var(--muted);
  font-size: 12px;
}

.model-state.is-error { color: #b91c1c; }

.model-table-wrap {
  border: 1px solid var(--line);
  border-radius: 6px;
  overflow: hidden;
}

.model-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.model-table th {
  height: 36px;
  padding: 0 9px;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  background: var(--surface-subtle);
  font-size: 10px;
  text-align: left;
}

.model-table th:nth-child(1) { width: 28%; }
.model-table th:nth-child(2) { width: 15%; }
.model-table th:nth-child(3) { width: 20%; }
.model-table th:nth-child(4) { width: 20%; }
.model-table th:nth-child(5) { width: 94px; }

.model-table td {
  min-width: 0;
  height: 58px;
  padding: 8px 9px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 11px;
}

.model-table tr:last-child td { border-bottom: 0; }
.model-table td > strong { overflow-wrap: anywhere; color: var(--ink); }

.probe-result {
  display: grid;
  gap: 2px;
}

.probe-result strong { color: var(--muted); font-size: 11px; }
.probe-result strong.is-healthy { color: #047857; }
.probe-result strong.is-rate_limited,
.probe-result strong.is-timeout { color: #92400e; }
.probe-result strong.is-authentication_failed,
.probe-result strong.is-model_unavailable,
.probe-result strong.is-invalid_response { color: #b91c1c; }
.probe-result span { color: var(--muted); font-size: 10px; }

.probe-metadata {
  display: flex;
  flex-wrap: wrap;
  gap: 3px 5px;
  line-height: 1.25;
}

.probe-metadata span { color: var(--muted); }
.probe-metadata code { overflow-wrap: anywhere; color: var(--muted); font-size: 10px; }

.probe-confirmation {
  display: grid;
  gap: 14px;
}

.probe-confirmation-lead { margin: 0; color: var(--ink); font-size: 13px; }

.probe-confirmation-details {
  display: grid;
  gap: 8px;
  margin: 0;
}

.probe-confirmation-details > div {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 8px;
  align-items: baseline;
}

.probe-confirmation-details dt { color: var(--muted); font-size: 11px; }
.probe-confirmation-details dd { display: grid; gap: 2px; min-width: 0; margin: 0; color: var(--ink); font-size: 12px; }
.probe-confirmation-details code { overflow-wrap: anywhere; color: var(--muted); font-size: 10px; }

.probe-confirmation-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.model-probe-button {
  min-width: 72px;
  padding-right: 8px;
  padding-left: 8px;
}

@media (max-width: 720px) {
  .resource-header { align-items: flex-start; }
  .resource-strip { grid-template-columns: 1fr; gap: 10px; }
  .detail-tabs { gap: 16px; overflow-x: auto; }
  .panel-toolbar { align-items: flex-start; }
  .panel-actions { flex-wrap: wrap; justify-content: flex-end; }
  .detail-table-wrap { overflow: visible; }
  .key-table,
  .key-table tbody { display: block; }
  .key-table thead { display: none; }
  .key-table tr {
    display: grid;
    grid-template-columns: 1fr auto;
    padding: 12px;
    border-bottom: 1px solid var(--line);
    gap: 8px 12px;
  }
  .key-table td { display: block; height: auto; padding: 0; border: 0; }
  .key-table td:nth-child(1),
  .key-table td:nth-child(3),
  .key-table td:nth-child(4),
  .key-table td:nth-child(5) { grid-column: 1; }
  .key-table td:nth-child(2),
  .key-table td:nth-child(6) { grid-column: 2; }
  .key-table td:nth-child(2) { grid-row: 1; }
  .key-table td:nth-child(6) { grid-row: 2 / span 3; }
  .key-actions { min-width: 40px; flex-direction: column; }
  .overview-status,
  .overview-entry { align-items: flex-start; flex-wrap: wrap; }
  .overview-status > button,
  .overview-entry > a { margin-left: 52px; }
  .capability-grid { grid-template-columns: 1fr 1fr; }
  .settings-list > div { grid-template-columns: 100px minmax(0, 1fr); }

  .models-command-bar,
  .models-filter-bar {
    min-height: 44px;
  }

  .models-command-bar {
    align-items: center;
  }

  .models-command-bar .secondary-button {
    min-height: 44px;
    flex: 0 0 auto;
  }

  .models-filter-bar {
    align-items: center;
    flex-direction: row;
  }

  .model-filter {
    width: 136px;
    flex: 0 0 136px;
  }

  .model-filter > span {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .model-filter select { width: 100%; }
  .model-search input,
  .model-filter select { min-height: 44px; }

  .model-table-wrap { overflow: visible; }
  .model-table,
  .model-table tbody { display: block; }
  .model-table thead { display: none; }
  .model-table tr {
    display: grid;
    padding: 10px;
    border-bottom: 1px solid var(--line);
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 8px 12px;
  }
  .model-table tr:last-child { border-bottom: 0; }
  .model-table td {
    display: block;
    height: auto;
    padding: 0;
    border: 0;
  }
  .model-table td:nth-child(1),
  .model-table td:nth-child(2),
  .model-table td:nth-child(3) { grid-column: 1; }
  .model-table td:nth-child(4),
  .model-table td:nth-child(5) { grid-column: 2; }
  .model-table td:nth-child(4) { grid-row: 1 / span 2; }
  .model-table td:nth-child(5) { grid-row: 3; }
  .model-table select,
  .model-probe-button { min-height: 44px; }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}
</style>
