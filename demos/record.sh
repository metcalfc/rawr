#!/bin/sh
# Record real Go UIs with an isolated workspace and explicit provider fixtures.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
for dependency in vhs ttyd ffmpeg git; do
    command -v "$dependency" >/dev/null 2>&1 || {
        printf 'Missing demo dependency: %s\n' "$dependency" >&2
        exit 1
    }
done

demo_dir=$(mktemp -d /tmp/rawr-demo.XXXXXX)
trap 'rm -rf "$demo_dir"' EXIT HUP INT TERM
mkdir -p "$demo_dir/project" "$demo_dir/bin"
cp -R demos/workspace/. "$demo_dir/project/"
git init --quiet --initial-branch=demo/tools --separate-git-dir="$demo_dir/git" "$demo_dir/project"

if [ -n "${RAWR_DEMO_BIN:-}" ]; then
    demo_bin=$(CDPATH= cd -- "$RAWR_DEMO_BIN" && pwd)
else
    command -v go >/dev/null 2>&1 || { printf 'Go is required to build the demo apps.\n' >&2; exit 1; }
    demo_bin="$demo_dir/bin"
    for app in filerawr ghrawr exerawr; do
        CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$demo_bin/$app" "./cmd/$app"
    done
fi
for app in filerawr ghrawr exerawr; do
    [ -x "$demo_bin/$app" ] || { printf 'Missing executable: %s\n' "$demo_bin/$app" >&2; exit 1; }
done

# mktemp's path contains no JSON quoting characters. No user environment,
# authentication material, machine name, or personal path enters this context.
printf '{"name":"DEMO - GitHub / VM data are fixtures","root":"%s/project","repository":"demo/rawr-workspace","scope":"vhs-demo"}\n' "$demo_dir" > "$demo_dir/context.json"
export RAWR_DEMO_CONTEXT="$demo_dir/context.json"
export RAWR_DEMO_BIN="$demo_bin"
export RAWR_DEMO_ADAPTERS="$repo_dir/demos/adapters"
export RAWR_DEMO_FIXTURES="$repo_dir/demos/fixtures"

mkdir -p assets
vhs validate demos/rawr.tape
PATH="$demo_bin:$PATH" vhs demos/rawr.tape
