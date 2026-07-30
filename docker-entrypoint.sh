#!/bin/sh

set -eu

if [ "$(id -u)" = "0" ]; then
	install -d -m 0700 -o synchub -g synchub /data
	chown -R synchub:synchub /data
	exec su-exec synchub /usr/local/bin/sync-hub "$@"
fi

exec /usr/local/bin/sync-hub "$@"
