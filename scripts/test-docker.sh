#!/bin/sh

set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

fail() {
	printf 'docker contract failed: %s\n' "$*" >&2
	exit 1
}

assert_file() {
	[ -f "$repo_root/$1" ] || fail "missing $1"
}

assert_contains() {
	file=$1
	needle=$2
	grep -Fq -- "$needle" "$repo_root/$file" || fail "$file must contain: $needle"
}

assert_not_contains() {
	file=$1
	needle=$2
	if grep -Fq -- "$needle" "$repo_root/$file"; then
		fail "$file must not contain: $needle"
	fi
}

assert_file Dockerfile
assert_file docker-compose.yml
assert_file .dockerignore
assert_file docker-entrypoint.sh

assert_contains Dockerfile 'FROM node:20.19.0-alpine3.21 AS web-builder'
assert_contains Dockerfile 'FROM golang:1.24.0-alpine3.21 AS go-builder'
assert_contains Dockerfile 'FROM alpine:3.21'
assert_contains Dockerfile 'USER synchub'
assert_contains Dockerfile 'EXPOSE 8888'
assert_contains Dockerfile 'ENTRYPOINT ["/usr/local/bin/docker-entrypoint"]'
assert_not_contains Dockerfile 'COPY data/config.yaml'
assert_contains .dockerignore 'data/config.yaml'
assert_contains docker-entrypoint.sh 'su-exec synchub /usr/local/bin/sync-hub "$@"'

assert_contains docker-compose.yml '127.0.0.1:8888:8888'
assert_contains docker-compose.yml 'synchub_data:/data'
assert_contains docker-compose.yml 'read_only: true'
assert_contains docker-compose.yml 'no-new-privileges:true'
assert_contains README.md 'docker compose up -d --build'

compose_output=$(cd "$repo_root" && docker compose config)
case "$compose_output" in
	*'host_ip: 127.0.0.1'*'target: 8888'*'source: synchub_data'*'target: /data'*) ;;
	*) fail 'normalized Compose configuration lost local port or persistent data volume' ;;
esac

printf 'docker contract: PASS\n'
