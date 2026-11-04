# rawrkit: resource panes and central work

The commands are now **ghrawr**, **filerawr** and **exerawr**. **Rawr** remains the optional project/saved-setup command. Their shared native pane behavior is a public Go library, so another independently compiled app can use it without importing Rawr's project or provider integrations.

The SDK is part of the Go module `github.com/metcalfc/rawr`. Import the subpackages below; it does not require a separate module.

**Version scope:** this guide describes **v0.2.0**, including `RunLocalAction`, the status/document helpers and the file/PR resource workflows. v0.1.0 contains the earlier pane/UI SDK. The [main demo](../assets/demo.gif) captures the real Rex GUI: the three commands open native resource panes from an existing work terminal, and a selected local file opens in the center. Its provider data are fixtures; it does not demonstrate a live-provider checkout, browser action or SSH connection. The [optional standalone VHS tour](https://github.com/metcalfc/rawr/blob/main/assets/standalone-demo.gif) is separate. See [capture methods](https://github.com/metcalfc/rawr/tree/main/demos) and [verification](VERIFICATION.md) for their scope.

**Current source after v0.2.0** adds close-on-exit for newly hosted resource apps. That behavior is not part of the published v0.2.0 SDK pin below; use the current source checkout for it.

In your own Go module, pin the SDK with:

```sh
go get github.com/metcalfc/rawr@v0.2.0
```

| Import | Purpose |
| --- | --- |
| `github.com/metcalfc/rawr/rawrkit` | App registration; native open, hide, toggle, context/state and central managed terminals |
| `github.com/metcalfc/rawr/rawrkit/ui` | Optional tcell view loop, canvas, list selection/mouse handling and asynchronous refresh |
| `github.com/metcalfc/rawr/rawrkit/rex` | Narrow adapter for the verified Rex CLI/API |

All public SDK types are importable by another Go module. Applications do not need to import this repository's `internal` packages. The SDK itself can use its private storage implementation; that is not part of its public contract.

## Resource selection drives work

An app owns its resource list, selection, details and explicit actions. Browsing with arrows or mouse selection does not execute the resource. In the v0.2.0 apps, files/diffs activate read-only work in the center, VM connect actions activate an SSH terminal, and a PR checkout is a separately confirmed local repository action. GitHub browser/check details remain named actions of that pane.

Use `ActivateTerminal` for a declared central view/program and retain previous work in native history tabs. The opened or reused target receives focus. Keep the app's root/scope anchored while focus changes; a remote shell or a different checkout in another pane must not silently retarget its resource list.

The resource app should expose its current action and context through help and status. Cached rows, refresh progress and a failed read are distinct states. Rawr's optional project store owns deliberate saved setups; list refreshes, resource opening and native focus changes do not save a new setup.

## Register an independent app

An app chooses its own stable ID, executable and default placement. There is no built-in requirement for a GitHub, files or exe.dev app name. For example:

```go
package notes

import (
    "context"
    "os"
    "path/filepath"

    "github.com/metcalfc/rawr/rawrkit"
    "github.com/metcalfc/rawr/rawrkit/rex"
)

// root is an absolute, canonical local directory; appBinary is absolute.
func Open(ctx context.Context, root, stateDir, appBinary string) error {
    manager := rawrkit.New(rex.New("", "", ""), stateDir, filepath.Dir(appBinary))
    err := manager.Register(rawrkit.Definition{
        ID: "notesrawr", Title: "Notes",
        Command: []string{appBinary},
        Placement: rawrkit.PlacementSpec{
            Direction: "horizontal", Side: "after", Ratio: .75,
        },
    })
    if err != nil { return err }
    anchored := rawrkit.Context{
        Root: root, Scope: rawrkit.ScopeForRoot(root),
        Session: os.Getenv("REX_SESSION"), Anchor: os.Getenv("REX_BLOCK"),
        StateDir: stateDir,
    }
    _, err = manager.Open(ctx, anchored, "notesrawr")
    return err
}
```

`Register` returns definition/alias validation errors immediately. Definitions can also be passed to `New`. `Command` is literal argv; when omitted, the executable is `BinaryDir/ID`. IDs and aliases are lowercase identifiers. The native ratio is the fraction before the split: the example places the new app after 75% of the region. `AnchorApp` can place one registered app beside another, and `WholeLayer` can use the containing tiled layer. These are first-open defaults, rather than a background layout enforcement loop.

The native launch appends **`--tui --context-file PATH`** to the registered command. The executable must implement those flags, read the explicit JSON with `rawrkit.ReadContext(path)` and render its UI in that new terminal. The helper rejects unknown fields, trailing data and oversized documents. The context contains root/scope/session/anchor/connection identifiers, not terminal output or an environment snapshot. Run/open returns to the caller while Rex hosts the app process. A generic arbitrary executable that does not implement this launch contract is not automatically a pane app.

The complete [noterawr example](../examples/noterawr/main.go) implements this contract with only public SDK imports, tcell and the standard library. From the [v0.2.0 source checkout](https://github.com/metcalfc/rawr/tree/v0.2.0):

```sh
go build -o build/bin/noterawr ./examples/noterawr
build/bin/noterawr --tui --root /absolute/path/to/checkout
# Or inside Rex: open/hide/show its independent native pane.
build/bin/noterawr --root /absolute/path/to/checkout
build/bin/noterawr --toggle --root /absolute/path/to/checkout
```

It is an example app, rather than a fifth default release executable. Its source is included in release bundles for reference; the build commands above require the source checkout. An independent module can also copy that example and use the pinned SDK dependency.

Use the same canonical root, session, scope and state directory for apps sharing a workspace. `Open` opens/focuses, `Hide` detaches the surface while keeping its process, `Toggle` hides/shows and `Snapshot` inspects bindings/visibility. A running app is reused through its recorded native identity and process fingerprint. Changed, occupied or ambiguous work is preserved and reported, with interrupted operations retained in a journal.

In current source after v0.2.0, managed resource launches use `rex.AppLeaf`, which sets Rex's `exit.on_completion=true`. The default 250 ms quick-exit guard is retained so quick launch failures stay visible. Rex removes the owned resource pane after the app exits; the built-in `q`/Escape controls first leave details/help, then quit. `Hide`/`Toggle` keep the process running. Generic `rex.Leaf` terminals for local work, SSH and file viewers retain surfaces on exit. Standalone `--tui` only returns control to its hosting terminal.

Launch options are not changed on already-running panes: no runtime setter is verified. Explicitly close/recreate an old resource pane once to use the new behavior. Rawr's new initial/open/restore resource panes use it too, while live panes and working terminals are preserved.

## Optional terminal UI helpers

`rawrkit/ui` is independent of pane placement, projects and providers. Implement:

```go
type View interface {
    Init(*ui.Canvas) error
    Refresh(context.Context)
    Handle(context.Context, tcell.Event) bool
    Draw(*ui.Canvas)
}
```

Call `ui.Run(ctx, view)` to own a real terminal's initialization/restoration. `Handle` returns true to quit; the app decides what `q`/Escape do, so a detail view can close before the app exits. The runtime handles Ctrl-C, resize, mouse setup and cancellation. A view implementing `Close()` is cleaned up on exit.

`Canvas.Text` clips printable Unicode and treats content as inert text; `Canvas.Theme` supplies normal/title/selected styles. `ui.List` supplies selection, scrolling, keyboard navigation and one activation per mouse press; it does not choose app actions or own row data. `ui.Refresh[T]` cancels superseded loads and discards their late results. Its load callback returns data; view mutation stays on the UI goroutine. `Canvas.Suspend` can lend the same terminal to an interactive foreground action and restore the UI afterward.

`ui.ResourceStatus` records the last successful update and an error, and formats loading/current/stale status through `Line(loading)`. Applications retain their last good rows. `ui.Document` supplies scrolling for read-only help or details; the application decides how to leave that view. `ui.WrapLines` fits help/detail text to a narrow pane. The app controls whether a mouse/list activation only selects or invokes an explicit action; the built-in panes use selection-only mouse browsing.

`ui.RunScreen` uses a caller-initialized tcell screen, including a simulation screen. The caller retains `Init`/`Fini` ownership. This lets an external module exercise its UI without opening Rex or a real TTY.

## Put a managed terminal in the center

An app can call `manager.ActivateTerminal(ctx, anchored, rawrkit.TerminalSpec{...})`. The spec supplies a stable `ID`, `Title`, literal `Argv` and absolute `Cwd`; optional `Metadata` holds app-owned nonsecret identifiers. This API is generic: a caller can choose SSH, a local tool or another declared program without adding a provider integration to rawrkit.

Rex opens the terminal, then swaps its native surface into the main work slot. Previous work remains in a native history tab. Reusing the same terminal ID with the same recipe reuses its live program. A conflicting argv, cwd, title or metadata under that live ID returns an error while preserving the work. `LocalTerm` brings the retained original local work back to the center; a caller in a history tab must explicitly select the resource tab to see that restored center. Neither operation injects relaunch text into an arbitrary terminal, and the library does not own VM lifecycle or credentials.

The built-in exerawr app supplies the SSH recipe for selected existing VMs. Its provider listing and SSH/browser choices belong to the app. Rawr separately owns deliberate saved project definitions; opening/toggling an app does not save a new arrangement.

Filerawr's file/diff action is another app-owned terminal recipe: the content is displayed read-only in the center, and previous work remains running. A local `p` preview is a different view inside the resource pane. Neither is an editor integration or a pin/preview policy.

## Guard a local repository action

`manager.RunLocalAction(ctx, anchored, action)` checks the original local work shell's foreground idle state, root and primary screen while holding the pane-state lock, then runs the supplied `func(context.Context) error`. This serializes Rawr-managed swaps in that shared pane-state context during the action. It does not coordinate external-client typing or background filesystem writers.

If a login/terminal wrapper hides the actual shell foreground, the guard refuses the action. It does not parse prompt text as an idle-shell signal.

The app still owns confirmation and provider-specific validation. Ghrawr's `x` action asks for `y`/`n`, checks that the selected PR repository matches the anchored local checkout, and refuses tracked/untracked changes or an in-progress Git operation. Inside Rex it uses the local-work guard; outside Rex it uses the local repository checks without a Rex center. It performs checkout only, with no merge, rebase, push, cleanup or terminal-text injection. The callback is not a general sandbox for arbitrary code and must not recursively call methods on that manager while its lock is held.

## Compatibility and evidence boundaries

New default builds/installations and `rawr-apps_VERSION_OS_ARCH` release bundles contain `ghrawr`, `filerawr`, `exerawr` and `rawr`. The old `ghrex`, `filerex` and `exerex` command entry points remain compatibility names; they are not added to the default new release bundles. `make install` writes only the four current names and does not remove old executables. `make build-compat` also builds the old entry points locally; `make install-compat` explicitly refreshes those aliases along with the current commands.

Running processes retain their original UI code. Installation does not hot-reload or restart them; explicitly closing/reopening an app loads the new executable. Apps retain the shared `rexapps` state directory so a command rename does not silently abandon known native surfaces. Compatibility checks verified in-memory recipe/binding aliases, running-program reuse, and open/plan preserving native surfaces, geometry and saved definition bytes. This compatibility applies to the earlier Go state; Python projects remain separate.

The three built-in v0.2.0 app commands are silent after successful pane actions. `--verbose`/`-v` prints their result JSON; `--status` always prints JSON. Rawr's optional project subcommands keep structured output. This is a built-in CLI policy, not output that the SDK forces on another app; noterawr and other consumers own their command interfaces.

The [public verification summary](VERIFICATION.md) records current coverage and limits. Earlier Python source and tests remain as reference material; the public summary excludes local machine identifiers and raw terminal/session captures.

The v0.1.0 native baseline checked the installed Rex skill/guide and live identity: CLI/server `0.0.1793645657-gd018d2`, API 10, Rex Beta 0.1.0 (1758), local Unix endpoint on macOS. The same version/endpoint guard remains in v0.2.0. The [verification summary](VERIFICATION.md) distinguishes the v0.2.0 native resource walkthrough with fixture providers from earlier real SSH reuse. Autobalance splits must be off for deliberate fixed geometry. The prior private-server restart retained native surfaces/layout with fresh shells; it did not restore programs/transcripts. SDK mutation refuses an unexpected generation rather than relaunching into those shells. App reopen and machine reboot remain separate unverified cases.

## Build and verification

Run these from a source checkout. Release archives contain ready-to-run apps, public guides and the noterawr example source; they do not include the Makefile, verification scripts or full SDK tree.

```sh
make test vet build
make sdk-smoke
go test -race ./...
make release VERSION=dev
```

The [external SDK smoke program](https://github.com/metcalfc/rawr/tree/v0.2.0/scripts/sdk-smoke) creates a separate temporary Go module with a local `replace` to this checkout. Its consumer imports only public rawrkit/ui/rex packages plus tcell, verifies a custom `sdk-notes` app/alias unrelated to the built-in names, round-trips explicit context through the public reader, type-checks the native launch/terminal APIs, and runs shared canvas/list selection on a simulation screen. It does not connect to Rex or mutate projects.

For a complete compile matrix of that external consumer:

```sh
go run ./scripts/sdk-smoke -targets darwin/arm64,darwin/amd64,linux/arm64,linux/amd64,windows/arm64,windows/amd64
```

Release defaults are macOS/Linux arm64+amd64 with CGO disabled. Optional Windows arm64/amd64 zip artifacts can be requested through `make release TARGETS=windows/arm64,windows/amd64`. A build succeeding for a target does not verify Rex, real-terminal rendering, filesystem or SSH behavior there. Bundles include public documentation, dependency license/notice texts, a module/license manifest, build metadata and SHA-256 checksums. The build script does not publish or sign releases.

These commands describe how to verify a source checkout. The [public verification summary](VERIFICATION.md) records test/native results and separates v0.2.0 resource checks from the v0.1.0 baseline. That baseline covers pane reuse, retained programs/geometry, explicit save/restore, private-server restart safety, public SDK use and cross-compilation.

Terminal References, content text search, editor integration, pin/preview policies and added activity features are not implemented. Linux native Rex behavior remains unverified. Rex owns terminal hosting and native activity; rawrkit does not emulate a universal herdr host or inject relaunch text into arbitrary work.

## Design references

[herdr-radar](https://github.com/hhdebb/herdr-radar), [herdr-sidebar](https://github.com/alexarthurs/herdr-sidebar), [termscope](https://github.com/iurysza/termscope) and [clauth](https://github.com/uwuclxdy/clauth) inform browse-first resource navigation, contextual detail, visible refresh state and explicit actions. Those are product lessons applied through the verified Rex API and independent panes. No plugin manifest compatibility, account switching, credential capture or credential lending is implied.
