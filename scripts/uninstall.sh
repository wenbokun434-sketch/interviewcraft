#!/bin/sh
set -eu

receipt_path=${1:-${INTERVIEWCRAFT_INSTALL_TEST_RECEIPT:-$HOME/.interviewcraft/install-receipt.txt}}
header=interviewcraft-install-receipt-v1
path_begin='# >>> InterviewCraft PATH >>>'
path_end='# <<< InterviewCraft PATH <<<'
[ -f "$receipt_path" ] || { printf 'uninstall: install receipt not found: %s\n' "$receipt_path" >&2; exit 1; }

first=$(sed -n '1p' "$receipt_path")
[ "$first" = "$header" ] || { printf '%s\n' 'uninstall: invalid install receipt' >&2; exit 1; }
tab=$(printf '\tX'); tab=${tab%X}
version=
install_dir=
binary_path=
path_files=
while IFS=$tab read -r key value extra; do
    [ -z "${extra:-}" ] || { printf '%s\n' 'uninstall: malformed receipt' >&2; exit 1; }
    case "$key" in
        "$header") ;;
        version) [ -z "$version" ] || exit 1; version=$value ;;
        install_dir) [ -z "$install_dir" ] || exit 1; install_dir=$value ;;
        binary_path) [ -z "$binary_path" ] || exit 1; binary_path=$value ;;
        path_file) path_files=${path_files}${path_files:+|}$value ;;
        *) printf 'uninstall: unknown receipt field: %s\n' "$key" >&2; exit 1 ;;
    esac
done < "$receipt_path"
if ! { [ -n "$version" ] && [ -n "$install_dir" ] && [ -n "$binary_path" ]; }; then printf '%s\n' 'uninstall: incomplete receipt' >&2; exit 1; fi
if ! { [ "$(dirname "$binary_path")" = "$install_dir" ] && [ "$(basename "$binary_path")" = interviewcraft ]; }; then printf '%s\n' 'uninstall: unsafe binary path in receipt' >&2; exit 1; fi

strip_block() {
    target=$1
    [ -f "$target" ] || return 0
    temp=$target.interviewcraft-uninstall-$$.tmp
    awk -v begin="$path_begin" -v end="$path_end" '
        $0 == begin { inside=1; next }
        $0 == end { inside=0; next }
        !inside { print }
    ' "$target" > "$temp"
    mv "$temp" "$target"
}

old_ifs=$IFS; IFS='|'; for path_file in $path_files; do strip_block "$path_file"; done; IFS=$old_ifs
rm -f "$binary_path"
rmdir "$install_dir" 2>/dev/null || true
rm -f "$receipt_path"
printf 'InterviewCraft %s was uninstalled.\n' "$version"
printf 'Configuration, credentials, and %s/.interviewcraft data were preserved.\n' "$HOME"
