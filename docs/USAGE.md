# Rawr apps and rawrkit

Full usage and distribution details. Run checkout examples from the repository root. See [the project home](../README.md) for the quick start and [rawrkit](RAWRKIT.md) for the public SDK.

Three standalone Go commands open independent native Rex panes:

| Command | App |
| --- | --- |
| `filerawr` | File tree and preview anchored to a local project root |
| `ghrawr` | GitHub PRs and checks for that project |
| `exerawr` | Existing exe.dev VMs; select a host to put its SSH shell in the central work pane |

Run a command from a Rex terminal and it opens or focuses its pane, then returns to the shell. Each executable includes its own terminal UI and uses the public **rawrkit** Go library for shared native pane behavior. **Rawr is optional**: the fourth Go command adds named projects and deliberately saved arrangements. No Python installation is needed by the Go binaries.

Rex owns terminals, pane movement, tabs, activity and native surface persistence. The apps do not run a daemon or another terminal host. They adapt the useful file/GitHub/`metcalfc/herdr-exe` workflows without a universal herdr compatibility host.

Build another app with `github.com/metcalfc/rawr/rawrkit`; its optional `rawrkit/ui` package provides the terminal UI loop/canvas, and `rawrkit/rex` provides the verified Rex CLI adapter. Applications register their own IDs, commands and placement. The library does not require one of these three apps or a Rawr project. See [the SDK guide and current verification](RAWRKIT.md).

The earlier names `ghrex`, `filerex` and `exerex` are compatibility names for existing installations. New builds and release bundles use `ghrawr`, `filerawr` and `exerawr`. See the [public verification summary](VERIFICATION.md) for current coverage and the [SDK guide](RAWRKIT.md) for its API.

## Build or install

The module requires Go 1.24 or later; the current verification uses **Go 1.27.1**. Run from this checkout:

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

## Run the independent apps

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

`--toggle` hides or shows a pane; `--hide` hides it; `--status` inspects the shared pane state. Hiding detaches the native surface and leaves its process running. Plain invocation focuses or opens, rather than toggling. No command injects text into an existing terminal.

Rex supplies `REX_SESSION` and `REX_BLOCK`. For an explicit target, use `--session FULL_ID --anchor WORK_BLOCK_ID`. `--rex`, `--server` and `--client` select the Rex connection. Outside Rex, run the UI directly:

```sh
filerawr --tui --root /absolute/path/to/checkout
ghrawr --tui --root /absolute/path/to/checkout --repo OWNER/NAME
exerawr --tui
```

`--tui` needs an interactive terminal. Outside Rex, selecting a VM temporarily gives that same terminal to SSH and returns to the selector when SSH exits. It requires neither Rex nor Rawr. Run `COMMAND --help` for the complete shared flags.

The file tree supports arrows or `j`/`k`, Enter/right to expand or preview, left to collapse, `o` to open a file, `r` to refresh and `q`/Escape to leave a preview or exit. GitHub uses existing `git`/`gh` authentication; `--repo OWNER/NAME` resolves ambiguous remotes. Missing authentication and service failures appear in that app.

In exerawr, **click a host or highlight it and press Enter** to put its SSH shell in the central work pane. The previous local work stays running in a **Work · previous** native tab; older VM shells stay in **VM · HOST** tabs. Selecting an existing host reuses its shell. `exerawr --local` brings the retained local work back to the center. Arrow keys alone browse. Hide the app panes to give central work more space.

For deliberate pane dimensions, turn **Autobalance splits off** in Rex Settings → Interface → Customize pane behavior → Splits & Focus. The installed app otherwise changes sizes asynchronously after pane operations. VM switching uses native swaps without resize calls. The apps do not change this app-wide preference.

exe.dev discovery uses `ssh` and existing SSH access. `EXE_HOST` and `EXE_SSH_KEY` retain the upstream host/key choices. The apps do not create, destroy, stop or restart VMs, and do not lend Claude/imp credentials. Public github.com is supported; GitHub Enterprise is not implemented.

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

`create` registers a project without opening Rex or contacting providers. The initial arrangement has a file pane left, work in the center, and independent GitHub/exe.dev panes stacked right. Use `--apps filerawr,ghrawr` for a subset, or `--apps=` for a shell only. `--vm EXISTING_NAME` records an initial VM link. `rawr link` verifies an existing VM through a read-only listing and changes the saved link; it does not switch a live shell. The Go TUI's `l` action is not connected to Rawr's optional store yet.

Opening an already live project selects it and preserves the current arrangement and hidden tools. Only `save` records live geometry; `save --expected-revision N` protects against a stale writer. Unrecognized live programs are retained and saved as shell placeholders, since their processes and conversations cannot be reconstructed from a layout.

Restore preserves existing programs, extra panes, native history tabs and occupied terminals. Unsupported geometry or ambiguous ownership produces a concrete partial result instead of closing work. `plan` and `status` inspect the intended restore. Partial/failed restore results exit 2; ordinary errors exit 1. Inspect the result and any pending-operation journal before retrying a timeout.

Go project definitions use `$XDG_CONFIG_HOME/rawr-go` (default `~/.config/rawr-go`); runtime associations use `$XDG_STATE_HOME/rawr-go` (default `~/.local/state/rawr-go`). Apps use shared `$XDG_STATE_HOME/rexapps` state, including when launched by Rawr. Running bare `ghrawr` in a Rawr work shell finds the existing project pane when it resolves to the same project root. `--config-dir` and `--state-dir` override Rawr's project paths. For an isolated app namespace use `rawr --apps-state-dir PATH`; bare tools must use the same `--state-dir PATH`. Keep definitions and runtime mappings together when moving an installation. Saved files contain explicit recipes and layout identities, not terminal output or credentials.

The Go commands preserve the older Python workspace and settings by using separate directories. They do not automatically import Python projects, attach to Python-managed panes, install keybindings or edit Rex's Lua config. A safe legacy importer and a Go shortcut installer remain explicit gaps. Earlier Python source and tests remain as reference material.

Rex's tested isolated server restart retained surfaces and layout but started **fresh shells**. It did not restore programs, scrollback or conversations. The Go pane adapter stops on a changed server generation so it cannot relaunch into those shells. App reopen and machine reboot acceptance remain separate, unverified cases.

## Distribution and checks

```sh
make test vet build
make sdk-smoke
make release VERSION=dev
```

`dist/` receives `rawr-apps_VERSION_OS_ARCH` bundles for the default targets: macOS and Linux, each arm64 and amd64. Every bundle contains `ghrawr`, `filerawr`, `exerawr`, `rawr`, the README and logo, public guides, the noterawr example source, third-party license texts, a license manifest, build metadata and binary SHA-256 checksums. `dist/SHA256SUMS` checksums the archives. The release builder uses an explicit public-file list, Go's standard library, `CGO_ENABLED=0`, `-trimpath` and literal build arguments. `make sdk-smoke` compiles a separate consumer module using only the public SDK imports and a custom app ID.

Extract the matching archive, check its SHA-256, and copy whichever executables you need into PATH. Go is not required on the destination. Services still require their normal external tools (`git`/`gh` or `ssh`), and native panes require the verified Rex installation.

To build selected targets:

```sh
make release VERSION=dev TARGETS=darwin/arm64,linux/amd64
# Optional Windows build artifacts; native Rex integration is unverified:
make release VERSION=dev TARGETS=windows/amd64,windows/arm64
```

The workflow in `.github/workflows/go.yml` runs Go/SDK checks and uploads build artifacts; it does not publish releases. Current API details are in [RAWRKIT.md](RAWRKIT.md); coverage and remaining platform gaps are in the [public verification summary](VERIFICATION.md).
