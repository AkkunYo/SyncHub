#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
dry_run=${DRY_RUN:-0}

case "$dry_run" in
	0|1) ;;
	*)
		printf 'DRY_RUN must be 0 or 1\n' >&2
		exit 2
		;;
esac

run_go() {
	printf '+'
	printf ' %s' "$@"
	printf '\n'
	if [ "$dry_run" -eq 0 ]; then
		(cd "$repo_root" && "$@")
	fi
}

run_go go test ./...
run_go go test -race ./...
run_go go vet ./...
