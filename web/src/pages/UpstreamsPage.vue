<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-vue-next'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import SidePanel from '@/components/SidePanel.vue'
import { useConsoleStore } from '@/stores/console'
import type { ConnectionTestResult, UpstreamConfig, UpstreamPlatformType } from '@/types'

type TypeFilter = 'all' | UpstreamPlatformType
type StatusFilter = 'all' | 'unverified' | 'verified' | 'failed'
type TestState = 'unverified' | 'testing' | 'verified' | 'failed'

interface ConnectionState {
  state: TestState
  result?: ConnectionTestResult
  message?: string
}

interface KeyDraft {
  id: string
  name: string
  apiKey: string
}

const route = useRoute()
const router = useRouter()
const store = useConsoleStore()
const searchText = ref(typeof route.query.q === 'string' ? route.query.q : '')
const typeFilter = ref<TypeFilter>(
  route.query.type === 'newapi' || route.query.type === 'generic' ? route.query.type : 'all',
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
  type: 'newapi' as UpstreamPlatformType,
  baseUrl: '',
  userId: '' as string | number,
  credential: '',
  keys: [] as KeyDraft[],
})

const isEditing = computed(() => Boolean(editingId.value))
const filteredUpstreams = computed(() => {
  const query = searchText.value.trim().toLocaleLowerCase()
  return store.upstreams.filter((upstream) => {
    if (typeFilter.value !== 'all' && upstream.type !== typeFilter.value) return false
    const state = connectionState(upstream.id).state
    if (statusFilter.value !== 'all' && state !== statusFilter.value) return false
    if (!query) return true
    return [upstream.name, upstream.id, upstream.base_url, platformLabel(upstream.type)]
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
  return connectionStates.get(id) ?? { state: 'unverified' }
}

function stateLabel(state: TestState): string {
  if (state === 'testing') return '验证中'
  if (state === 'verified') return '验证通过'
  if (state === 'failed') return '验证失败'
  return '未验证'
}

function platformLabel(type: UpstreamPlatformType): string {
  return type === 'newapi' ? 'New API 普通用户' : '通用 OpenAI-compatible'
}

function displayEndpoint(value: string): string {
  try {
    const url = new URL(value)
    return `${url.host}${url.pathname === '/' ? '' : url.pathname}`
  } catch {
    return value
  }
}

function keyCount(upstream: UpstreamConfig): number {
  return upstream.keys?.length ?? 0
}

function enabledKeyCount(upstream: UpstreamConfig): number {
  return upstream.keys?.filter((key) => key.enabled).length ?? 0
}

function modelCount(upstream: UpstreamConfig): number {
  return upstream.keys?.reduce((total, key) => total + key.models.length, 0) ?? 0
}

function resetForm(): void {
  editingId.value = ''
  form.id = ''
  form.name = ''
  form.type = 'newapi'
  form.baseUrl = ''
  form.userId = ''
  form.credential = ''
  form.keys = [{ id: '', name: '', apiKey: '' }]
  formError.value = ''
  deleteArmed.value = false
}

function openAdd(): void {
  resetForm()
  formOpen.value = true
}

function openEdit(upstream: UpstreamConfig): void {
  resetForm()
  editingId.value = upstream.id
  form.id = upstream.id
  form.name = upstream.name
  form.type = upstream.type
  form.baseUrl = upstream.base_url
  form.userId = upstream.user_id && upstream.user_id > 0 ? String(upstream.user_id) : ''
  formOpen.value = true
}

function closeForm(): void {
  if (saving.value || deleting.value) return
  form.credential = ''
  for (const key of form.keys) key.apiKey = ''
  formOpen.value = false
  resetForm()
}

function addKeyDraft(): void {
  form.keys.push({ id: '', name: '', apiKey: '' })
}

function removeKeyDraft(index: number): void {
  if (form.keys.length === 1) return
  form.keys[index]!.apiKey = ''
  form.keys.splice(index, 1)
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
  if (!form.id.trim() || !form.name.trim()) return '连接 ID 和名称不能为空'
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(form.id.trim())) return '连接 ID 格式无效'
  if (!validHTTPURL(form.baseUrl.trim())) return '请输入不含凭证的绝对 HTTP(S) 地址'
  if (form.type === 'newapi') {
    if (!isEditing.value && !form.credential) return '普通用户管理 Token 不能为空'
    if (normalizedUserID() === null) return 'New API 用户 ID 必须为正整数'
    return ''
  }
  if (isEditing.value) return ''
  if (form.keys.length === 0) return '至少添加一个 Key'
  const ids = new Set<string>()
  for (const [index, key] of form.keys.entries()) {
    if (!key.id.trim() || !key.name.trim() || !key.apiKey) return `第 ${index + 1} 个 Key 信息不完整`
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(key.id.trim())) return `第 ${index + 1} 个 Key ID 格式无效`
    if (ids.has(key.id.trim())) return 'Key ID 不能重复'
    ids.add(key.id.trim())
  }
  return ''
}

async function saveUpstream(): Promise<void> {
  const validationError = validateForm()
  if (validationError) {
    formError.value = validationError
    return
  }
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    base_url: form.baseUrl.trim().replace(/\/+$/, ''),
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
  } else if (!isEditing.value) {
    payload.keys = form.keys.map((key) => ({
      id: key.id.trim(),
      name: key.name.trim(),
      api_key: key.apiKey,
      enabled: true,
    }))
  }

  form.credential = ''
  for (const key of form.keys) key.apiKey = ''
  saving.value = true
  formError.value = ''
  try {
    const upstream = isEditing.value
      ? await api.updateUpstream(editingId.value, payload)
      : await api.createUpstream(payload)
    await store.upsertUpstream(upstream)
    formOpen.value = false
    resetForm()
  } catch (error) {
    formError.value = safeErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function deleteUpstream(): Promise<void> {
  if (!editingId.value || deleting.value) return
  if (!deleteArmed.value) {
    deleteArmed.value = true
    return
  }
  deleting.value = true
  formError.value = ''
  try {
    await api.deleteUpstream(editingId.value)
    await store.removeUpstream(editingId.value)
    connectionStates.delete(editingId.value)
    formOpen.value = false
    resetForm()
  } catch (error) {
    formError.value = safeErrorMessage(error)
  } finally {
    deleting.value = false
  }
}

async function testConnection(upstream: UpstreamConfig): Promise<void> {
  connectionStates.set(upstream.id, { state: 'testing' })
  try {
    const result = await api.testUpstreamConnection(upstream.id)
    connectionStates.set(upstream.id, { state: 'verified', result })
  } catch (error) {
    connectionStates.set(upstream.id, { state: 'failed', message: safeErrorMessage(error) })
  }
}
</script>

<template>
  <section class="page connections-page" aria-labelledby="upstreams-heading">
    <header class="page-header connections-header">
      <div>
        <h1 id="upstreams-heading">上游连接</h1>
        <p>{{ store.upstreams.length }} 个连接</p>
      </div>
      <button class="primary-button" type="button" @click="openAdd">
        <Plus :size="16" aria-hidden="true" />
        添加上游连接
      </button>
    </header>

    <div class="connection-toolbar" aria-label="上游连接筛选">
      <label class="search-control">
        <Search :size="16" aria-hidden="true" />
        <span class="sr-only">搜索上游连接</span>
        <input v-model="searchText" type="search" aria-label="搜索上游连接" placeholder="搜索名称、ID 或地址" />
      </label>
      <label class="filter-control">
        <span>接入预设</span>
        <select v-model="typeFilter" aria-label="接入预设">
          <option value="all">全部预设</option>
          <option value="newapi">New API 普通用户</option>
          <option value="generic">通用 OpenAI-compatible</option>
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
      <span class="result-count">{{ filteredUpstreams.length }} 项</span>
    </div>

    <section class="connection-surface" aria-label="上游连接列表">
      <div v-if="store.initialState === 'loading'" class="connection-state" role="status">
        <RefreshCw class="spin" :size="20" aria-hidden="true" />
        正在加载上游连接
      </div>
      <div v-else-if="store.initialState === 'error'" class="connection-state is-error" role="alert">
        <CircleAlert :size="20" aria-hidden="true" />
        <span>{{ store.initialError || '上游连接加载失败' }}</span>
        <button class="secondary-button" type="button" @click="store.loadConsole">重试</button>
      </div>
      <div v-else-if="store.upstreams.length === 0" class="connection-state empty-state">
        <KeyRound :size="24" aria-hidden="true" />
        <h2>尚未配置上游连接</h2>
        <button class="primary-button" type="button" @click="openAdd">添加上游连接</button>
      </div>
      <div v-else-if="filteredUpstreams.length === 0" class="connection-state empty-state">
        <Search :size="22" aria-hidden="true" />
        <h2>没有匹配的上游连接</h2>
        <button class="secondary-button" type="button" @click="searchText = ''; typeFilter = 'all'; statusFilter = 'all'">
          清除筛选
        </button>
      </div>
      <div v-else class="table-wrap">
        <table class="connection-table">
          <thead>
            <tr>
              <th scope="col">连接</th>
              <th scope="col">接入预设</th>
              <th scope="col">端点</th>
              <th scope="col">Key / 模型</th>
              <th scope="col">验证</th>
              <th scope="col"><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="upstream in filteredUpstreams" :key="upstream.id">
              <td data-label="连接">
                <div class="primary-cell">
                  <RouterLink :to="{ name: 'upstream-detail', params: { id: upstream.id } }">
                    {{ upstream.name }}
                  </RouterLink>
                  <code>{{ upstream.id }}</code>
                </div>
              </td>
              <td data-label="接入预设">
                <span class="type-badge">{{ platformLabel(upstream.type) }}</span>
              </td>
              <td data-label="端点">
                <span class="endpoint" :title="upstream.base_url">{{ displayEndpoint(upstream.base_url) }}</span>
              </td>
              <td data-label="Key / 模型">
                <div v-if="keyCount(upstream) > 0" class="summary-cell">
                  <strong>{{ enabledKeyCount(upstream) }} / {{ keyCount(upstream) }} 启用</strong>
                  <span>{{ modelCount(upstream) }} 个模型</span>
                </div>
                <span v-else class="muted-value">待发现</span>
              </td>
              <td data-label="验证">
                <div class="validation-cell">
                  <span class="connection-status" :class="`is-${connectionState(upstream.id).state}`">
                    <CheckCircle2 v-if="connectionState(upstream.id).state === 'verified'" :size="14" aria-hidden="true" />
                    <CircleAlert v-else-if="connectionState(upstream.id).state === 'failed'" :size="14" aria-hidden="true" />
                    <span v-else class="status-dot" aria-hidden="true" />
                    {{ stateLabel(connectionState(upstream.id).state) }}
                  </span>
                  <small v-if="connectionState(upstream.id).result">
                    {{ connectionState(upstream.id).result?.resource_count }} 个资源
                  </small>
                  <small v-else-if="connectionState(upstream.id).message" class="error-text">
                    {{ connectionState(upstream.id).message }}
                  </small>
                </div>
              </td>
              <td data-label="操作">
                <div class="row-actions">
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :disabled="connectionState(upstream.id).state === 'testing'"
                    :aria-label="`验证 ${upstream.name} 连接`"
                    title="验证连接"
                    @click="testConnection(upstream)"
                  >
                    <RefreshCw :class="{ spin: connectionState(upstream.id).state === 'testing' }" :size="16" aria-hidden="true" />
                  </button>
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :aria-label="`编辑 ${upstream.name}`"
                    title="编辑连接"
                    @click="openEdit(upstream)"
                  >
                    <Pencil :size="16" aria-hidden="true" />
                  </button>
                  <RouterLink
                    class="icon-button icon-button-small"
                    :to="{ name: 'upstream-detail', params: { id: upstream.id } }"
                    :aria-label="`查看 ${upstream.name} 详情`"
                    title="查看详情"
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
      :title="isEditing ? '编辑上游连接' : '添加上游连接'"
      :close-label="isEditing ? '关闭编辑上游连接' : '关闭添加上游连接'"
      width="regular"
      @close="closeForm"
    >
      <form class="drawer-form" @submit.prevent="saveUpstream">
        <fieldset class="preset-fieldset" :disabled="isEditing">
          <legend>接入预设</legend>
          <div class="preset-options">
            <label :class="{ selected: form.type === 'newapi' }">
              <input v-model="form.type" type="radio" value="newapi" aria-label="New API 普通用户" />
              <span>New API 普通用户</span>
              <small>URL + 普通用户管理 Token</small>
            </label>
            <label :class="{ selected: form.type === 'generic' }">
              <input v-model="form.type" type="radio" value="generic" aria-label="通用 OpenAI-compatible" />
              <span>通用 OpenAI-compatible</span>
              <small>URL + 一个或多个 API Key</small>
            </label>
          </div>
        </fieldset>

        <div class="form-section">
          <label class="drawer-field">
            <span>连接 ID</span>
            <input v-model="form.id" type="text" autocomplete="off" :disabled="isEditing" />
          </label>
          <label class="drawer-field">
            <span>连接名称</span>
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
          <label v-if="form.type === 'newapi'" class="drawer-field">
            <span>普通用户管理 Token</span>
            <input
              v-model="form.credential"
              type="password"
              autocomplete="new-password"
              autocapitalize="off"
              spellcheck="false"
            />
          </label>
        </div>

        <section v-if="form.type === 'generic' && !isEditing" class="form-section key-editor" aria-labelledby="generic-keys-heading">
          <header>
            <div>
              <h3 id="generic-keys-heading">API Key</h3>
              <span>{{ form.keys.length }} 个</span>
            </div>
            <button class="secondary-button compact-button" type="button" @click="addKeyDraft">
              <Plus :size="15" aria-hidden="true" />
              再添加一个 Key
            </button>
          </header>
          <div v-for="(key, index) in form.keys" :key="index" class="key-draft">
            <div class="key-draft-title">
              <strong>Key {{ index + 1 }}</strong>
              <button
                v-if="form.keys.length > 1"
                class="panel-icon-button"
                type="button"
                :aria-label="`移除第 ${index + 1} 个 Key`"
                title="移除 Key"
                @click="removeKeyDraft(index)"
              >
                <Trash2 :size="15" aria-hidden="true" />
              </button>
            </div>
            <label class="drawer-field">
              <span>Key ID</span>
              <input v-model="key.id" type="text" autocomplete="off" :aria-label="`第 ${index + 1} 个 Key ID`" />
            </label>
            <label class="drawer-field">
              <span>Key 别名</span>
              <input v-model="key.name" type="text" autocomplete="off" :aria-label="`第 ${index + 1} 个 Key 别名`" />
            </label>
            <label class="drawer-field">
              <span>API Key</span>
              <input
                v-model="key.apiKey"
                type="password"
                autocomplete="new-password"
                autocapitalize="off"
                spellcheck="false"
                :aria-label="`第 ${index + 1} 个 API Key`"
              />
            </label>
          </div>
        </section>

        <p v-if="formError" class="drawer-error" role="alert">{{ formError }}</p>

        <section v-if="isEditing" class="danger-zone">
          <strong>删除连接</strong>
          <button class="danger-button" type="button" :disabled="deleting" @click="deleteUpstream">
            <Trash2 :size="15" aria-hidden="true" />
            {{ deleting ? '删除中' : deleteArmed ? '确认删除连接' : '删除连接' }}
          </button>
        </section>

        <footer class="drawer-actions">
          <button class="secondary-button" type="button" :disabled="saving || deleting" @click="closeForm">取消</button>
          <button class="primary-button" type="submit" :disabled="saving || deleting">
            {{ saving ? '保存中' : '保存上游' }}
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

.search-control input {
  padding: 7px 10px 7px 34px;
}

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
.connection-table th:nth-child(2) { width: 19%; }
.connection-table th:nth-child(3) { width: 21%; }
.connection-table th:nth-child(4) { width: 14%; }
.connection-table th:nth-child(5) { width: 15%; }
.connection-table th:nth-child(6) { width: 116px; }

.connection-table td {
  height: 62px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--line);
  color: #3f3f46;
  font-size: 12px;
  vertical-align: middle;
}

.connection-table tbody tr:last-child td {
  border-bottom: 0;
}

.connection-table tbody tr:hover {
  background: #fafafa;
}

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

.primary-cell a:hover {
  color: var(--blue);
}

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

.summary-cell strong {
  color: var(--ink);
  font-size: 12px;
}

.summary-cell span,
.validation-cell small,
.muted-value {
  color: var(--muted);
  font-size: 11px;
}

.connection-status {
  padding: 3px 7px;
  color: #52525b;
  background: #f4f4f5;
}

.connection-status.is-verified {
  color: #047857;
  background: var(--green-soft);
}

.connection-status.is-failed {
  color: #b91c1c;
  background: var(--red-soft);
}

.connection-status.is-testing {
  color: #1d4ed8;
  background: var(--blue-soft);
}

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

.row-actions {
  justify-content: flex-end;
}

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

.connection-state.is-error {
  color: #b91c1c;
}

.empty-state {
  flex-direction: column;
}

.empty-state h2 {
  margin: 2px 0 4px;
  color: var(--ink);
  font-size: 15px;
}

.spin {
  animation: spin 900ms linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

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
.drawer-field > span,
.key-editor h3 {
  color: #3f3f46;
  font-size: 12px;
  font-weight: 700;
}

.preset-fieldset legend {
  margin-bottom: 8px;
}

.preset-options {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.preset-options label {
  position: relative;
  display: grid;
  min-height: 76px;
  align-content: center;
  gap: 4px;
  padding: 10px 11px 10px 34px;
  border: 1px solid var(--line-strong);
  border-radius: 7px;
  background: var(--surface);
}

.preset-options label.selected {
  border-color: #93c5fd;
  background: #eff6ff;
}

.preset-options input {
  position: absolute;
  top: 14px;
  left: 11px;
}

.preset-options span {
  color: var(--ink);
  font-size: 12px;
  font-weight: 700;
}

.preset-options small {
  color: var(--muted);
  font-size: 10px;
  line-height: 1.4;
}

.form-section {
  display: grid;
  gap: 12px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}

.drawer-field {
  display: grid;
  gap: 6px;
}

.drawer-field input {
  padding: 7px 9px;
}

.key-editor > header,
.key-editor > header > div,
.key-draft-title,
.danger-zone,
.drawer-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.key-editor > header > div {
  justify-content: flex-start;
}

.key-editor h3 {
  margin: 0;
}

.key-editor header span {
  color: var(--muted);
  font-size: 11px;
}

.compact-button {
  min-height: 32px;
  padding: 0 9px;
  font-size: 11px;
}

.key-draft {
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 7px;
  background: var(--surface-subtle);
}

.key-draft-title strong {
  font-size: 12px;
}

.panel-icon-button {
  display: inline-grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--red);
  background: transparent;
}

.panel-icon-button:hover {
  border-color: #fecaca;
  background: var(--red-soft);
}

.drawer-error {
  margin: 0;
  padding: 9px 10px;
  border: 1px solid #fecaca;
  border-radius: 6px;
  color: #b91c1c;
  background: var(--red-soft);
  font-size: 12px;
}

.danger-zone {
  padding: 12px;
  border: 1px solid #fecaca;
  border-radius: 7px;
  color: #991b1b;
  background: #fffafa;
}

.danger-zone strong {
  font-size: 12px;
}

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
  .connection-toolbar {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  }

  .search-control {
    grid-column: 1 / -1;
  }

  .filter-control {
    width: 100%;
  }
}

@media (max-width: 720px) {
  .connections-page {
    display: block;
    min-height: 0;
  }

  .connections-header {
    align-items: flex-start;
  }

  .connections-header .primary-button {
    min-width: 44px;
    min-height: 44px;
  }

  .connection-toolbar {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    padding-bottom: 10px;
  }

  .filter-control {
    display: grid;
    gap: 4px;
  }

  .result-count {
    grid-column: 1 / -1;
    justify-self: end;
  }

  .connection-surface {
    min-height: 0;
    flex: none;
  }

  .table-wrap {
    overflow: visible;
  }

  .connection-table,
  .connection-table tbody {
    display: block;
  }

  .connection-table thead {
    display: none;
  }

  .connection-table tr {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    padding: 14px 12px 12px;
    border-bottom: 1px solid var(--line);
    gap: 10px 14px;
  }

  .connection-table tbody tr:last-child {
    border-bottom: 0;
  }

  .connection-table td {
    display: block;
    height: auto;
    padding: 0;
    border: 0;
  }

  .connection-table td:nth-child(1),
  .connection-table td:nth-child(3) {
    grid-column: 1;
  }

  .connection-table td:nth-child(2) {
    grid-row: 1;
    grid-column: 2;
  }

  .connection-table td:nth-child(3) {
    grid-row: 2;
    grid-column: 1 / -1;
  }

  .connection-table td:nth-child(4) {
    grid-row: 3;
    grid-column: 1;
  }

  .connection-table td:nth-child(5) {
    grid-row: 3;
    grid-column: 2;
    justify-self: end;
  }

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

  .row-actions .icon-button {
    width: 100%;
    height: 44px;
  }

  .preset-options {
    grid-template-columns: 1fr;
  }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}
</style>
