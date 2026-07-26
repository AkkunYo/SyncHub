#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/build-common.sh"

resolve_metadata
"$script_dir/frontend.sh"
"$script_dir/go-check.sh"

release_dir=build/release
prepare_clean_directory "$release_dir"
copy_release_file data/config.example.yaml "$release_dir/config.example.yaml"
copy_release_file LICENSE "$release_dir/LICENSE"

for goos in darwin linux windows; do
	for goarch in amd64 arm64; do
		suffix=
		if [ "$goos" = windows ]; then
			suffix=.exe
		fi
		artifact="sync-hub_${VERSION}_${goos}_${goarch}${suffix}"
		run_cross_build "$goos" "$goarch" "$release_dir/$artifact"
	done
done
