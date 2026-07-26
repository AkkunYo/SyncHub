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

run_web() {
	printf '+ (cd web &&'
	printf ' %s' "$@"
	printf ')\n'
	if [ "$dry_run" -eq 0 ]; then
		(cd "$repo_root/web" && "$@")
	fi
}

run_web npm ci
run_web npm run test:coverage
run_web npm run type-check
run_web npm run lint
run_web npm run test:e2e
run_web npm audit --audit-level=low
run_web npm audit --omit=dev --audit-level=low
run_web npm run build
