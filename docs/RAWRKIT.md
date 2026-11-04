# rawrkit and the renamed apps

The commands are now **ghrawr**, **filerawr** and **exerawr**. **Rawr** remains the optional project/saved-setup command. Their shared native pane behavior is a public Go library, so another independently compiled app can use it without importing Rawr's project or provider integrations.

The SDK is part of the Go module `github.com/metcalfc/rawr`. Import the subpackages below; it does not require a separate module.

| Import | Purpose |
| --- | --- |
| `github.com/metcalfc/rawr/rawrkit` | App registration; native open, hide, toggle, context/state and central managed terminals |
| `github.com/metcalfc/rawr/rawrkit/ui` | Optional tcell view loop, canvas, list selection/mouse handling and asynchronous refresh |
| `github.com/metcalfc/rawr/rawrkit/rex` | Narrow adapter for the verified Rex CLI/API |

All public SDK types are importable by another Go module. Applications do not need to import this repository's `internal` packages. The SDK itself can use its private storage implementation; that is not part of its public contract.

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

The complete [noterawr example](../examples/noterawr/main.go) implements this contract with only public SDK imports, tcell and the standard library. From the checkout:

```sh
go build -o build/bin/noterawr ./examples/noterawr
build/bin/noterawr --tui --root /absolute/path/to/checkout
# Or inside Rex: open/hide/show its independent native pane.
build/bin/noterawr --root /absolute/path/to/checkout
build/bin/noterawr --toggle --root /absolute/path/to/checkout
```

It is an example app, rather than a fifth default release executable. Its source is included in release bundles for reference; building the example uses the SDK checkout/module.

Use the same canonical root, session, scope and state directory for apps sharing a workspace. `Open` opens/focuses, `Hide` detaches the surface while keeping its process, `Toggle` hides/shows and `Snapshot` inspects bindings/visibility. A running app is reused through its recorded native identity and process fingerprint. Changed, occupied or ambiguous work is preserved and reported, with interrupted operations retained in a journal.

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

`ui.RunScreen` uses a caller-initialized tcell screen, including a simulation screen. The caller retains `Init`/`Fini` ownership. This lets an external module exercise its UI without opening Rex or a real TTY.

## Put a managed terminal in the center

An app can call `manager.ActivateTerminal(ctx, anchored, rawrkit.TerminalSpec{...})`. The spec supplies a stable `ID`, `Title`, literal `Argv` and absolute `Cwd`; optional `Metadata` holds app-owned nonsecret identifiers. This API is generic: a caller can choose SSH, a local tool or another declared program without adding a provider integration to rawrkit.

Rex opens the terminal, then swaps its native surface into the main work slot. Previous work remains in a native history tab. Reusing the same terminal ID with the same recipe reuses its live program. A conflicting argv, cwd, title or metadata under that live ID returns an error while preserving the work. `LocalTerm` brings the retained original local work back to the center. Neither operation injects relaunch text into an arbitrary terminal, and the library does not own VM lifecycle or credentials.

The built-in exerawr app supplies the SSH recipe for selected existing VMs. Its provider listing and SSH/browser choices belong to the app. Rawr separately owns deliberate saved project definitions; opening/toggling an app does not save a new arrangement.

## Compatibility and evidence boundaries

New default builds/installations and `rawr-apps_VERSION_OS_ARCH` release bundles contain `ghrawr`, `filerawr`, `exerawr` and `rawr`. The old `ghrex`, `filerex` and `exerex` command entry points remain compatibility names; they are not added to the default new release bundles. `make install` writes only the four current names and does not remove old executables. `make build-compat` also builds the old entry points locally; `make install-compat` explicitly refreshes those aliases along with the current commands.

Running processes retain their original UI code. Installation does not hot-reload or restart them; explicitly closing/reopening an app loads the new executable. Apps retain the shared `rexapps` state directory so a command rename does not silently abandon known native surfaces. Compatibility checks verified in-memory recipe/binding aliases, running-program reuse, and open/plan preserving native surfaces, geometry and saved definition bytes. This compatibility applies to the earlier Go state; Python projects remain separate.

The [public verification summary](VERIFICATION.md) records current coverage and limits. Earlier Python source and tests remain as reference material; the public summary excludes local machine identifiers and raw terminal/session captures.

Before the SDK native walkthrough, the installed Rex skill/guide and live identity were rechecked: CLI/server `0.0.1793645657-gd018d2`, API 10, Rex Beta 0.1.0 (1758), local Unix endpoint on macOS. The SDK uses the verified native create/view/process/inventory/detach/move/swap/focus methods; extracting a public library does not broaden the Rex API/platform claim. Other Rex versions and remote endpoints remain rejected. Autobalance splits must be off for deliberate fixed geometry. Rex's verified private server restart retained native surfaces/layout with fresh shells; it did not restore programs/transcripts. SDK mutation refuses an unexpected generation rather than relaunching into those shells. App reopen and machine reboot remain separate unverified cases.

## Build and verification

```sh
make test vet build
make sdk-smoke
go test -race ./...
make release VERSION=dev
```

`scripts/sdk-smoke` creates a separate temporary Go module with a local `replace` to this checkout. Its consumer imports only public rawrkit/ui/rex packages plus tcell, verifies a custom `sdk-notes` app/alias unrelated to the built-in names, round-trips explicit context through the public reader, type-checks the native launch/terminal APIs, and runs shared canvas/list selection on a simulation screen. It does not connect to Rex or mutate projects.

For a complete compile matrix of that external consumer:

```sh
go run ./scripts/sdk-smoke -targets darwin/arm64,darwin/amd64,linux/arm64,linux/amd64,windows/arm64,windows/amd64
```

Release defaults are macOS/Linux arm64+amd64 with CGO disabled. Optional Windows arm64/amd64 zip artifacts can be requested through `make release TARGETS=windows/arm64,windows/amd64`. A build succeeding for a target does not verify Rex, real-terminal rendering, filesystem or SSH behavior there. Bundles include public documentation, dependency license/notice texts, a module/license manifest, build metadata and SHA-256 checksums. The build script does not publish or sign releases.

The external consumer compiled for all six targets above and passed registration/alias, context and UI simulation checks on macOS arm64. Project native acceptance covered a bare `ghrawr` reusing the project pane and a closed/reopened app rebinding to its exact shared identity without duplicate restore. A private-server restart verified fresh shells, lost prior program output, and open/plan/restore preserving those shells without terminal input. Legacy Go compatibility checks preserved existing programs and saved definitions.

Native pane/SDK acceptance covered ordinary command return, repeated open reuse, mixed hide/show with the same programs/geometry, real SSH shells returning to the center/local work, and an independently compiled `noterawr` app using only the public SDK/UI. The example retained its running process across hide/show.

The real Rex app was also inspected through native accessibility/screen state: filerawr opened a README preview through End+Enter; exerawr accepted mouse and keyboard selection of an existing VM into the center while native split ratios stayed unchanged. The independent noterawr UI rendered its context, and Down+Enter changed its message through the shared list/view runtime. Its process survived hide/show. Existing SSH/local history work was retained; already-running UIs kept their original code.

The full race suite passed, including generic-terminal recipe conflict, legacy executable/alias normalization and old Rawr app-argument regressions. Distribution checks covered six target builds, checksums, extracted macOS executions and optional alias/install checks. Default archives contain only the four current executables, with dependency and Go runtime/standard-library notices. See [VERIFICATION.md](VERIFICATION.md) for the public summary and explicit remaining gaps.
