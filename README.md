# SyncHub

SyncHub is a self-hosted control plane for synchronizing AI API assets to multiple target platforms. It discovers supported upstream assets through public APIs, creates and reconciles target channels, and stores configuration and mappings in a local YAML file. The web console is embedded in the Go binary; no database or Node.js runtime is required after building.

## Supported platforms

| Role | Supported type | Authentication model |
| --- | --- | --- |
| Upstream | New API | Ordinary-user access token; SyncHub reads only that user's tokens, groups, and models. |
| Upstream | `generic` OpenAI-compatible endpoint | Base URL and shared API key; SyncHub requests only the standard model-list endpoint. |
| Target | New API | Administrator access token. |
| Target | CLIProxyAPI | Administrator management key. |

New API upstreams only support ordinary-user token discovery. `auto`, `channel`, administrator/root discovery, and specialized CPA or Sub2Api upstream configurations are rejected. A service that exposes an OpenAI-compatible URL and shared API key is configured as `generic` regardless of the product behind it.

## Run with Docker Compose

Docker Compose is the recommended local runtime. It binds the management UI only to `127.0.0.1:8888` and persists configuration in the named volume `synchub_data`.

```bash
docker compose up -d --build
docker compose logs -f sync-hub
```

Open `http://127.0.0.1:8888` and configure targets and upstreams in the console. The first start creates an empty default configuration in the volume. Stop the local stack without deleting configuration:

```bash
docker compose down
```

To discard all Compose-managed configuration, run `docker compose down -v`; this deletes the named data volume.

## Run a release binary

Create a local configuration from the example and replace every `REPLACE_WITH_...` value:

```bash
cp data/config.example.yaml data/config.yaml
chmod 600 data/config.yaml
go run ./scripts/validate-config data/config.yaml
./sync-hub
```

The default listener is `127.0.0.1:8888`. Windows users can start `sync-hub.exe` from PowerShell.

## Build from source

Building from source requires Go 1.24+, Node.js 20.19+, and npm:

```bash
make build
./build/sync-hub
```

`make release` creates macOS, Linux, and Windows amd64/arm64 binaries in `build/release/`.

## Security

- Keep `data/config.yaml` private; it is Git-ignored and should be mode `0600`.
- Do not put tokens or API keys in source code, command lines, logs, issues, or commits.
- The Compose service runs the application as an unprivileged user, uses a read-only root filesystem, and keeps mutable state only in `/data`.
- Do not expose the management port directly to the public Internet. Use an authenticated HTTPS reverse proxy when remote access is required.

## License

Apache-2.0. See [LICENSE](LICENSE).
