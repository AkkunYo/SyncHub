import { Buffer } from 'node:buffer'
import { execFileSync, spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath, URL } from 'node:url'

const fixturePort = 19090
const syncHubPort = 18888
const fixtureHost = `http://127.0.0.1:${fixturePort}`
const currentDirectory = dirname(fileURLToPath(import.meta.url))
const repositoryRoot = resolve(currentDirectory, '../..')
const temporaryRoot = await mkdtemp(join(tmpdir(), 'synchub-browser-e2e-'))
const configPath = join(temporaryRoot, 'config.yaml')
const binaryPath = join(temporaryRoot, 'sync-hub-e2e')

const sourceToken = 'E2E_SOURCE_ADMIN_TOKEN_PLACEHOLDER'
const sourceUserId = 9001
const targetTokens = {
  'target-a': 'E2E_TARGET_A_ADMIN_TOKEN_PLACEHOLDER',
  'target-b': 'E2E_TARGET_B_ADMIN_TOKEN_PLACEHOLDER',
}
const targetUserIds = {
  'target-a': 9101,
  'target-b': 9102,
}
const sourceKey = 'E2E_UPSTREAM_KEY_PLACEHOLDER'

const targets = {
  'target-a': { nextId: 101, channels: new Map() },
  'target-b': { nextId: 201, channels: new Map() },
}

function json(response, status, body) {
  const encoded = JSON.stringify(body)
  response.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(encoded),
  })
  response.end(encoded)
}

async function readJSON(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  return JSON.parse(Buffer.concat(chunks).toString('utf8'))
}

function publicChannel(channel) {
  return {
    id: channel.id,
    type: channel.type,
    name: channel.name,
    status: channel.status,
    base_url: channel.base_url,
    models: channel.models,
    group: channel.group,
    priority: channel.priority,
    weight: channel.weight,
    channel_info: { is_multi_key: false },
  }
}

function channelList(response, channels, requestURL) {
  const page = Number(requestURL.searchParams.get('p') ?? '1')
  const pageSize = Number(requestURL.searchParams.get('page_size') ?? '100')
  json(response, 200, {
    success: true,
    data: { items: channels.map(publicChannel), total: channels.length, page, page_size: pageSize },
  })
}

function authorized(request, expectedToken, expectedUserId) {
  return request.headers.authorization === `Bearer ${expectedToken}` &&
    request.headers['new-api-user'] === String(expectedUserId)
}

const fixtureServer = createServer(async (request, response) => {
  try {
    const requestURL = new URL(request.url ?? '/', fixtureHost)
    const parts = requestURL.pathname.split('/').filter(Boolean)

    if (parts[0] === '__control') {
      const target = targets[parts[1]]
      const channelId = Number(parts[3])
      const channel = target?.channels.get(channelId)
      if (!target || parts[2] !== 'channels' || !channel) {
        json(response, 404, { ok: false })
        return
      }
      if (request.method === 'POST' && parts[4] === 'drift') {
        channel.weight = 61
        channel.models = 'gpt-4.1,gpt-4.1-mini'
        json(response, 200, { ok: true })
        return
      }
      if (request.method === 'DELETE' && parts.length === 4) {
        target.channels.delete(channelId)
        json(response, 200, { ok: true })
        return
      }
      json(response, 404, { ok: false })
      return
    }

    if (parts[0] === 'source') {
      if (!authorized(request, sourceToken, sourceUserId)) {
        json(response, 401, { success: false })
        return
      }
      if (request.method === 'GET' && requestURL.pathname === '/source/api/user/self') {
        json(response, 200, { success: true, data: { role: 1, group: 'default' } })
        return
      }
      if (request.method === 'GET' && requestURL.pathname === '/source/api/token/') {
        const page = Number(requestURL.searchParams.get('p') ?? '1')
        const items = page === 1 ? [{
          id: 42,
          name: 'E2E upstream key',
          key: 'sk-e2e****fixture',
          status: 1,
          group: 'default',
          remain_quota: 1000000,
          used_quota: 0,
          unlimited_quota: false,
          expired_time: 4102444800,
          model_limits_enabled: true,
          model_limits: 'gpt-4.1',
        }] : []
        json(response, 200, { success: true, data: { page, page_size: 100, total: 1, items } })
        return
      }
      if (request.method === 'GET' && requestURL.pathname === '/source/api/user/self/groups') {
        json(response, 200, { success: true, data: { default: { ratio: 1, desc: 'Default' } } })
        return
      }
      if (request.method === 'GET' && requestURL.pathname === '/source/api/user/models' && requestURL.searchParams.get('group') === 'default') {
        json(response, 200, { success: true, data: ['gpt-4.1'] })
        return
      }
      if (request.method === 'POST' && requestURL.pathname === '/source/api/token/batch/keys') {
        const body = await readJSON(request)
        if (!Array.isArray(body.ids) || body.ids.length !== 1 || body.ids[0] !== 42) {
          json(response, 400, { success: false })
          return
        }
        json(response, 200, { success: true, data: { keys: { 42: sourceKey } } })
        return
      }
    }

    const targetName = parts[0]
    const target = targets[targetName]
    if (target) {
      if (!authorized(request, targetTokens[targetName], targetUserIds[targetName])) {
        json(response, 401, { success: false })
        return
      }
      if (request.method === 'GET' && requestURL.pathname === `/${targetName}/api/channel/`) {
        channelList(response, [...target.channels.values()], requestURL)
        return
      }
      if (request.method === 'POST' && requestURL.pathname === `/${targetName}/api/channel/`) {
        const body = await readJSON(request)
        const id = target.nextId
        target.nextId += 1
        const channel = {
          id,
          type: body.channel.type,
          name: body.channel.name,
          status: body.channel.status,
          base_url: body.channel.base_url,
          models: body.channel.models,
          group: body.channel.group,
          priority: body.channel.priority,
          weight: body.channel.weight,
        }
        target.channels.set(id, channel)
        json(response, 200, { success: true, data: { id } })
        return
      }
      if (request.method === 'PUT' && requestURL.pathname === `/${targetName}/api/channel/`) {
        const body = await readJSON(request)
        const channel = target.channels.get(body.id)
        if (!channel) {
          json(response, 404, { success: false })
          return
        }
        Object.assign(channel, {
          name: body.name,
          base_url: body.base_url,
          models: body.models,
          group: body.group,
          priority: body.priority,
          weight: body.weight,
        })
        json(response, 200, { success: true, data: publicChannel(channel) })
        return
      }
      const channelId = Number(parts[3])
      const channel = target.channels.get(channelId)
      if (request.method === 'GET' && parts.length === 4 && channel) {
        json(response, 200, { success: true, data: publicChannel(channel) })
        return
      }
      if (request.method === 'POST' && parts[4] === 'status' && channel) {
        const body = await readJSON(request)
        channel.status = body.status
        json(response, 200, { success: true, data: true })
        return
      }
      if (request.method === 'DELETE' && parts.length === 4 && channel) {
        target.channels.delete(channelId)
        json(response, 200, { success: true, data: true })
        return
      }
    }

    json(response, 404, { success: false })
  } catch {
    json(response, 500, { success: false })
  }
})

await new Promise((resolveListen, rejectListen) => {
  fixtureServer.once('error', rejectListen)
  fixtureServer.listen(fixturePort, '127.0.0.1', resolveListen)
})

await writeFile(configPath, `app:
  host: 127.0.0.1
  port: ${syncHubPort}
  reconcile_interval: 1h
  request_timeout: 5s
  sync_concurrency: 2
targets: []
upstreams: []
`, { mode: 0o600 })

execFileSync('go', ['build', '-trimpath', '-o', binaryPath, './cmd/sync-hub'], {
  cwd: repositoryRoot,
  stdio: 'inherit',
})

const syncHub = spawn(binaryPath, ['-config', configPath, '-listen', `127.0.0.1:${syncHubPort}`], {
  cwd: repositoryRoot,
  stdio: 'inherit',
})

let stopping = false
async function stop(exitCode = 0) {
  if (stopping) return
  stopping = true
  syncHub.kill('SIGTERM')
  fixtureServer.close()
  await rm(temporaryRoot, { recursive: true, force: true })
  process.exit(exitCode)
}

syncHub.once('exit', (code, signal) => {
  if (!stopping) void stop(code ?? (signal ? 1 : 0))
})
process.once('SIGINT', () => void stop())
process.once('SIGTERM', () => void stop())
