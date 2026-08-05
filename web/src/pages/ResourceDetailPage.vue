<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  Settings2,
  Trash2,
} from 'lucide-vue-next'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import SidePanel from '@/components/SidePanel.vue'
import { useConsoleStore } from '@/stores/console'
import type {
  ConnectionTestResult,
  TargetConfig,
  UpstreamConfig,
  UpstreamKey,
  UpstreamKeyUpdateInput,
} from '@/types'

type ResourceKind = 'upstream' | 'target' | 'drift' | 'task'
type DetailTab = 'keys' | 'groups' | 'overview' | 'settings'
type ConnectionState = 'unverified' | 'testing' | 'verified' | 'failed'

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
const keyForm = reactive({
  id: '',
  name: '',
  apiKey: '',
  enabled: true,
  models: '',
})

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

function setTab(tab: DetailTab): void {
  const query = { ...route.query }
  if (tab === defaultTab.value) delete query.tab
  else query.tab = tab
  void router.replace({ query })
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
  keyForm.models = key.models.join(', ')
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
  const upstream = upstreamResource.value
  if (!upstream) return
  const keys = upstream.keys ?? []
  const index = keys.findIndex((candidate) => candidate.id === key.id)
  if (index === -1) upstream.keys = [...keys, key]
  else upstream.keys = keys.map((candidate, candidateIndex) => candidateIndex === index ? key : candidate)
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
        models,
      }
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
    upstream.keys = (upstream.keys ?? []).filter((key) => key.id !== editingKeyId.value)
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
    connectionResult.value = props.kind === 'target'
      ? await api.testTargetConnection(resource.id)
      : await api.testUpstreamConnection(resource.id)
    connectionState.value = 'verified'
  } catch (error) {
    connectionError.value = safeErrorMessage(error)
    connectionState.value = 'failed'
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
            <span>{{ upstreamResource?.keys?.length ?? 0 }} 个 Key</span>
          </div>
          <div class="panel-actions">
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

        <div v-if="!upstreamResource?.keys?.length" class="detail-empty">
          <KeyRound :size="22" aria-hidden="true" />
          <p>{{ upstreamResource?.type === 'newapi' ? '暂无已发现的用户 Key' : '尚未配置通用 Key' }}</p>
        </div>
        <div v-else class="detail-table-wrap">
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
              <tr v-for="key in upstreamResource.keys" :key="key.id">
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
                <td data-label="模型"><strong>{{ key.models.length }} 个模型</strong></td>
                <td data-label="发现状态"><span class="compact-status is-unverified">未验证</span></td>
                <td data-label="凭证">{{ key.credential_present ? '凭证已配置' : '凭证缺失' }}</td>
                <td data-label="操作">
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
        <div class="detail-empty">
          <CircleAlert :size="22" aria-hidden="true" />
          <p>暂无可用分组快照</p>
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
            <span v-else-if="connectionError" class="error-text">{{ connectionError }}</span>
            <span v-else>管理员凭证</span>
          </div>
          <button class="secondary-button" type="button" :disabled="connectionState === 'testing'" aria-label="验证目标连接" @click="validateConnection">
            <RefreshCw :class="{ spin: connectionState === 'testing' }" :size="15" aria-hidden="true" />
            {{ connectionState === 'testing' ? '验证中' : '验证目标连接' }}
          </button>
        </div>

        <div v-if="connectionResult" class="capability-grid" aria-label="目标能力">
          <div>
            <span>地址可达</span>
            <strong>{{ connectionResult.reachable ? '通过' : '未通过' }}</strong>
          </div>
          <div>
            <span>凭证有效</span>
            <strong>{{ connectionResult.authenticated ? '通过' : '未通过' }}</strong>
          </div>
          <div>
            <span>管理权限</span>
            <strong>{{ connectionResult.authorized ? '通过' : '未通过' }}</strong>
          </div>
          <div v-for="(value, capability) in connectionResult.capabilities" :key="capability">
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

.key-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.key-table th {
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
.key-table th:nth-child(6) { width: 54px; }

.key-table td {
  height: 62px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 12px;
}

.key-table tbody tr:last-child td { border-bottom: 0; }

.key-name {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.key-name strong { overflow-wrap: anywhere; color: var(--ink); }
.key-name code { color: var(--muted); font-size: 10px; }

.compact-status { padding: 3px 7px; }
.compact-status.is-on { color: #047857; background: var(--green-soft); }
.compact-status.is-off { color: #52525b; background: #f4f4f5; }
.compact-status.is-unverified { color: #92400e; background: var(--amber-soft); }

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
  .overview-status,
  .overview-entry { align-items: flex-start; flex-wrap: wrap; }
  .overview-status > button,
  .overview-entry > a { margin-left: 52px; }
  .capability-grid { grid-template-columns: 1fr 1fr; }
  .settings-list > div { grid-template-columns: 100px minmax(0, 1fr); }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}
</style>
