# SyncHub

SyncHub 是面向 AI API 分发系统的资产与渠道同步中枢。它以单个 Go 进程运行，通过平台公开 API 发现上游资产、批量下发目标渠道，并定期检查渠道丢失和配置漂移。前端已嵌入二进制，运行时只需本地 YAML 配置，不依赖数据库或 Node.js。

## 主要功能

- 统一展示上游资产到多个目标实例的同步矩阵。
- 批量下发模型、分组、优先级和权重配置，并独立展示每个目标的结果。
- 管理 New API 和 CLIProxyAPI 目标渠道。
- 定期发现目标渠道删除和模型、分组、优先级、权重漂移。
- 使用本地 YAML 原子保存平台配置与同步映射。

## 支持平台

| 平台 | 上游资产源 | 目标分发平台 |
| --- | :---: | :---: |
| New API | 支持 | 支持 |
| CLIProxyAPI（CPA） | 支持 | 支持 |
| Sub2Api | 支持 | - |

New API 配置中的 `access_token` 是管理员/Root 控制台访问令牌，不是用于模型调用的 API Key。要求 `New-Api-User` 的旧版实例还需配置 Token 所属用户的正整数 `user_id`；读取渠道秘密时，上游 New API 仍可能要求当次 Root 安全证明。

CLIProxyAPI 上游使用 `management_key` 读取管理元数据。需要把 CPA 作为 OpenAI-compatible 代理资产下发时，另行配置专用的 `proxy_api_key`；该值不能复用管理密钥。

## 使用方法

### 准备配置

从完整示例创建本地配置，然后替换 URL 和所有 `REPLACE_WITH_...` 占位符：

```bash
cp data/config.example.yaml data/config.yaml
chmod 600 data/config.yaml
go run ./scripts/validate-config data/config.yaml
```

`make validate-config` 用于检查仓库中的 `data/config.example.yaml`。

### 运行发布二进制

将对应平台的发布二进制与 `data/config.yaml` 放在同一工作目录，然后启动：

```bash
./sync-hub
```

Windows PowerShell：

```powershell
.\sync-hub.exe
```

默认读取 `data/config.yaml`，并仅监听 `127.0.0.1:8888`。启动后在浏览器访问 `http://127.0.0.1:8888`。

### 从源码构建

需要 Go 1.24 或更高版本、Node.js 20.19 或更高版本以及 npm。

```bash
make build
./build/sync-hub
```

`make build` 使用 `npm ci` 安装锁定的前端依赖，完成覆盖率、类型、lint、真实 Playwright E2E、依赖安全、生产构建与 Go 检查后，生成 `build/sync-hub`。

生成 macOS、Linux 和 Windows 的 amd64/arm64 发布二进制：

```bash
VERSION=v0.1.0 \
COMMIT="$(git rev-parse --short=12 HEAD)" \
BUILD_DATE="$(git show -s --format=%cI HEAD)" \
make release
```

产物位于 `build/release/`。`VERSION`、`COMMIT` 和 `BUILD_DATE` 会注入二进制；未显式传入时，构建脚本使用当前 Git 提交的稳定元数据。

## 安全注意事项

- 只在 `data/config.yaml` 中保存管理员凭证；该文件已被 Git 忽略，且应保持 `0600` 权限。
- 为 SyncHub 创建权限最小化的专用凭证，不要将 Token、API Key 或安全证明放入命令行、日志、Issue 或提交记录。
- 不要直接将管理端口暴露到公网。需要远程访问时，应使用带身份认证和 HTTPS 的受信反向代理。

## 许可证

本项目采用 [Apache License 2.0](LICENSE) 许可。
