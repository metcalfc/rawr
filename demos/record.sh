#!/bin/sh
# Record real Go UIs with an isolated workspace and explicit provider fixtures.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
demo_tool_path=/usr/bin:/bin:/usr/sbin:/sbin
for dependency in vhs ttyd ffmpeg git; do
    dependency_path=$(command -v "$dependency") || {
        printf 'Missing demo dependency: %s\n' "$dependency" >&2
        exit 1
    }
    demo_tool_path="$(dirname -- "$dependency_path"):$demo_tool_path"
done
demo_vhs=$(command -v vhs)
demo_git=$(command -v git)

demo_dir=$(mktemp -d /tmp/rawr-demo.XXXXXX)
trap 'rm -rf "$demo_dir"' EXIT HUP INT TERM
demo_project="$demo_dir/DEMO-fixtures"
mkdir -p "$demo_project" "$demo_dir/bin" "$demo_dir/empty" "$demo_dir/config" "$demo_dir/state"
cp -R demos/workspace/. "$demo_project/"

# No inherited Git environment/configuration, signing key or hooks enter these
# disposable commits. No command below contacts a remote.
fixture_git() {
    env -i PATH="$demo_tool_path" GIT_CONFIG_NOSYSTEM=1 \
        GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 \
        "$demo_git" -c core.hooksPath="$demo_dir/empty" -c commit.gpgsign=false "$@"
}
fixture_git init --quiet --template="$demo_dir/empty" --initial-branch=demo/tools \
    --separate-git-dir="$demo_dir/git" "$demo_project"
fixture_git -C "$demo_project" remote add origin https://github.com/demo/rawr-workspace.git
fixture_git -C "$demo_project" add .
fixture_git -C "$demo_project" -c user.name='Rawr Demo' -c user.email=demo@example.invalid \
    commit --quiet -m 'Disposable VHS baseline'
printf '\nUse Tree, Changes and filename filters to explore the project.\n' >> "$demo_project/docs/PLAN.md"
cat > "$demo_project/src/main.go" <<'EOF'
package main

import "fmt"

func main() {
	fmt.Println("Browse resources. Keep your work.")
}
EOF
fixture_git -C "$demo_project" add src/main.go
printf 'A new, untracked file in the disposable demo project.\n' > "$demo_project/NOTES.md"

# Fail before recording if the three intended Git categories were not created.
fixture_git -C "$demo_project" status --porcelain=v1 > "$demo_dir/status.txt"
grep -Fx ' M docs/PLAN.md' "$demo_dir/status.txt" >/dev/null
grep -Fx 'M  src/main.go' "$demo_dir/status.txt" >/dev/null
grep -Fx '?? NOTES.md' "$demo_dir/status.txt" >/dev/null

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

# Preserve the caller's home identity only for VHS's installed browser cache;
# Bash skips rc/profile files, Git ignores user/system config, and app config
# paths are private. Provider credentials, Rex IDs and shell startup variables
# are absent from this allowlisted child environment from the first frame.
mkdir -p assets .acceptance/vhs-demo
"$demo_vhs" validate demos/rawr.tape
env -i HOME="$HOME" PATH="$repo_dir/demos/adapters:$demo_bin:$demo_tool_path" \
    TERM=xterm-256color COLORTERM=truecolor \
    XDG_CONFIG_HOME="$demo_dir/config" XDG_STATE_HOME="$demo_dir/state" \
    GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 \
    RAWR_DEMO_PROJECT="$demo_project" RAWR_DEMO_BIN="$demo_bin" \
    RAWR_DEMO_ADAPTERS="$repo_dir/demos/adapters" \
    RAWR_DEMO_FIXTURES="$repo_dir/demos/fixtures" \
    "$demo_vhs" demos/rawr.tape
