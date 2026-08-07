<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RotateCcw, Save, TriangleAlert } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { AppSettings } from '@/types'

const store = useConsoleStore()
const notice = ref('')
const error = ref('')
const saving = ref(false)
const defaultSettings: AppSettings = {
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
}
const form = reactive<AppSettings>({ ...defaultSettings })
const savedSettings = ref<AppSettings>({ ...defaultSettings })

const isDirty = computed(() => (
  form.host !== savedSettings.value.host
  || form.port !== savedSettings.value.port
  || form.reconcile_interval !== savedSettings.value.reconcile_interval
  || form.request_timeout !== savedSettings.value.request_timeout
  || form.sync_concurrency !== savedSettings.value.sync_concurrency
))

watch(
  () => store.config?.app,
  (settings) => {
    if (!settings) return
    savedSettings.value = { ...settings }
    Object.assign(form, settings)
  },
  { deep: true, immediate: true },
)

function resetRuntimeSettings(): void {
  Object.assign(form, savedSettings.value)
  notice.value = ''
  error.value = ''
}

async function saveRuntimeSettings(): Promise<void> {
  if (!isDirty.value || saving.value) return
  if (!form.host.trim() || form.port < 1 || form.port > 65535) {
    error.value = '监听地址或端口无效'
    return
  }
  if (form.sync_concurrency < 1) {
    error.value = '同步并发数至少为 1'
    return
  }

  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const settings = await api.updateApp({ ...form, host: form.host.trim() })
    store.replaceAppSettings(settings)
    savedSettings.value = { ...settings }
    Object.assign(form, settings)
    notice.value = '运行设置已保存'
  } catch (reason) {
    error.value = safeErrorMessage(reason)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="settings-heading">
    <header class="page-header">
      <div>
        <h1 id="settings-heading" aria-label="设置">系统设置</h1>
        <p>调整 SyncHub 的网络入口与后台任务运行参数。</p>
      </div>
    </header>

    <form class="settings-form" @submit.prevent="saveRuntimeSettings">
      <section class="settings-groups" aria-label="运行参数">
        <section class="settings-band settings-surface" aria-labelledby="network-settings-heading">
          <header class="section-header">
            <div>
              <h2 id="network-settings-heading">网络监听</h2>
              <p>控制管理界面与本地管理 API 的监听入口。</p>
            </div>
          </header>

          <div class="settings-field-grid">
            <div class="field">
              <label for="settings-host">监听地址</label>
              <input
                id="settings-host"
                v-model="form.host"
                type="text"
                autocomplete="off"
                aria-describedby="settings-host-help"
              />
              <small id="settings-host-help">使用 127.0.0.1 仅允许本机访问，0.0.0.0 可监听所有网卡。</small>
            </div>
            <div class="field">
              <label for="settings-port">端口</label>
              <div class="input-with-unit">
                <input
                  id="settings-port"
                  v-model.number="form.port"
                  type="number"
                  min="1"
                  max="65535"
                  aria-describedby="settings-port-help"
                />
                <span>TCP</span>
              </div>
              <small id="settings-port-help">管理界面和 API 对外提供服务的端口。</small>
            </div>
          </div>

          <p class="settings-warning">
            <TriangleAlert :size="16" aria-hidden="true" />
            修改监听地址或端口后，可能需要重启服务并重新连接。
          </p>
        </section>

        <section class="settings-band settings-surface" aria-labelledby="schedule-settings-heading">
          <header class="section-header">
            <div>
              <h2 id="schedule-settings-heading">任务调度</h2>
              <p>控制后台校验、上游请求与同步任务的执行节奏。</p>
            </div>
          </header>

          <p class="duration-help">支持 s、m、h 等时长单位，例如 5m0s。</p>
          <div class="settings-field-grid schedule-grid">
            <div class="field">
              <label for="settings-reconcile-interval">校验间隔</label>
              <input
                id="settings-reconcile-interval"
                v-model="form.reconcile_interval"
                type="text"
                autocomplete="off"
                aria-describedby="settings-reconcile-help"
              />
              <small id="settings-reconcile-help">自动检查目标配置漂移的时间间隔。</small>
            </div>
            <div class="field">
              <label for="settings-request-timeout">请求超时</label>
              <input
                id="settings-request-timeout"
                v-model="form.request_timeout"
                type="text"
                autocomplete="off"
                aria-describedby="settings-timeout-help"
              />
              <small id="settings-timeout-help">单次访问上游或目标平台允许等待的最长时间。</small>
            </div>
            <div class="field">
              <label for="settings-sync-concurrency">同步并发</label>
              <div class="input-with-unit">
                <input
                  id="settings-sync-concurrency"
                  v-model.number="form.sync_concurrency"
                  type="number"
                  min="1"
                  aria-describedby="settings-concurrency-help"
                />
                <span>个任务</span>
              </div>
              <small id="settings-concurrency-help">同时执行的同步操作数量，过高可能增加平台压力。</small>
            </div>
          </div>
        </section>
      </section>

      <div class="runtime-actions">
        <div class="settings-feedback" aria-live="polite">
          <p v-if="notice" class="notice notice-success" role="status">{{ notice }}</p>
          <p v-if="error" class="notice notice-error" role="alert">{{ error }}</p>
        </div>
        <div class="settings-action-buttons">
          <button
            class="secondary-button"
            type="button"
            :disabled="saving || !isDirty"
            @click="resetRuntimeSettings"
          >
            <RotateCcw :size="16" aria-hidden="true" />
            重置修改
          </button>
          <button
            class="primary-button"
            type="submit"
            aria-label="保存运行设置"
            :disabled="saving || !isDirty"
          >
            <Save :size="16" aria-hidden="true" />
            {{ saving ? '保存中' : '保存设置' }}
          </button>
        </div>
      </div>
    </form>
  </section>
</template>

<style scoped>
.page-header p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 12px;
}

.settings-form {
  width: 100%;
  max-width: 960px;
  margin: 0 auto;
}

.settings-groups {
  display: grid;
  gap: 16px;
}

.settings-band {
  margin: 0;
  padding: 0 16px 16px;
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

.settings-surface {
  min-width: 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
}

.section-header {
  display: flex;
  min-height: 58px;
  align-items: center;
  border-bottom: 1px solid var(--line);
}

.section-header h2 {
  margin: 0;
  font-size: 14px;
}

.section-header p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 11px;
}

.settings-field-grid {
  display: grid;
  padding-top: 16px;
  gap: 18px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.schedule-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.field {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: 7px;
}

.field label {
  color: var(--ink);
  font-size: 12px;
  font-weight: 650;
}

.field input {
  width: 100%;
  min-width: 0;
  min-height: 42px;
}

.field small,
.duration-help {
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.55;
}

.duration-help {
  padding-top: 14px;
}

.input-with-unit {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
}

.input-with-unit input {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

.input-with-unit > span {
  display: inline-flex;
  min-width: 54px;
  align-items: center;
  justify-content: center;
  padding: 0 10px;
  border: 1px solid var(--line-strong);
  border-left: 0;
  border-radius: 0 6px 6px 0;
  background: var(--surface-subtle, #f8fafc);
  color: var(--muted);
  font-size: 11px;
  white-space: nowrap;
}

.settings-warning {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 16px 0 0;
  padding: 10px 12px;
  border: 1px solid color-mix(in srgb, var(--amber, #b45309) 24%, var(--line));
  border-radius: 6px;
  background: color-mix(in srgb, var(--amber, #b45309) 6%, var(--surface));
  color: var(--muted);
  font-size: 11px;
  line-height: 1.55;
}

.settings-warning svg {
  flex: 0 0 auto;
  color: var(--amber, #b45309);
}

.runtime-actions {
  display: flex;
  min-height: 66px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-top: 16px;
  padding: 10px 0;
  border-top: 1px solid var(--line);
}

.settings-feedback {
  min-width: 0;
}

.settings-feedback .notice {
  margin: 0;
}

.settings-action-buttons {
  display: flex;
  flex: 0 0 auto;
  gap: 10px;
}

.settings-action-buttons button {
  min-height: 44px;
}

@media (max-width: 620px) {
  .settings-form {
    max-width: 100%;
  }

  .settings-band {
    padding-right: 10px;
    padding-left: 10px;
  }

  .settings-field-grid,
  .schedule-grid {
    grid-template-columns: 1fr;
  }

  .field input {
    min-height: 44px;
  }

  .runtime-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .settings-action-buttons {
    width: 100%;
  }

  .settings-action-buttons button {
    width: 100%;
  }
}
</style>
