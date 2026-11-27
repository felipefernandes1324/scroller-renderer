#!/bin/sh
set -eu

cd "$(dirname "$0")"

if ! command -v ffmpeg >/dev/null 2>&1; then
	printf '%s\n' 'FFmpeg was not found on PATH.' 'Install it with your system package manager, then run this script again.'
	exit 1
fi

if ! ffmpeg -hide_banner -encoders 2>/dev/null | grep -q 'libx264'; then
	printf '%s\n' 'Your FFmpeg build does not provide libx264.'
	exit 1
fi

if ! ffmpeg -hide_banner -encoders 2>/dev/null | grep -Eq '[[:space:]]aac[[:space:]]'; then
	printf '%s\n' 'Your FFmpeg build does not provide the AAC encoder.'
	exit 1
fi

exec ./scroller-renderer -gui
