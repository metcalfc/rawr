<p align="center">
  <img src="assets/rawr-logo.png" alt="Rawr" width="220">
</p>

# Rawr

Small resource panes. Your work in the center.

Run `ghrawr`, `filerawr` or `exerawr` from an existing work terminal in [Rex](https://www.superlogical.com/rex). Each Go command opens or focuses its own native resource pane and returns, leaving your work available. Browse files, pull requests, issues and VMs, then explicitly open a file/diff in the center, check out a PR into an idle local checkout, or connect a VM shell. Previous central work stays available in native tabs.

![Captured native Rex workflow: open ghrawr, filerawr and exerawr, then quit each pane](assets/demo.gif)

Captured in an existing Rex work window: bare `ghrawr`, `filerawr` and `exerawr` open native panes, then `q` from each app's main list removes its pane while the original shell stays alive. The apps are development build `fd98579`, after v0.2.0; provider data are fixtures. Timed GUI screenshots have explanatory captions below the Rex image; this is not continuous video. No central file opening, PR checkout, browser action or VM connection is shown. [Capture scope and reproduction](https://github.com/metcalfc/rawr/tree/main/demos).

**v0.2.0** adds repository-wide PR/issue browsing, file Changes and filters, central file/diff views, guarded PR checkout and quieter shell commands. Upgrading scripts from v0.1.0: add `--verbose` to keep receiving pane-action JSON. `--status` still prints JSON.

| Resource pane | Workflow |
| --- | --- |
| `filerawr` | Switch between Tree and Changes, filter filenames, and open a read-only file or diff in the center. |
| `ghrawr` | Browse open PRs/issues across the repository, inspect checks, open the browser, or confirm a guarded local PR checkout. |
| `exerawr` | Browse VM details, then explicitly connect the selected host's SSH shell in the center. |
| `rawr` *(optional)* | Name projects and deliberately save or restore their setups. |

## Work from the resource pane

1. In `ghrawr`, use `1` for PRs or `2` for issues and `/` to filter. Press `c` for PR checks or Enter/`o` for the browser. For a PR checkout, press `x`, review the prompt and confirm with `y`; the local repository must match and be clean, and Rex requires the original idle local shell in the center.
2. In `filerawr`, use `1` for Tree or `2` for Changes. Enter opens the selected file or diff read-only in the center, retaining prior work in a native history tab. Use `p` for a preview inside the file pane.
3. In `exerawr`, browse with arrows or a mouse selection, inspect with `i`, and press Enter/`s` to connect. `b` opens the selected VM in the browser. Existing local work and VM shells keep running.

Arrows and mouse selection browse; they do not launch work. `?` shows context help and `r` refreshes with loading/cached/error status. Running a command opens or focuses its existing pane; `--toggle` hides/shows it while retaining the process. Tools stay anchored to the project when focus moves to another terminal or SSH shell.

**Current source after v0.2.0:** `q`/Esc backs out of details, then quits the app and lets Rex remove its managed resource pane. Quick launch failures stay visible; `--hide`/`--toggle` keep the app running. Standalone `--tui` returns to its terminal. Existing panes need an explicit close/reopen once to pick up the new launch behavior; published v0.2.0 is unchanged.

Successful pane actions are silent. Add `--verbose` (or `-v`) for their JSON details; `--status` always prints JSON and errors go to stderr. Rawr's optional project commands keep their structured output.

## Install v0.2.0

Download [v0.2.0 macOS and Linux binaries](https://github.com/metcalfc/rawr/releases/tag/v0.2.0) for arm64 or amd64. Go is not needed to run them. Follow the [download and checksum instructions](docs/USAGE.md#download-prebuilt-binaries).

Or install with Go 1.24 or later:

```sh
go install github.com/metcalfc/rawr/cmd/filerawr@v0.2.0
go install github.com/metcalfc/rawr/cmd/ghrawr@v0.2.0
go install github.com/metcalfc/rawr/cmd/exerawr@v0.2.0
# Optional projects and deliberately saved setups:
go install github.com/metcalfc/rawr/cmd/rawr@v0.2.0
```

## Run the apps

From an existing work terminal inside Rex, in your project:

```sh
ghrawr
filerawr
exerawr
```

Each command opens/focuses its pane; return focus to your working terminal to run the next one. The default context is its Git root, otherwise its current directory. Use `--root PATH` or `ghrawr --repo OWNER/NAME` when you need an explicit context.

Use `exerawr --local` from the same local context to return retained local work to the center. The apps also offer `--tui` in an ordinary interactive terminal; file Enter falls back to a local preview when there is no Rex center. See [the usage guide](docs/USAGE.md) for exact keys, checkout guards, roots and saved setups.

Already-running app processes keep their original UI code. Building or installing does not hot-upgrade them; use a fresh Rex session or explicitly reopen the tool panes you intend to update.

To use current source changes, including close-on-exit, build from the [main source checkout](https://github.com/metcalfc/rawr/tree/main):

```sh
make build
export PATH="$PWD/build/bin:$PATH"
```

Release archives contain ready-to-run apps and public guides from their release commit. The source checkout also contains the Makefile and verification scripts; the v0.2.0 tag and downloads remain unchanged.

## Build another resource pane

The public [rawrkit SDK](docs/RAWRKIT.md) supplies native pane lifecycle, anchored context, managed central terminals and optional terminal UI helpers. Resource data and actions belong to the app; Rawr remains the optional project layer. Start with the [noterawr example](examples/noterawr/main.go).

## Scope and verification

The native integration baseline is **macOS**, **Rex Beta 0.1.0 (1758)**, CLI/server **`0.0.1793645657-gd018d2`**, API **10**. Other Rex versions and remote endpoints are rejected until verified. Turn **Autobalance splits off** for deliberate fixed pane sizes. The [public verification summary](docs/VERIFICATION.md) separates the v0.2.0 walkthrough from earlier evidence.

The v0.2.0 native resource walkthrough uses labelled GitHub/VM fixtures; earlier baseline acceptance separately exercised real SSH reuse. Linux native Rex behavior remains unverified; cross-compilation establishes build compatibility. GitHub uses existing `git`/`gh` authentication, and exe.dev uses existing SSH access. Terminal References, content text search, editor integration, pin/preview policies and additional activity features are not implemented. Rex continues to own native terminal hosting and activity; Rawr does not inject relaunch text or host arbitrary herdr plugins.

PR checkout also refuses a macOS login/wrapped shell when Rex cannot verify its idle shell foreground. A visible prompt is not treated as proof that checkout is safe.

## Optional standalone UI tour

The [standalone VHS tour](https://github.com/metcalfc/rawr/blob/main/assets/standalone-demo.gif) shows the `--tui` apps in one isolated terminal, including filename filters, local preview/diffs, PRs/issues/checks and VM details. Its providers are fixtures; it does not show the native Rex window. [Recording instructions](https://github.com/metcalfc/rawr/tree/main/demos#optional-standalone-vhs-tour).

A separate [native pane screenshot](assets/rex-resource-panes.png) is also available. Media updates on main do not change the v0.2.0 release tag or archives.

## Inspiration

[herdr-radar](https://github.com/hhdebb/herdr-radar), [herdr-sidebar](https://github.com/alexarthurs/herdr-sidebar), [termscope](https://github.com/iurysza/termscope) and [clauth](https://github.com/uwuclxdy/clauth) are useful design references for resource navigation, contextual details, visible status and deliberate actions. Rawr applies those lessons through independent Rex panes and its own SDK; this is design inspiration, not plugin compatibility or credential/account management.

Rawr is an independent project, not affiliated with or endorsed by Rex or Superlogical. Rex and other product names and marks belong to their respective owners.
