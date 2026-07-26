#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/build-common.sh"

resolve_metadata
"$script_dir/frontend.sh"
"$script_dir/go-check.sh"
prepare_directory build
run_host_build build/sync-hub
