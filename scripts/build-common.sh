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

resolve_metadata() {
	if [ -z "${VERSION:-}" ]; then
		VERSION=$(cd "$repo_root" && git describe --tags --always --dirty 2>/dev/null) || VERSION=dev
	fi
	if [ -z "${COMMIT:-}" ]; then
		COMMIT=$(cd "$repo_root" && git rev-parse --short=12 HEAD 2>/dev/null) || COMMIT=unknown
	fi
	if [ -z "${BUILD_DATE:-}" ]; then
		BUILD_DATE=$(cd "$repo_root" && git show -s --format=%cI HEAD 2>/dev/null) || BUILD_DATE=unknown
	fi

	validate_metadata version "$VERSION"
	validate_metadata commit "$COMMIT"
	validate_build_date "$BUILD_DATE"
	ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILD_DATE"
}

validate_metadata() {
	metadata_name=$1
	metadata_value=$2
	case "$metadata_value" in
		''|*[!A-Za-z0-9._+-]*)
			printf '%s contains unsupported characters\n' "$metadata_name" >&2
			exit 2
			;;
	esac
}

validate_build_date() {
	case "$1" in
		''|*[!A-Za-z0-9:._+-]*)
			printf 'build date contains unsupported characters\n' >&2
			exit 2
			;;
	esac
}

prepare_directory() {
	relative_path=$1
	printf '+ mkdir -p %s\n' "$relative_path"
	if [ "$dry_run" -eq 0 ]; then
		mkdir -p "$repo_root/$relative_path"
	fi
}

prepare_clean_directory() {
	relative_path=${1-}
	case "$relative_path" in
		'')
			printf 'clean directory path must not be empty\n' >&2
			return 2
			;;
		.)
			printf 'refusing to clean the repository root\n' >&2
			return 2
			;;
		/*)
			printf 'clean directory path must be relative\n' >&2
			return 2
			;;
		*..*)
			printf 'clean directory path must not contain ..\n' >&2
			return 2
			;;
		build/release) ;;
		*)
			printf 'clean directory path must be build/release\n' >&2
			return 2
			;;
	esac

	if [ "$dry_run" -eq 0 ]; then
		if [ -L "$repo_root/build" ] || [ -L "$repo_root/build/release" ]; then
			printf 'refusing to clean a symlinked release path\n' >&2
			return 2
		fi
	fi

	printf '+ rm -rf %s\n' "$relative_path"
	printf '+ mkdir -p %s\n' "$relative_path"
	if [ "$dry_run" -eq 0 ]; then
		rm -rf "$repo_root/build/release"
		mkdir -p "$repo_root/build/release"
	fi
}

copy_release_file() {
	source_path=$1
	destination_path=$2
	printf '+ cp %s %s\n' "$source_path" "$destination_path"
	if [ "$dry_run" -eq 0 ]; then
		cp "$repo_root/$source_path" "$repo_root/$destination_path"
	fi
}

run_host_build() {
	output_path=$1
	printf '+ CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "%s" -o %s ./cmd/sync-hub\n' "$ldflags" "$output_path"
	if [ "$dry_run" -eq 0 ]; then
		(
			cd "$repo_root"
			CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$output_path" ./cmd/sync-hub
		)
	fi
}

run_cross_build() {
	goos=$1
	goarch=$2
	output_path=$3
	printf '+ CGO_ENABLED=0 GOOS=%s GOARCH=%s go build -trimpath -buildvcs=false -ldflags "%s" -o %s ./cmd/sync-hub\n' \
		"$goos" "$goarch" "$ldflags" "$output_path"
	if [ "$dry_run" -eq 0 ]; then
		(
			cd "$repo_root"
			CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
				go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$output_path" ./cmd/sync-hub
		)
	fi
}
