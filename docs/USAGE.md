# Rawr apps and rawrkit

Full usage and distribution details. Run checkout examples from the repository root. See [the project home](../README.md) for the resource-to-work-pane workflow and [rawrkit](RAWRKIT.md) for the public SDK.

## Version and upgrade notes

The controls below describe **v0.2.0**. It adds Changes and filename/list filters, repository-wide open PR/issue views, central file/diff opening, guarded PR checkout and explicit VM actions to the earlier v0.1.0 UI.

Successful standalone pane actions are now silent. Scripts consuming v0.1.0 lifecycle JSON must add `--verbose` (or `-v`); `--status` continues to print JSON. Rawr project subcommands retain their structured output. Existing app processes keep their original code until explicitly reopened; installing new binaries does not restart live work. The existing VHS GIF is a historical v0.1.0 standalone-terminal reference.

Three standalone Go commands open independent native Rex panes:

| Command | App |
| --- | --- |
| `filerawr` | Project Tree/Changes browser; open a read-only file or diff in the center |
| `ghrawr` | Repository-wide open PRs/issues, checks, browser actions and guarded local checkout |
| `exerawr` | Existing VM browser; explicitly connect its SSH shell in the center |

Run a command from a Rex terminal and it opens or focuses its pane, then returns to the shell. Each executable includes its own terminal UI and uses the public **rawrkit** Go library for shared native pane behavior. **Rawr is optional**: the fourth Go command adds named projects and deliberately saved arrangements. No Python installation is needed by the Go binaries.

Rex owns terminals, pane movement, tabs, activity and native surface persistence. The apps do not run a daemon or another terminal host. They adapt the useful file/GitHub/`metcalfc/herdr-exe` workflows without a universal herdr compatibility host.

Build another app with `github.com/metcalfc/rawr/rawrkit`; its optional `rawrkit/ui` package provides the terminal UI loop/canvas, and `rawrkit/rex` provides the verified Rex CLI adapter. Applications register their own IDs, commands and placement. The library does not require one of these three apps or a Rawr project. See [the SDK guide and current verification](RAWRKIT.md).

The earlier names `ghrex`, `filerex` and `exerex` are compatibility names for existing installations. New builds and release bundles use `ghrawr`, `filerawr` and `exerawr`. See the [public verification summary](VERIFICATION.md) for current coverage and the [SDK guide](RAWRKIT.md) for its API.

## Download prebuilt binaries

The [v0.2.0 release](https://github.com/metcalfc/rawr/releases/tag/v0.2.0) provides macOS and Linux archives with all four commands. Go is not required on the destination. Choose the matching archive:

| Platform | Archive |
| --- | --- |
| macOS, Apple Silicon | `rawr-apps_v0.2.0_darwin_arm64.tar.gz` |
| macOS, Intel | `rawr-apps_v0.2.0_darwin_amd64.tar.gz` |
| Linux, ARM64 | `rawr-apps_v0.2.0_linux_arm64.tar.gz` |
| Linux, x86-64 | `rawr-apps_v0.2.0_linux_amd64.tar.gz` |

For example, on an Apple Silicon Mac:

```sh
rawr_version=v0.2.0
rawr_archive="rawr-apps_${rawr_version}_darwin_arm64.tar.gz"
rawr_release_url="https://github.com/metcalfc/rawr/releases/download/${rawr_version}"
curl -fLO "$rawr_release_url/$rawr_archive"
curl -fLO "$rawr_release_url/SHA256SUMS"
grep -F "  $rawr_archive" SHA256SUMS | shasum -a 256 -c -
tar -xzf "$rawr_archive"
export PATH="$PWD/rawr-apps_${rawr_version}_darwin_arm64:$PATH"
ghrawr --version
```

On Linux, use `sha256sum -c -` in place of `shasum -a 256 -c -`. You can copy the selected executables into any directory on PATH. Native Rex acceptance remains macOS-only; Linux artifacts are cross-compiled builds. Existing `git`/`gh` or SSH tools/authentication are still needed for their corresponding apps.

When using an older v0.1.0 binary, consult its footer and `--help`: files Enter/right previews locally, GitHub browses current-branch PRs/checks without an Issues tab or checkout action, and the VM picker includes mouse activation. v0.2.0 uses mouse selection for browsing and Enter/`s` for an explicit VM connection.

## Build or install from source

The module requires Go 1.24 or later; verification uses **Go 1.27.1**. Install the pinned version without a checkout:

```sh
go install github.com/metcalfc/rawr/cmd/filerawr@v0.2.0
go install github.com/metcalfc/rawr/cmd/ghrawr@v0.2.0
go install github.com/metcalfc/rawr/cmd/exerawr@v0.2.0
go install github.com/metcalfc/rawr/cmd/rawr@v0.2.0 # optional
```

Or run from the [v0.2.0 source checkout](https://github.com/metcalfc/rawr/tree/v0.2.0):

```sh
make build
export PATH="$PWD/build/bin:$PATH"
ghrawr --version
```

`make install PREFIX="$HOME/.local"` installs all four current binaries under `PREFIX/bin`. `make install-compat` also builds/updates the three old command-name entry points; release bundles contain only the current four names. The Go output is separate from the retained Python `bin/rawr` reference. Put `build/bin` or the install directory first in PATH when trying the Go commands.

Use the bundled Rex CLI automatically on macOS, a `rex` executable in PATH, or pass its path explicitly:

```sh
filerawr --rex '/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex'
```

Native integration is verified against Rex CLI/server **`0.0.1793645657-gd018d2`**, API **10**, app **Rex Beta 0.1.0 (1758)** on macOS. The adapter rejects other Rex versions and remote endpoints until their capabilities are verified. A binary cross-compiling for another OS establishes build compatibility only; see the [public verification summary](VERIFICATION.md).

## Open the resource panes

From a terminal inside Rex:

```sh
filerawr --root /absolute/path/to/checkout
ghrawr --root /absolute/path/to/checkout --repo OWNER/NAME
exerawr --root /absolute/path/to/checkout

filerawr --toggle
ghrawr --toggle
exerawr --toggle
exerawr --local
```

Use the same root and scope when addressing the same panes; without `--root`, the command discovers the local Git root, otherwise uses the current directory. A default scope is derived from the canonical root. `--scope NAME` supplies an explicit shared context. Native launches retain that root and repository when focus moves into a different checkout or SSH shell.

`--toggle` hides or shows a pane; `--hide` hides it; `--status` inspects the shared pane state. Hiding detaches the native surface and leaves its process running. Plain invocation focuses or opens. Resource panes stay independent; an explicit resource action opens work in the main area or invokes its named browser/checkout action. No command injects launch or relaunch text into an existing terminal.

Successful `ghrawr`, `filerawr` and `exerawr` pane actions are silent, including hide/toggle and `exerawr --local`. Add `--verbose` (or `-v`) to print their result JSON. `--status` always prints JSON; errors still go to stderr with a nonzero exit code. Help, version and interactive UI output stay visible. Scripts that consumed lifecycle JSON in v0.1.0 must now add `--verbose`. The optional `rawr` project command keeps its structured output.

```sh
ghrawr            # open/focus, then return silently
ghrawr --verbose  # open/focus and print pane details
ghrawr --status   # explicitly inspect shared state as JSON
```

Rex supplies `REX_SESSION` and `REX_BLOCK`. For an explicit target, use `--session FULL_ID --anchor WORK_BLOCK_ID`. `--rex`, `--server` and `--client` select the Rex connection. Outside Rex, run the UI directly:

```sh
filerawr --tui --root /absolute/path/to/checkout
ghrawr --tui --root /absolute/path/to/checkout --repo OWNER/NAME
exerawr --tui
```

`--tui` needs an interactive terminal. Outside Rex, selecting a VM temporarily gives that same terminal to SSH and returns to the selector when SSH exits. It requires neither Rex nor Rawr. Run `COMMAND --help` for the complete shared flags.

## Shared browsing and status

Arrows or `j`/`k` and mouse selection browse rows without launching them. Enter performs the view's explicit action. `?` opens contextual help; `r` refreshes. The status distinguishes an in-progress refresh, successful update and cached/stale data after an error. Error details stay visible rather than making a failed read look like an empty result.

Filter controls belong to the active view: `/` edits a filter, Enter applies, Escape cancels the edit and Ctrl-U clears it. GitHub filters the loaded list; file filtering matches filenames, not file contents. `q`/Escape leaves a detail/preview/help view before exiting the app.

## GitHub: choose a PR or issue

`ghrawr` shows open items across the declared repository, independent of the focused terminal's cwd or current branch. Use `--repo OWNER/NAME` when Git remotes are ambiguous. It uses existing `git`/`gh` authentication; missing login, provider failures and no matching items have separate states.

| Key | Action |
| --- | --- |
| `1` / `2` | Open PRs / open issues |
| `/` | Filter the loaded list |
| Enter / `o` | Open the selected item's browser URL |
| `c` | Inspect the selected PR's checks |
| `x` | Request checkout of the selected PR |
| `r` / `?` | Refresh / contextual help |

Each list loads at most 100 open items. The filter does not search the whole remote repository beyond those loaded rows. An issue has a browser action; it is not a PR checkout target.

For checkout, highlight a PR, press `x`, read the prompt and press `y` to confirm or `n` to cancel. The operation requires the anchored directory to be the existing Git worktree root, an unambiguous matching repository in local Git remotes, and no tracked or untracked changes. It also refuses an in-progress merge, rebase, cherry-pick, revert or bisect. Inside Rex, the original local work shell must be idle, at that root, on its primary screen in the center. A VM, agent, viewer or another busy program is not a checkout target. Outside Rex, the app uses the guarded local repository operation without a native center-pane check.

Some macOS login or wrapped terminals report only `login` or a terminal wrapper, without a verifiable shell foreground. Checkout refuses that ambiguous state, including wrappers such as Atuin when the actual idle shell cannot be observed. It does not infer idleness from prompt text. Browser/check browsing and read-only resource actions remain separate from this checkout guard.

The action rechecks the PR and local repository, then checks out that PR; it does not merge, rebase, push, stash, reset or clean the worktree. Refusals are shown without typing a command into the main terminal. Rawr serializes pane actions in the same shared state context; external clients and background filesystem writers remain outside that lock. If the checkout command fails or times out, inspect Git HEAD/status before retrying rather than assuming it rolled back.

## Files: open read-only work in the center

| Key | Action |
| --- | --- |
| `1` / `2` | Tree / Changes |
| `/` | Filter filenames |
| Enter | Expand a directory, or open the selected file/diff |
| `p` | Preview locally inside the file pane |
| `d` | Open the selected file's diff |
| `o` | Use the system file opener |
| `r` / `?` | Refresh / contextual help |

Changes distinguishes staged, unstaged and untracked files. Enter on a tracked change opens its staged or unstaged diff; an untracked file opens its contents. In Rex, opening that file/diff activates a read-only central view and retains the previous central work in a native history tab. The opened or reused view receives focus. This is a viewer, not an editor or content search interface. The system opener uses your platform's configured application separately.

Tree filtering matches relative filename paths through a bounded index; a 10,000-entry or two-second limit is reported in the pane. It does not search file contents. Left/right expand or collapse directories in the unfiltered Tree.

With `--tui` outside Rex, Enter previews the file in the current terminal UI instead of requiring a central pane. Use `p` when you deliberately want that local preview inside Rex too. Read-only details can be scrolled and left with the pane's displayed back/quit controls.

## VMs: inspect, then connect

| Key | Action |
| --- | --- |
| Enter / `s` | Connect the selected existing VM in the center |
| `i` | Inspect the selected VM's details |
| `b` | Open the selected VM in the browser |
| `r` / `?` | Refresh / contextual help |

Mouse selection and arrows only browse. Connecting explicitly puts the selected VM's SSH shell in the central work pane and focuses it. Previous local work stays running in a **Work · previous** native tab; older VM shells stay in **VM · HOST** tabs. Reconnecting the same host reuses its shell. `exerawr --local` brings retained local work back to the center. Hide resource panes with `--toggle` to give central work more space.

`l` is available only when an embedding caller supplies the optional link callback. The standalone CLI does not own a saved project link; use `rawr link PROJECT EXISTING_VM` for that deliberate metadata change. Opening a browser or connecting a live shell does not save a setup.

For deliberate pane dimensions, turn **Autobalance splits off** in Rex Settings → Interface → Customize pane behavior → Splits & Focus. The installed app otherwise changes sizes asynchronously after pane operations. Center switching uses native surfaces and retains previous work. The apps do not change this app-wide preference.

exe.dev discovery uses `ssh` and existing SSH access. `EXE_HOST` and `EXE_SSH_KEY` retain the upstream host/key choices. The apps do not create, destroy, stop or restart VMs, and do not lend Claude/imp credentials. Public github.com is supported; GitHub Enterprise is not implemented.

The v0.2.0 native resource walkthrough uses fixture VM records and local terminal recipes to check explicit selection, switching and preserved work. Earlier baseline acceptance separately verified real SSH connections/reuse. Those are distinct checks; see [verification](VERIFICATION.md).

## Optional Rawr projects and saved setups

```sh
rawr create demo /absolute/path/to/checkout --repo OWNER/NAME
rawr open demo
# Arrange native panes, then deliberately save:
rawr save demo
rawr plan demo
rawr restore demo

rawr toggle demo ghrawr
rawr term demo
rawr link demo EXISTING_VM
rawr show demo
```

`create` registers a project without opening Rex or contacting providers. The initial arrangement has a file pane left, work in the center, and independent GitHub/exe.dev panes stacked right. Use `--apps filerawr,ghrawr` for a subset, or `--apps=` for a shell only. `--vm EXISTING_NAME` records an initial VM link. `rawr link` verifies an existing VM through a read-only listing and changes the saved link; it does not switch a live shell. The default CLI's TUI does not install the optional `l` link callback.

Opening an already live project selects it and preserves the current arrangement and hidden tools. Only `save` records live geometry; `save --expected-revision N` protects against a stale writer. Unrecognized live programs are retained and saved as shell placeholders, since their processes and conversations cannot be reconstructed from a layout.

Restore preserves existing programs, extra panes, native history tabs and occupied terminals. Unsupported geometry or ambiguous ownership produces a concrete partial result instead of closing work. `plan` and `status` inspect the intended restore. Partial/failed restore results exit 2; ordinary errors exit 1. Inspect the result and any pending-operation journal before retrying a timeout.

Go project definitions use `$XDG_CONFIG_HOME/rawr-go` (default `~/.config/rawr-go`); runtime associations use `$XDG_STATE_HOME/rawr-go` (default `~/.local/state/rawr-go`). Apps use shared `$XDG_STATE_HOME/rexapps` state, including when launched by Rawr. Running bare `ghrawr` in a Rawr work shell finds the existing project pane when it resolves to the same project root. `--config-dir` and `--state-dir` override Rawr's project paths. For an isolated app namespace use `rawr --apps-state-dir PATH`; bare tools must use the same `--state-dir PATH`. Keep definitions and runtime mappings together when moving an installation. Saved files contain explicit recipes and layout identities, not terminal output or credentials.

The Go commands preserve the older Python workspace and settings by using separate directories. They do not automatically import Python projects, attach to Python-managed panes, install keybindings or edit Rex's Lua config. A safe legacy importer and a Go shortcut installer remain explicit gaps. Earlier Python source and tests remain as reference material.

Rex's tested isolated server restart retained surfaces and layout but started **fresh shells**. It did not restore programs, scrollback or conversations. The Go pane adapter stops on a changed server generation so it cannot relaunch into those shells. App reopen and machine reboot acceptance remain separate, unverified cases.

## Distribution and checks

Run the following from a source checkout. Release archives include executables, guides and example source, but not the Makefile, build scripts or full SDK source tree.

```sh
make test vet build
make sdk-smoke
make release VERSION=dev
```

`dist/` receives `rawr-apps_VERSION_OS_ARCH` bundles for the default targets: macOS and Linux, each arm64 and amd64. Every bundle contains `ghrawr`, `filerawr`, `exerawr`, `rawr`, the README, logo, native screenshot and historical demo GIF, public guides, the noterawr example source, third-party license texts, a license manifest, build metadata and binary SHA-256 checksums. The output directory's `SHA256SUMS` checksums the archives. The release builder uses an explicit public-file list, Go's standard library, `CGO_ENABLED=0`, `-trimpath` and literal build arguments. `make sdk-smoke` compiles a separate consumer module using only the public SDK imports and a custom app ID.

Extract the matching archive, check its SHA-256, and copy whichever executables you need into PATH. Go is not required on the destination. Services still require their normal external tools (`git`/`gh` or `ssh`), and native panes require the verified Rex installation.

To build selected targets:

```sh
make release VERSION=dev TARGETS=darwin/arm64,linux/amd64
# Optional Windows build artifacts; native Rex integration is unverified:
make release VERSION=dev TARGETS=windows/amd64,windows/arm64
```

For a versioned release build, use its reviewed full source commit and a fresh output directory. For v0.2.0:

```sh
go run ./scripts/release.go -version v0.2.0 -commit FULL_SOURCE_COMMIT \
  -targets darwin/arm64,darwin/amd64,linux/arm64,linux/amd64 \
  -out dist/v0.2.0
```

Every binary and `BUILD-INFO.json` records that version/commit. Publish only those four fresh archives and their adjacent `SHA256SUMS`; older local development archives are separate artifacts. The script builds and checksums locally; it does not create tags or upload releases.

The [source workflow](https://github.com/metcalfc/rawr/blob/v0.2.0/.github/workflows/go.yml) runs Go/SDK checks and uploads build artifacts; it does not publish releases. Implementation evidence and test results belong to the [public verification summary](VERIFICATION.md); this guide describes controls rather than declaring checks passed.

## Recording and remaining scope

The [historical v0.1.0 VHS recording](https://github.com/metcalfc/rawr/tree/v0.2.0/demos) shows the earlier standalone terminal UIs with fixture provider data. It does not demonstrate native Rex panes or the v0.2.0 resource-to-center workflow.

Terminal References, content text search, editor integration, pin/preview policies and additional activity features are not implemented. Filename filters and read-only views are the current file-navigation scope. Rex continues to own native terminal hosting/activity; no universal herdr compatibility host or terminal-text relaunch mechanism is introduced. Linux native acceptance remains unverified.
