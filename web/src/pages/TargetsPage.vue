<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ServerCog,
  Trash2,
} from 'lucide-vue-next'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import SidePanel from '@/components/SidePanel.vue'
import { useConsoleStore } from '@/stores/console'
import type { ConnectionTestResult, TargetConfig, TargetPlatformType } from '@/types'

type TypeFilter = 'all' | TargetPlatformType
type StatusFilter = 'all' | 'unverified' | 'verified' | 'failed'
type TestState = 'unverified' | 'testing' | 'verified' | 'failed'

interface ConnectionState {
  state: TestState
  result?: ConnectionTestResult
  message?: string
}

const route = useRoute()
const router = useRouter()
const store = useConsoleStore()
const searchText = ref(typeof route.query.q === 'string' ? route.query.q : '')
const typeFilter = ref<TypeFilter>(
  route.query.type === 'newapi' || route.query.type === 'cliproxyapi' ? route.query.type : 'all',
)
const statusFilter = ref<StatusFilter>(
  route.query.status === 'verified' || route.query.status === 'failed' || route.query.status === 'unverified'
    ? route.query.status
    : 'all',
)
const connectionStates = reactive(new Map<string, ConnectionState>())
const formOpen = ref(false)
const editingId = ref('')
const saving = ref(false)
const deleting = ref(false)
const deleteArmed = ref(false)
const formError = ref('')
const form = reactive({
  id: '',
  name: '',
  type: 'newapi' as TargetPlatformType,
  baseUrl: '',
  userId: '' as string | number,
  credential: '',
})

const isEditing = computed(() => Boolean(editingId.value))
const filteredTargets = computed(() => {
  const query = searchText.value.trim().toLocaleLowerCase()
  return store.targets.filter((target) => {
    if (typeFilter.value !== 'all' && target.type !== typeFilter.value) return false
    const state = connectionState(target.id).state
    if (statusFilter.value !== 'all' && state !== statusFilter.value) return false
    if (!query) return true
    return [target.name, target.id, target.base_url, platformLabel(target.type)]
      .some((value) => value.toLocaleLowerCase().includes(query))
  })
})

watch([searchText, typeFilter, statusFilter], ([query, type, status]) => {
  const nextQuery = { ...route.query }
  if (query.trim()) nextQuery.q = query.trim()
  else delete nextQuery.q
  if (type !== 'all') nextQuery.type = type
  else delete nextQuery.type
  if (status !== 'all') nextQuery.status = status
  else delete nextQuery.status
  void router.replace({ query: nextQuery })
})

function connectionState(id: string): ConnectionState {
  const pending = connectionStates.get(id)
  if (pending) return pending
  const target = store.targets.find((candidate) => candidate.id === id)
  const state = target?.validation_status === 'verified' || target?.validation_status === 'failed'
    ? target.validation_status
    : 'unverified'
  return {
    state,
    result: state === 'verified' && target
      ? {
          reachable: true,
          authenticated: true,
          authorized: true,
          resource_count: 0,
          capabilities: target.validation_capabilities ?? {},
          validation_status: state,
          validated_at: target.validated_at,
        }
      : undefined,
  }
}

function capabilitySummary(id: string): string {
  const target = store.targets.find((candidate) => candidate.id === id)
  const capabilities = connectionStates.get(id)?.result?.capabilities
    ?? target?.validation_capabilities
  if (!capabilities) return ''
  const summary: string[] = []
  if (typeof capabilities.platform === 'string' && capabilities.platform) {
    summary.push(`平台: ${capabilities.platform}`)
  }
  if (capabilities.providers && typeof capabilities.providers === 'object' && !Array.isArray(capabilities.providers)) {
    const providers = Object.entries(capabilities.providers as Record<string, unknown>)
    const modes = new Set<string>()
    for (const [, capability] of providers) {
      if (!capability || typeof capability !== 'object') continue
      const candidateModes = (capability as { modes?: unknown }).modes
      if (!Array.isArray(candidateModes)) continue
      for (const mode of candidateModes) if (typeof mode === 'string') modes.add(mode)
    }
    summary.push(`${providers.length} 个 provider`)
    if (modes.size > 0) summary.push(`模式: ${[...modes].join(', ')}`)
  }
  const generic = Object.entries(capabilities).flatMap(([name, value]) => {
    if (name === 'platform' || name === 'providers' || name === 'native_auth_schema') return []
    if (typeof value === 'boolean') return `${name}: ${value ? '支持' : '不支持'}`
    if (typeof value === 'string' || typeof value === 'number') return `${name}: ${value}`
    return []
  })
  return [...summary, ...generic].join(' · ')
}

function validatedAt(id: string): string {
  const target = store.targets.find((candidate) => candidate.id === id)
  return connectionStates.get(id)?.result?.validated_at ?? target?.validated_at ?? ''
}

function stateLabel(state: TestState): string {
  if (state === 'testing') return '验证中'
  if (state === 'verified') return '验证通过'
  if (state === 'failed') return '验证失败'
  return '未验证'
}

function platformLabel(type: TargetPlatformType): string {
  return type === 'newapi' ? 'New API' : 'CPA'
}

function displayEndpoint(value: string): string {
  try {
    const url = new URL(value)
    return `${url.host}${url.pathname === '/' ? '' : url.pathname}`
  } catch {
    return value
  }
}

function resetForm(): void {
  editingId.value = ''
  form.id = ''
  form.name = ''
  form.type = 'newapi'
  form.baseUrl = ''
  form.userId = ''
  form.credential = ''
  formError.value = ''
  deleteArmed.value = false
}

function openAdd(): void {
  resetForm()
  formOpen.value = true
}

function openEdit(target: TargetConfig): void {
  resetForm()
  editingId.value = target.id
  form.id = target.id
  form.name = target.name
  form.type = target.type
  form.baseUrl = target.base_url
  form.userId = target.user_id && target.user_id > 0 ? String(target.user_id) : ''
  formOpen.value = true
}

function closeForm(): void {
  if (saving.value || deleting.value) return
  form.credential = ''
  formOpen.value = false
  resetForm()
}

function validHTTPURL(value: string): boolean {
  try {
    const parsed = new URL(value)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:')
      && Boolean(parsed.host)
      && !parsed.username
      && !parsed.password
  } catch {
    return false
  }
}

function normalizedUserID(): number | null | undefined {
  const value = String(form.userId).trim()
  if (!value) return undefined
  if (!/^\d+$/.test(value)) return null
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}

function validateForm(): string {
  if (!form.id.trim() || !form.name.trim()) return '实例 ID 和名称不能为空'
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(form.id.trim())) return '实例 ID 格式无效'
  if (!validHTTPURL(form.baseUrl.trim())) return '请输入不含凭证的绝对 HTTP(S) 地址'
  if (!isEditing.value && !form.credential) {
    return form.type === 'newapi' ? 'New API 管理员 Token 不能为空' : 'CPA 管理员凭证不能为空'
  }
  if (form.type === 'newapi' && normalizedUserID() === null) return 'New API 用户 ID 必须为正整数'
  return ''
}

async function saveTarget(): Promise<void> {
  const validationError = validateForm()
  if (validationError) {
    formError.value = validationError
    return
  }
  const currentTarget = isEditing.value
    ? store.targets.find((candidate) => candidate.id === editingId.value)
    : undefined
  const normalizedBaseURL = form.baseUrl.trim().replace(/\/+$/, '')
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    base_url: normalizedBaseURL,
  }
  if (!isEditing.value) {
    payload.id = form.id.trim()
    payload.type = form.type
  }
  if (form.type === 'newapi') {
    const userID = normalizedUserID()
    if (userID !== undefined && userID !== null) payload.user_id = userID
    else if (isEditing.value) payload.user_id = 0
    if (form.credential) payload.access_token = form.credential
  } else if (form.credential) {
    payload.management_key = form.credential
  }

  const nextUserID = form.type === 'newapi' ? normalizedUserID() : undefined
  const validationInputsChanged = !currentTarget
    || currentTarget.base_url !== normalizedBaseURL
    || Boolean(form.credential)
    || (form.type === 'newapi' && currentTarget.user_id !== (nextUserID ?? undefined))

  form.credential = ''
  saving.value = true
  formError.value = ''
  try {
    const target = isEditing.value
      ? await api.updateTarget(editingId.value, payload)
      : await api.createTarget(payload)
    await store.upsertTarget(target)
    if (target.validation_status) connectionStates.delete(target.id)
    else if (validationInputsChanged) connectionStates.set(target.id, { state: 'unverified' })
    formOpen.value = false
    resetForm()
  } catch (error) {
    formError.value = safeErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function deleteTarget(): Promise<void> {
  if (!editingId.value || deleting.value) return
  if (!deleteArmed.value) {
    deleteArmed.value = true
    return
  }
  deleting.value = true
  formError.value = ''
  try {
    await api.deleteTarget(editingId.value)
    await store.removeTarget(editingId.value)
    connectionStates.delete(editingId.value)
    formOpen.value = false
    resetForm()
  } catch (error) {
    formError.value = safeErrorMessage(error)
  } finally {
    deleting.value = false
  }
}

async function testConnection(target: TargetConfig): Promise<void> {
  connectionStates.set(target.id, { state: 'testing' })
  try {
    const result = await api.testTargetConnection(target.id)
    const state = result.validation_status === 'failed'
      || !result.reachable
      || !result.authenticated
      || !result.authorized
      ? 'failed'
      : 'verified'
    store.setTargetValidation(target.id, state, {
      validatedAt: result.validated_at,
      capabilities: result.capabilities,
    })
    connectionStates.set(target.id, { state, result })
  } catch (error) {
    connectionStates.set(target.id, { state: 'failed', message: safeErrorMessage(error) })
  }
}
</script>

<template>
  <section class="page connections-page" aria-labelledby="targets-heading">
    <header class="page-header connections-header">
      <div>
        <h1 id="targets-heading">目标实例</h1>
        <p>{{ store.targets.length }} 个实例</p>
      </div>
      <button class="primary-button" type="button" @click="openAdd">
        <Plus :size="16" aria-hidden="true" />
        添加目标实例
      </button>
    </header>

    <div class="connection-toolbar" aria-label="目标实例筛选">
      <label class="search-control">
        <Search :size="16" aria-hidden="true" />
        <span class="sr-only">搜索目标实例</span>
        <input v-model="searchText" type="search" aria-label="搜索目标实例" placeholder="搜索名称、ID 或地址" />
      </label>
      <label class="filter-control">
        <span>平台类型</span>
        <select v-model="typeFilter" aria-label="平台类型">
          <option value="all">全部类型</option>
          <option value="newapi">New API</option>
          <option value="cliproxyapi">CPA</option>
        </select>
      </label>
      <label class="filter-control">
        <span>验证状态</span>
        <select v-model="statusFilter" aria-label="验证状态">
          <option value="all">全部状态</option>
          <option value="unverified">未验证</option>
          <option value="verified">验证通过</option>
          <option value="failed">验证失败</option>
        </select>
      </label>
      <span class="result-count">{{ filteredTargets.length }} 项</span>
    </div>

    <section class="connection-surface" aria-label="目标实例列表">
      <div v-if="store.initialState === 'loading'" class="connection-state" role="status">
        <RefreshCw class="spin" :size="20" aria-hidden="true" />
        正在加载目标实例
      </div>
      <div v-else-if="store.initialState === 'error'" class="connection-state is-error" role="alert">
        <CircleAlert :size="20" aria-hidden="true" />
        <span>{{ store.initialError || '目标实例加载失败' }}</span>
        <button class="secondary-button" type="button" @click="store.loadConsole">重试</button>
      </div>
      <div v-else-if="store.targets.length === 0" class="connection-state empty-state">
        <ServerCog :size="24" aria-hidden="true" />
        <h2>尚未配置目标实例</h2>
        <button class="primary-button" type="button" @click="openAdd">添加目标实例</button>
      </div>
      <div v-else-if="filteredTargets.length === 0" class="connection-state empty-state">
        <Search :size="22" aria-hidden="true" />
        <h2>没有匹配的目标实例</h2>
        <button class="secondary-button" type="button" @click="searchText = ''; typeFilter = 'all'; statusFilter = 'all'">
          清除筛选
        </button>
      </div>
      <div v-else class="table-wrap">
        <table class="connection-table">
          <thead>
            <tr>
              <th scope="col">实例</th>
              <th scope="col">平台</th>
              <th scope="col">管理端点</th>
              <th scope="col">凭证权限</th>
              <th scope="col">验证</th>
              <th scope="col"><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="target in filteredTargets" :key="target.id">
              <td data-label="实例">
                <div class="primary-cell">
                  <RouterLink :to="{ name: 'target-detail', params: { id: target.id } }">
                    {{ target.name }}
                  </RouterLink>
                  <code>{{ target.id }}</code>
                </div>
              </td>
              <td data-label="平台">
                <span class="type-badge">{{ platformLabel(target.type) }}</span>
              </td>
              <td data-label="管理端点">
                <span class="endpoint" :title="target.base_url">{{ displayEndpoint(target.base_url) }}</span>
              </td>
              <td data-label="凭证权限">
                <div class="summary-cell">
                  <strong>管理员</strong>
                  <span v-if="target.type === 'newapi' && target.user_id">用户 ID {{ target.user_id }}</span>
                  <span v-else>{{ target.type === 'newapi' ? '管理 Token' : 'Management Key' }}</span>
                </div>
              </td>
              <td data-label="验证">
                <div class="validation-cell">
                  <span class="connection-status" :class="`is-${connectionState(target.id).state}`">
                    <CheckCircle2 v-if="connectionState(target.id).state === 'verified'" :size="14" aria-hidden="true" />
                    <CircleAlert v-else-if="connectionState(target.id).state === 'failed'" :size="14" aria-hidden="true" />
                    <span v-else class="status-dot" aria-hidden="true" />
                    {{ stateLabel(connectionState(target.id).state) }}
                  </span>
                  <small v-if="connectionState(target.id).result">
                    <template v-if="connectionState(target.id).result!.resource_count > 0">
                      {{ connectionState(target.id).result!.resource_count }} 个渠道
                    </template>
                    <template v-else-if="validatedAt(target.id)">最近验证 {{ validatedAt(target.id) }}</template>
                  </small>
                  <small v-if="capabilitySummary(target.id)" class="capability-summary">
                    {{ capabilitySummary(target.id) }}
                  </small>
                  <small v-if="connectionState(target.id).message" class="error-text">
                    {{ connectionState(target.id).message }}
                  </small>
                </div>
              </td>
              <td data-label="操作">
                <div class="row-actions">
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :disabled="connectionState(target.id).state === 'testing'"
                    :aria-label="`验证 ${target.name} 连接`"
                    title="验证连接"
                    @click="testConnection(target)"
                  >
                    <RefreshCw :class="{ spin: connectionState(target.id).state === 'testing' }" :size="16" aria-hidden="true" />
                  </button>
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :aria-label="`编辑 ${target.name}`"
                    title="编辑目标"
                    @click="openEdit(target)"
                  >
                    <Pencil :size="16" aria-hidden="true" />
                  </button>
                  <RouterLink
                    class="icon-button icon-button-small"
                    :to="{ name: 'target-detail', params: { id: target.id } }"
                    :aria-label="`查看 ${target.name} 概览`"
                    title="查看概览"
                  >
                    <ArrowRight :size="16" aria-hidden="true" />
                  </RouterLink>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <SidePanel
      v-if="formOpen"
      :title="isEditing ? '编辑目标实例' : '添加目标实例'"
      :close-label="isEditing ? '关闭编辑目标实例' : '关闭添加目标实例'"
      width="regular"
      @close="closeForm"
    >
      <form class="drawer-form" @submit.prevent="saveTarget">
        <fieldset class="preset-fieldset" :disabled="isEditing">
          <legend>平台类型</legend>
          <div class="preset-options">
            <label :class="{ selected: form.type === 'newapi' }">
              <input v-model="form.type" type="radio" value="newapi" aria-label="New API" />
              <span>New API</span>
              <small>管理员 Token</small>
            </label>
            <label :class="{ selected: form.type === 'cliproxyapi' }">
              <input v-model="form.type" type="radio" value="cliproxyapi" aria-label="CPA" />
              <span>CPA</span>
              <small>Management Key</small>
            </label>
          </div>
        </fieldset>

        <div class="form-section">
          <label class="drawer-field">
            <span>实例 ID</span>
            <input v-model="form.id" type="text" autocomplete="off" :disabled="isEditing" />
          </label>
          <label class="drawer-field">
            <span>实例名称</span>
            <input v-model="form.name" type="text" autocomplete="off" />
          </label>
          <label class="drawer-field">
            <span>Base URL</span>
            <input v-model="form.baseUrl" type="url" inputmode="url" autocomplete="off" />
          </label>
          <label v-if="form.type === 'newapi'" class="drawer-field">
            <span>New API 用户 ID</span>
            <input v-model="form.userId" type="number" min="1" step="1" inputmode="numeric" autocomplete="off" />
          </label>
          <label class="drawer-field">
            <span>{{ form.type === 'newapi' ? 'New API 管理员 Token' : 'CPA 管理员凭证' }}</span>
            <input
              v-model="form.credential"
              type="password"
              autocomplete="new-password"
              autocapitalize="off"
              spellcheck="false"
            />
          </label>
        </div>

        <p v-if="formError" class="drawer-error" role="alert">{{ formError }}</p>

        <section v-if="isEditing" class="target-validation-summary" aria-label="目标验证详情">
          <strong>{{ stateLabel(connectionState(editingId).state) }}</strong>
          <span v-if="validatedAt(editingId)">{{ validatedAt(editingId) }}</span>
          <small v-if="capabilitySummary(editingId)">{{ capabilitySummary(editingId) }}</small>
        </section>

        <section v-if="isEditing" class="danger-zone">
          <strong>删除实例</strong>
          <button class="danger-button" type="button" :disabled="deleting" @click="deleteTarget">
            <Trash2 :size="15" aria-hidden="true" />
            {{ deleting ? '删除中' : deleteArmed ? '确认删除实例' : '删除实例' }}
          </button>
        </section>

        <footer class="drawer-actions">
          <button class="secondary-button" type="button" :disabled="saving || deleting" @click="closeForm">取消</button>
          <button class="primary-button" type="submit" :disabled="saving || deleting">
            {{ saving ? '保存中' : '保存目标' }}
          </button>
        </footer>
      </form>
    </SidePanel>
  </section>
</template>

<style scoped>
.connections-page {
  display: flex;
  min-height: calc(100svh - var(--app-header-height) - 48px);
  flex-direction: column;
}

.connections-header > div {
  display: grid;
  gap: 2px;
}

.connections-header p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}

.connection-toolbar {
  display: grid;
  min-height: 46px;
  grid-template-columns: minmax(260px, 1fr) minmax(180px, 220px) minmax(180px, 220px) auto;
  align-items: center;
  gap: 10px;
  padding: 0 0 12px;
  background: var(--surface);
}

.search-control {
  position: relative;
  display: flex;
  min-width: 0;
  align-items: center;
}

.search-control svg {
  position: absolute;
  left: 10px;
  color: var(--muted);
  pointer-events: none;
}

.search-control input,
.filter-control select,
.drawer-field input {
  width: 100%;
  min-height: 34px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  color: var(--ink);
  background: var(--surface);
}

.search-control input { padding: 7px 10px 7px 34px; }

.filter-control {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}

.filter-control > span {
  flex: 0 0 auto;
  color: var(--muted);
  font-size: 11px;
  font-weight: 650;
}

.filter-control select {
  padding: 6px 28px 6px 9px;
  font-size: 12px;
}

.result-count {
  justify-self: end;
  color: var(--muted);
  font-size: 12px;
  white-space: nowrap;
}

.connection-surface {
  display: flex;
  min-height: 260px;
  flex: 1 1 auto;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
}

.table-wrap {
  min-height: 0;
  flex: 1 1 auto;
  overflow: auto;
}

.connection-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.connection-table th {
  position: sticky;
  z-index: 1;
  top: 0;
  height: 38px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
  color: #52525b;
  background: var(--surface-subtle);
  font-size: 11px;
  font-weight: 700;
  text-align: left;
}

.connection-table th:nth-child(1) { width: 22%; }
.connection-table th:nth-child(2) { width: 12%; }
.connection-table th:nth-child(3) { width: 22%; }
.connection-table th:nth-child(4) { width: 18%; }
.connection-table th:nth-child(5) { width: 17%; }
.connection-table th:nth-child(6) { width: 116px; }

.connection-table td {
  height: 62px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 12px;
  vertical-align: middle;
}

.connection-table tbody tr:last-child td { border-bottom: 0; }
.connection-table tbody tr:hover { background: #fafafa; }

.primary-cell,
.summary-cell,
.validation-cell {
  display: grid;
  min-width: 0;
  gap: 4px;
}

.primary-cell a {
  overflow: hidden;
  color: var(--ink);
  font-size: 13px;
  font-weight: 650;
  text-decoration: none;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.primary-cell a:hover { color: var(--blue); }

.primary-cell code {
  overflow: hidden;
  color: var(--muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.type-badge,
.connection-status {
  display: inline-flex;
  width: fit-content;
  align-items: center;
  gap: 5px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 650;
}

.type-badge {
  padding: 4px 8px;
  color: #3f3f46;
  background: #f4f4f5;
}

.endpoint {
  display: block;
  overflow: hidden;
  color: #52525b;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary-cell strong { color: var(--ink); font-size: 12px; }
.summary-cell span,
.validation-cell small { color: var(--muted); font-size: 11px; }

.capability-summary {
  overflow-wrap: anywhere;
  line-height: 1.35;
}

.connection-status {
  padding: 3px 7px;
  color: #52525b;
  background: #f4f4f5;
}

.connection-status.is-verified { color: #047857; background: var(--green-soft); }
.connection-status.is-failed { color: #b91c1c; background: var(--red-soft); }
.connection-status.is-testing { color: #1d4ed8; background: var(--blue-soft); }

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #a1a1aa;
}

.error-text {
  overflow: hidden;
  color: #b91c1c !important;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-actions { justify-content: flex-end; }

.connection-state {
  display: flex;
  min-height: 260px;
  flex: 1 1 auto;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 28px;
  color: var(--muted);
  text-align: center;
}

.connection-state.is-error { color: #b91c1c; }
.empty-state { flex-direction: column; }
.empty-state h2 { margin: 2px 0 4px; color: var(--ink); font-size: 15px; }
.spin { animation: spin 900ms linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.drawer-form {
  display: grid;
  min-height: 100%;
  align-content: start;
  gap: 18px;
}

.preset-fieldset {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.preset-fieldset legend,
.drawer-field > span {
  color: #3f3f46;
  font-size: 12px;
  font-weight: 700;
}

.preset-fieldset legend { margin-bottom: 8px; }

.preset-options {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.preset-options label {
  position: relative;
  display: grid;
  min-height: 72px;
  align-content: center;
  gap: 4px;
  padding: 10px 11px 10px 34px;
  border: 1px solid var(--line-strong);
  border-radius: 7px;
  background: var(--surface);
}

.preset-options label.selected { border-color: #93c5fd; background: #eff6ff; }
.preset-options input { position: absolute; top: 14px; left: 11px; }
.preset-options span { color: var(--ink); font-size: 12px; font-weight: 700; }
.preset-options small { color: var(--muted); font-size: 10px; }

.form-section {
  display: grid;
  gap: 12px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}

.target-validation-summary {
  display: grid;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 7px;
  background: var(--surface-subtle);
}

.target-validation-summary span,
.target-validation-summary small {
  color: var(--muted);
  font-size: 11px;
  overflow-wrap: anywhere;
}

.drawer-field { display: grid; gap: 6px; }
.drawer-field input { padding: 7px 9px; }

.drawer-error {
  margin: 0;
  padding: 9px 10px;
  border: 1px solid #fecaca;
  border-radius: 6px;
  color: #b91c1c;
  background: var(--red-soft);
  font-size: 12px;
}

.danger-zone,
.drawer-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.danger-zone {
  padding: 12px;
  border: 1px solid #fecaca;
  border-radius: 7px;
  color: #991b1b;
  background: #fffafa;
}

.danger-zone strong { font-size: 12px; }

.drawer-actions {
  position: sticky;
  bottom: -18px;
  justify-content: flex-end;
  margin: auto -18px -18px;
  padding: 12px 18px;
  border-top: 1px solid var(--line);
  background: var(--surface);
}

@media (max-width: 900px) {
  .connection-toolbar { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; }
  .search-control { grid-column: 1 / -1; }
  .filter-control { width: 100%; }
}

@media (max-width: 720px) {
  .connections-page { display: block; min-height: 0; }
  .connections-header { align-items: flex-start; }
  .connections-header .primary-button { min-width: 44px; min-height: 44px; }
  .connection-toolbar {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    padding-bottom: 10px;
  }
  .filter-control { display: grid; gap: 4px; }
  .result-count { grid-column: 1 / -1; justify-self: end; }
  .connection-surface { min-height: 0; flex: none; }
  .table-wrap { overflow: visible; }
  .connection-table,
  .connection-table tbody { display: block; }
  .connection-table thead { display: none; }
  .connection-table tr {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    padding: 14px 12px 12px;
    border-bottom: 1px solid var(--line);
    gap: 10px 14px;
  }
  .connection-table tbody tr:last-child { border-bottom: 0; }
  .connection-table td { display: block; height: auto; padding: 0; border: 0; }
  .connection-table td:nth-child(1),
  .connection-table td:nth-child(3) { grid-column: 1; }
  .connection-table td:nth-child(2) { grid-row: 1; grid-column: 2; }
  .connection-table td:nth-child(3) { grid-row: 2; grid-column: 1 / -1; }
  .connection-table td:nth-child(4) { grid-row: 3; grid-column: 1; }
  .connection-table td:nth-child(5) { grid-row: 3; grid-column: 2; justify-self: end; }
  .connection-table td:nth-child(6) {
    grid-row: 4;
    grid-column: 1 / -1;
    margin-top: 2px;
    padding-top: 10px;
    border-top: 1px solid var(--line);
  }
  .row-actions {
    display: grid;
    width: 100%;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }
  .row-actions .icon-button { width: 100%; height: 44px; }
  .preset-options { grid-template-columns: 1fr; }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}
</style>
