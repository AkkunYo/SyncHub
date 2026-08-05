<script setup lang="ts">
import { computed, nextTick, reactive, ref, watchEffect } from 'vue'
import { Pencil, Plus, Save, Trash2 } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import { useConsoleStore } from '@/stores/console'
import type { AppSettings, PlatformType, TargetConfig, UpstreamConfig } from '@/types'

type EntityKind = 'target' | 'upstream'
type SettingsTab = 'instances' | 'runtime'

const store = useConsoleStore()
const activeSettingsTab = ref<SettingsTab>('instances')
const instancesTabButton = ref<HTMLButtonElement | null>(null)
const runtimeTabButton = ref<HTMLButtonElement | null>(null)
const formOpen = ref(false)
const entityKind = ref<EntityKind>('target')
const editingId = ref('')
const entityId = ref('')
const entityName = ref('')
const entityType = ref<PlatformType>('newapi')
const baseUrl = ref('')
const userId = ref<string | number>('')
const credential = ref('')
const formError = ref('')
const saving = ref(false)
const deleting = ref(false)
const deleteItem = ref<{ kind: EntityKind; id: string; name: string } | null>(null)
const deleteError = ref('')
const appNotice = ref('')
const appError = ref('')
const appSaving = ref(false)
const appForm = reactive<AppSettings>({
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
})

watchEffect(() => {
  if (!store.config) return
  Object.assign(appForm, store.config.app)
})

const isEditing = computed(() => Boolean(editingId.value))
const dialogTitle = computed(() => {
  const operation = isEditing.value ? '编辑' : '添加'
  return `${operation}${entityKind.value === 'target' ? '目标' : '上游'}实例`
})
const saveLabel = computed(() => `保存${entityKind.value === 'target' ? '目标' : '上游'}实例`)
const credentialLabel = computed(() => {
  if (entityType.value === 'generic') return 'API Key'
  if (entityType.value === 'cliproxyapi') return '管理密钥'
  return '访问令牌'
})

function activateSettingsTab(tab: SettingsTab, focus = false): void {
  activeSettingsTab.value = tab
  if (!focus) return
  void nextTick(() => {
    const button = tab === 'instances' ? instancesTabButton.value : runtimeTabButton.value
    button?.focus()
  })
}

function onSettingsTabKeydown(event: KeyboardEvent): void {
  let nextTab: SettingsTab | null = null
  if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
    nextTab = activeSettingsTab.value === 'instances' ? 'runtime' : 'instances'
  } else if (event.key === 'Home') {
    nextTab = 'instances'
  } else if (event.key === 'End') {
    nextTab = 'runtime'
  }
  if (!nextTab) return
  event.preventDefault()
  activateSettingsTab(nextTab, true)
}

function resetEntityForm(): void {
  editingId.value = ''
  entityId.value = ''
  entityName.value = ''
  entityType.value = 'newapi'
  baseUrl.value = ''
  userId.value = ''
  credential.value = ''
  formError.value = ''
}

function openAdd(kind: EntityKind): void {
  resetEntityForm()
  entityKind.value = kind
  formOpen.value = true
}

function openEdit(kind: EntityKind, item: TargetConfig | UpstreamConfig): void {
  resetEntityForm()
  entityKind.value = kind
  editingId.value = item.id
  entityId.value = item.id
  entityName.value = item.name
  entityType.value = item.type
  baseUrl.value = item.base_url
  userId.value = item.type === 'newapi' && item.user_id && item.user_id > 0 ? String(item.user_id) : ''
  formOpen.value = true
}

function closeForm(): void {
  credential.value = ''
  resetEntityForm()
  formOpen.value = false
}

function isAbsoluteHttpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return (url.protocol === 'http:' || url.protocol === 'https:') && Boolean(url.host)
  } catch {
    return false
  }
}

function credentialField(): 'access_token' | 'management_key' | 'api_key' {
  if (entityType.value === 'generic') return 'api_key'
  if (entityType.value === 'cliproxyapi') return 'management_key'
  return 'access_token'
}

function newApiUserId(): number | null {
  const value = String(userId.value).trim()
  if (!value) return 0
  const parsed = Number(value)
  if (!/^\d+$/.test(value) || !Number.isSafeInteger(parsed) || parsed < 1) return null
  return parsed
}

async function submitEntity(): Promise<void> {
  if (!entityId.value.trim() || !entityName.value.trim()) {
    formError.value = '实例 ID 和名称不能为空'
    return
  }
  if (!isAbsoluteHttpUrl(baseUrl.value.trim())) {
    formError.value = '请输入绝对 HTTP(S) 地址'
    return
  }
  if (!isEditing.value && !credential.value) {
    formError.value = `${credentialLabel.value}不能为空`
    return
  }
  const submittedUserId = entityType.value === 'newapi' ? newApiUserId() : undefined
  if (submittedUserId === null) {
    formError.value = 'New API 用户 ID 必须为正整数'
    return
  }

  const payload: Record<string, unknown> = {
    name: entityName.value.trim(),
    base_url: baseUrl.value.trim().replace(/\/+$/, ''),
  }
  if (!isEditing.value) {
    payload.id = entityId.value.trim()
    payload.type = entityType.value
  }
  if (submittedUserId !== undefined) payload.user_id = submittedUserId
  if (credential.value) payload[credentialField()] = credential.value

  credential.value = ''
  saving.value = true
  formError.value = ''
  try {
    if (entityKind.value === 'target') {
      const target = isEditing.value
        ? await api.updateTarget(editingId.value, payload)
        : await api.createTarget(payload)
      await store.upsertTarget(target)
    } else {
      const upstream = isEditing.value
        ? await api.updateUpstream(editingId.value, payload)
        : await api.createUpstream(payload)
      await store.upsertUpstream(upstream)
    }
    closeForm()
  } catch (error) {
    formError.value = safeErrorMessage(error)
  } finally {
    saving.value = false
  }
}

function askDelete(kind: EntityKind, id: string, name: string): void {
  deleteItem.value = { kind, id, name }
  deleteError.value = ''
}

function closeDelete(): void {
  if (deleting.value) return
  deleteItem.value = null
}

async function confirmDelete(): Promise<void> {
  if (!deleteItem.value || deleting.value) return
  const item = deleteItem.value
  deleting.value = true
  deleteError.value = ''
  try {
    if (item.kind === 'target') {
      await api.deleteTarget(item.id)
      await store.removeTarget(item.id)
    } else {
      await api.deleteUpstream(item.id)
      await store.removeUpstream(item.id)
    }
    if (deleteItem.value === item) deleteItem.value = null
  } catch (error) {
    if (deleteItem.value === item) deleteError.value = safeErrorMessage(error)
  } finally {
    deleting.value = false
  }
}

async function saveAppSettings(): Promise<void> {
  if (!appForm.host.trim() || appForm.port < 1 || appForm.port > 65535) {
    appError.value = '监听地址或端口无效'
    return
  }
  if (appForm.sync_concurrency < 1) {
    appError.value = '同步并发数至少为 1'
    return
  }
  appSaving.value = true
  appError.value = ''
  appNotice.value = ''
  try {
    const settings = await api.updateApp({ ...appForm, host: appForm.host.trim() })
    store.replaceAppSettings(settings)
    appNotice.value = '运行设置已保存'
  } catch (error) {
    appError.value = safeErrorMessage(error)
  } finally {
    appSaving.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="settings-heading">
    <header class="page-header">
      <div>
        <h1 id="settings-heading" aria-label="设置">系统设置</h1>
      </div>
    </header>

    <div class="settings-tabs" role="tablist" aria-label="设置分类">
      <button
        id="settings-instances-tab"
        ref="instancesTabButton"
        class="settings-tab"
        :class="{ active: activeSettingsTab === 'instances' }"
        type="button"
        role="tab"
        :tabindex="activeSettingsTab === 'instances' ? 0 : -1"
        :aria-selected="activeSettingsTab === 'instances'"
        aria-controls="settings-instances-panel"
        @click="activateSettingsTab('instances')"
        @keydown="onSettingsTabKeydown"
      >
        实例管理
      </button>
      <button
        id="settings-runtime-tab"
        ref="runtimeTabButton"
        class="settings-tab"
        :class="{ active: activeSettingsTab === 'runtime' }"
        type="button"
        role="tab"
        :tabindex="activeSettingsTab === 'runtime' ? 0 : -1"
        :aria-selected="activeSettingsTab === 'runtime'"
        aria-controls="settings-runtime-panel"
        @click="activateSettingsTab('runtime')"
        @keydown="onSettingsTabKeydown"
      >
        运行参数
      </button>
    </div>

    <div
      v-show="activeSettingsTab === 'instances'"
      id="settings-instances-panel"
      class="settings-tabpanel"
      role="tabpanel"
      :hidden="activeSettingsTab !== 'instances'"
      aria-labelledby="settings-instances-tab"
    >
      <section class="settings-surface" aria-label="实例与连接配置">
        <div class="settings-grid">
          <section class="settings-band" aria-labelledby="targets-settings-heading">
            <header class="section-header">
              <div>
                <h2 id="targets-settings-heading">目标实例</h2>
                <span>{{ store.targets.length }} 个</span>
              </div>
              <button class="secondary-button" type="button" aria-label="添加目标实例" @click="openAdd('target')">
                <Plus :size="16" aria-hidden="true" />
                添加目标
              </button>
            </header>
            <div v-if="store.targets.length === 0" class="inline-empty">暂无目标实例</div>
            <div v-else class="instance-list">
              <div v-for="target in store.targets" :key="target.id" class="instance-row">
                <div class="instance-primary">
                  <strong>{{ target.name }}</strong>
                  <small>{{ target.id }} / {{ target.type }}</small>
                  <small v-if="target.type === 'newapi' && target.user_id && target.user_id > 0">
                    用户 ID {{ target.user_id }}
                  </small>
                </div>
                <code>{{ target.base_url }}</code>
                <div class="row-actions">
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :aria-label="`编辑目标实例 ${target.name}`"
                    title="编辑目标实例"
                    @click="openEdit('target', target)"
                  >
                    <Pencil :size="16" aria-hidden="true" />
                  </button>
                  <button
                    class="icon-button icon-button-small danger-icon"
                    type="button"
                    :aria-label="`删除目标实例 ${target.name}`"
                    title="删除目标实例"
                    @click="askDelete('target', target.id, target.name)"
                  >
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </div>
              </div>
            </div>
          </section>

          <section class="settings-band" aria-labelledby="upstreams-settings-heading">
            <header class="section-header">
              <div>
                <h2 id="upstreams-settings-heading">上游实例</h2>
                <span>{{ store.upstreams.length }} 个</span>
              </div>
              <button class="secondary-button" type="button" aria-label="添加上游实例" @click="openAdd('upstream')">
                <Plus :size="16" aria-hidden="true" />
                添加上游
              </button>
            </header>
            <div v-if="store.upstreams.length === 0" class="inline-empty">暂无上游实例</div>
            <div v-else class="instance-list">
              <div v-for="source in store.upstreams" :key="source.id" class="instance-row">
                <div class="instance-primary">
                  <strong>{{ source.name }}</strong>
                  <small>{{ source.id }} / {{ source.type }}</small>
                  <small v-if="source.type === 'newapi' && source.user_id && source.user_id > 0">
                    用户 ID {{ source.user_id }}
                  </small>
                </div>
                <code>{{ source.base_url }}</code>
                <div class="row-actions">
                  <button
                    class="icon-button icon-button-small"
                    type="button"
                    :aria-label="`编辑上游实例 ${source.name}`"
                    title="编辑上游实例"
                    @click="openEdit('upstream', source)"
                  >
                    <Pencil :size="16" aria-hidden="true" />
                  </button>
                  <button
                    class="icon-button icon-button-small danger-icon"
                    type="button"
                    :aria-label="`删除上游实例 ${source.name}`"
                    title="删除上游实例"
                    @click="askDelete('upstream', source.id, source.name)"
                  >
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </div>
              </div>
            </div>
          </section>
        </div>
      </section>
    </div>

    <div
      v-show="activeSettingsTab === 'runtime'"
      id="settings-runtime-panel"
      class="settings-tabpanel"
      role="tabpanel"
      :hidden="activeSettingsTab !== 'runtime'"
      aria-labelledby="settings-runtime-tab"
    >
      <section class="settings-band settings-surface" aria-labelledby="runtime-settings-heading">
        <header class="section-header">
          <div>
            <h2 id="runtime-settings-heading">运行设置</h2>
          </div>
        </header>
        <form class="runtime-form" @submit.prevent="saveAppSettings">
          <label class="field">
            <span>监听地址</span>
            <input v-model="appForm.host" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>端口</span>
            <input v-model.number="appForm.port" type="number" min="1" max="65535" />
          </label>
          <label class="field">
            <span>校验间隔</span>
            <input v-model="appForm.reconcile_interval" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>请求超时</span>
            <input v-model="appForm.request_timeout" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>同步并发</span>
            <input v-model.number="appForm.sync_concurrency" type="number" min="1" />
          </label>
          <button class="primary-button runtime-save" type="submit" :disabled="appSaving">
            <Save :size="16" aria-hidden="true" />
            {{ appSaving ? '保存中' : '保存运行设置' }}
          </button>
        </form>
        <p v-if="appNotice" class="notice notice-success" role="status">{{ appNotice }}</p>
        <p v-if="appError" class="notice notice-error" role="alert">{{ appError }}</p>
      </section>
    </div>

    <ModalDialog v-if="formOpen" :title="dialogTitle" :close-label="`关闭${dialogTitle}`" @close="closeForm">
      <form class="form-stack" @submit.prevent="submitEntity">
        <div class="form-grid">
          <label class="field">
            <span>实例 ID</span>
            <input v-model="entityId" type="text" autocomplete="off" :disabled="isEditing" />
          </label>
          <label class="field">
            <span>名称</span>
            <input v-model="entityName" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>平台类型</span>
            <select v-model="entityType" :disabled="isEditing">
              <option value="newapi">New API</option>
              <option v-if="entityKind === 'target'" value="cliproxyapi">CLIProxyAPI</option>
              <option v-if="entityKind === 'upstream'" value="generic">通用 API</option>
            </select>
          </label>
          <label class="field field-wide">
            <span>Base URL</span>
            <input v-model="baseUrl" type="text" inputmode="url" autocomplete="off" />
          </label>
          <label v-if="entityType === 'newapi'" class="field field-wide">
            <span>New API 用户 ID</span>
            <input
              v-model="userId"
              type="number"
              min="1"
              step="1"
              inputmode="numeric"
              autocomplete="off"
            />
          </label>
          <label class="field field-wide">
            <span>{{ credentialLabel }}</span>
            <input
              v-model="credential"
              type="password"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
            />
          </label>
        </div>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <footer class="form-actions">
          <button class="secondary-button" type="button" @click="closeForm">取消</button>
          <button class="primary-button" type="submit" :disabled="saving">
            <Save :size="16" aria-hidden="true" />
            {{ saving ? '保存中' : saveLabel }}
          </button>
        </footer>
      </form>
    </ModalDialog>

    <ModalDialog v-if="deleteItem" title="删除实例" close-label="关闭删除确认" @close="closeDelete">
      <p>确定删除“{{ deleteItem.name }}”吗？有关联映射时服务端会拒绝此操作。</p>
      <p v-if="deleteError" class="form-error" role="alert">{{ deleteError }}</p>
      <footer class="form-actions">
        <button class="secondary-button" type="button" :disabled="deleting" @click="closeDelete">取消</button>
        <button class="danger-button" type="button" :disabled="deleting" @click="confirmDelete">
          {{ deleting ? '删除中' : '确认删除' }}
        </button>
      </footer>
    </ModalDialog>
  </section>
</template>

<style scoped>
.settings-tabs {
  margin-bottom: 0;
}

.settings-surface {
  min-width: 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
}

.settings-grid {
  display: block;
}

.settings-band {
  margin: 0;
  padding: 0 12px 12px;
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 0;
  box-shadow: none;
}

.settings-band:last-child {
  border-bottom: 0;
}

.settings-band.settings-surface {
  padding: 0 12px 14px;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.section-header {
  min-height: 50px;
}

.instance-row {
  min-height: 54px;
}

.runtime-form {
  padding: 10px 0 0;
}

@media (max-width: 620px) {
  .settings-band,
  .settings-band.settings-surface {
    padding-right: 8px;
    padding-left: 8px;
  }

  .section-header {
    align-items: flex-start;
    padding: 8px 0;
  }

  .instance-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .runtime-form {
    grid-template-columns: 1fr;
  }
}
</style>
