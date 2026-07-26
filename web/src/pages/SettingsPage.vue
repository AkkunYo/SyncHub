<script setup lang="ts">
import { computed, reactive, ref, watch, watchEffect } from 'vue'
import { Pencil, Plus, Save, Trash2 } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import { useConsoleStore } from '@/stores/console'
import type { AppSettings, PlatformType, TargetConfig, UpstreamConfig } from '@/types'

type EntityKind = 'target' | 'upstream'

const store = useConsoleStore()
const formOpen = ref(false)
const entityKind = ref<EntityKind>('target')
const editingId = ref('')
const entityId = ref('')
const entityName = ref('')
const entityType = ref<PlatformType>('newapi')
const baseUrl = ref('')
const userId = ref<string | number>('')
const credential = ref('')
const proxyApiKey = ref('')
const clearProxyApiKey = ref(false)
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
const isCPAUpstream = computed(
  () => entityKind.value === 'upstream' && entityType.value === 'cliproxyapi',
)
const credentialLabel = computed(() => {
  if (entityType.value === 'cliproxyapi') return '管理密钥'
  if (entityType.value === 'sub2api') return 'API Key'
  return '访问令牌'
})

function resetProxyApiKeyFields(): void {
  proxyApiKey.value = ''
  clearProxyApiKey.value = false
}

watch(isCPAUpstream, (visible) => {
  if (!visible) resetProxyApiKeyFields()
})

function resetEntityForm(): void {
  editingId.value = ''
  entityId.value = ''
  entityName.value = ''
  entityType.value = 'newapi'
  baseUrl.value = ''
  userId.value = ''
  credential.value = ''
  resetProxyApiKeyFields()
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
  if (entityType.value === 'cliproxyapi') return 'management_key'
  if (entityType.value === 'sub2api') return 'api_key'
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
  if (isCPAUpstream.value) {
    if (clearProxyApiKey.value) payload.proxy_api_key = ''
    else if (proxyApiKey.value) payload.proxy_api_key = proxyApiKey.value
  }

  credential.value = ''
  resetProxyApiKeyFields()
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

async function confirmDelete(): Promise<void> {
  if (!deleteItem.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    if (deleteItem.value.kind === 'target') {
      await api.deleteTarget(deleteItem.value.id)
      await store.removeTarget(deleteItem.value.id)
    } else {
      await api.deleteUpstream(deleteItem.value.id)
      await store.removeUpstream(deleteItem.value.id)
    }
    deleteItem.value = null
  } catch (error) {
    deleteError.value = safeErrorMessage(error)
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
        <p class="eyebrow">平台配置</p>
        <h1 id="settings-heading">实例与运行设置</h1>
      </div>
    </header>

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

    <section class="settings-band" aria-labelledby="runtime-settings-heading">
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
              <option value="cliproxyapi">CLIProxyAPI</option>
              <option v-if="entityKind === 'upstream'" value="sub2api">Sub2Api</option>
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
          <template v-if="isCPAUpstream">
            <label class="field field-wide">
              <span>代理 API Key（可选）</span>
              <input
                v-model="proxyApiKey"
                type="password"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                :disabled="clearProxyApiKey"
              />
            </label>
            <label v-if="isEditing" class="check-row field-wide">
              <input
                v-model="clearProxyApiKey"
                type="checkbox"
                @change="clearProxyApiKey && (proxyApiKey = '')"
              />
              <span>清除已保存的代理 API Key</span>
            </label>
          </template>
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

    <ModalDialog v-if="deleteItem" title="删除实例" close-label="关闭删除确认" @close="deleteItem = null">
      <p>确定删除“{{ deleteItem.name }}”吗？有关联映射时服务端会拒绝此操作。</p>
      <p v-if="deleteError" class="form-error" role="alert">{{ deleteError }}</p>
      <footer class="form-actions">
        <button class="secondary-button" type="button" @click="deleteItem = null">取消</button>
        <button class="danger-button" type="button" :disabled="deleting" @click="confirmDelete">
          {{ deleting ? '删除中' : '确认删除' }}
        </button>
      </footer>
    </ModalDialog>
  </section>
</template>
