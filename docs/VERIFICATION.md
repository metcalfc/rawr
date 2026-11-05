# Verification

Local verification was performed on macOS arm64 on November 4, 2026, using Go 1.27.1 and Rex `0.0.1793645657-gd018d2` (API 10). The Go module requires Go 1.24 or later.

## Capability check before the resource-pane refinement

On November 4, the installed Rex skill and its CLI-generated guide were read again before choosing the integration. A live `rex version`, `whoami`, API listing, and schemas for `session.new_window` and `session.swap_blocks` confirmed the same local server version and API revision 10. The server was not restarted and existing sessions were not changed for discovery.

| Need | Verified capability and integration choice |
| --- | --- |
| Independent resource panes | Retain the existing native terminal blocks, open/focus/hide/toggle lifecycle and explicit launch context. |
| A file, diff or VM in the center | Create/reuse a declared terminal in a native tab, then `session.swap_blocks`. Its schema explicitly preserves split IDs, sizes and window order. Retain previous work in its tab. |
| Safe commands | Literal argv in a newly hosted terminal, or a bounded local subprocess anchored to the checkout. Never write launch commands into another terminal. |
| Activity | The installed terminal API exposes `program_status`, with statuses reported by programs. No new agent-state detector or activity host is justified by this inspection. |
| Deliberate setups | Keep Rawr's existing explicit save/restore layer and native bindings. A resource action does not save a new setup. |

This check establishes the available API, not acceptance of the new interactions. Runtime results for this refinement are recorded separately below. No Herdr plugin transport or universal compatibility host is introduced. Remote Rex and other Rex versions remain outside the verified integration.

## Resource pane exit: v0.2.1

The installed Rex skill, live identity, API list and terminal creator schema were inspected again before this change. The same tested Rex version exposes `exit.on_completion` at terminal creation; its default quick-failure threshold is 250 ms. No runtime exit-policy setter was found in the installed API. `rex.AppLeaf` enables native completion cleanup for managed resource apps, including Rawr's initial and restored app roles. Work shells, SSH terminals and central file/diff viewers continue using retained `rex.Leaf` surfaces. Existing panes keep their original launch options.

Real native checks used current source builds and labelled GitHub/VM fixtures in a fresh Rex workspace. Literal `q` was sent only to each owned resource TUI after verifying its native block, executable, PID and alternate screen. Rex removed each of `ghrawr`, `filerawr` and `exerawr` from the complete block inventory. An explicit reopen created one fresh block; another open reused it. Both fixture work shells and sibling app bindings/processes survived. Hiding retained an app's process, quitting it while detached removed its surface, and reopening created one fresh pane. A separate quick nonzero-exit fixture stayed open with its error status and was then closed after verifying it had no live program. The ended earlier demo shell was preserved rather than reused. No server restart or launch-text injection was used.

Targeted regressions cover absent app reconciliation, hidden sibling retention, exits during open/hide/show, and one replacement per explicit project restore. Recovery of a known absent app journal happens only on a later explicit mutation. Unknown creation IDs, changed/live blocks, incomplete show witnesses and detached or changed surviving work retain their journals for inspection. Read-only status never clears a journal. `go test -race ./...`, `go vet ./...`, the host build and the external SDK smoke passed. This close-on-exit behavior is included in v0.2.1; the older v0.2.0 tag, archives and already-running panes are unchanged. Release artifact checks and CI are recorded in the [v0.2.1 release notes](https://github.com/metcalfc/rawr/releases/tag/v0.2.1).

### Native open/quit animation

The updated main demo uses current-source development build `fd98579`, after v0.2.0, in an existing Rex work window. Computer-use interaction entered bare `ghrawr`, `filerawr` and `exerawr` in the same original shell. Each app was focused at its main list before `q` quit `exerawr`, `filerawr`, then `ghrawr`; Rex removed each native pane. Complete block counts verified the sequence 4 → 3 → 2 → 1. The original shell PID stayed unchanged, and sibling app IDs/PIDs remained until each quit. The final `echo 'Still working here.'` succeeded in that same shell. Provider data are labelled fixtures; no central file opening, provider checkout, browser action or VM connection was performed.

The 1416×968 GIF runs for 20.36 seconds (1,234,900 bytes), with 14 timed keyframes from actual GUI screenshots. The native Rex image is 1416×912; a separate 56-pixel caption bar is appended below it. SVG bars were rendered with `rsvg-convert` and stacked using ffmpeg's `scale`/`vstack`, then the native encoder applied concat/palette encoding. Captions annotate actual `q` presses and observed pane removal outside the Rex UI. Decoded GIF frames were inspected for the quit instruction, subsequent removed pane and final retained work. This is an animation of captured checkpoints, not continuous video. The published v0.2.0 binaries and archives are unchanged.

## Resource-pane refinement: v0.2.0

The following checks cover v0.2.0. Earlier v0.1.0 evidence is retained separately below.

- `go test ./...`, `go test -race ./...`, `go vet ./...`, and the four-command host build passed. Coverage includes repository-wide PR/issues and partial-refresh retention, frozen checkout confirmation and bounded outcomes, clean-worktree and repository checks, native local-shell guards, file/subdirectory confinement, staged/worktree/deleted diffs, literal Git paths, asynchronous viewer errors/cancellation, and ended-view reopening without discarding its surface.
- All six app-opening orders were tested against the independent Files/work/GitHub/VM layout. Generic resource swaps preserve local processes, focus the selected work and issue no terminal input, close or resize calls.
- All four apps cross-compiled with CGO disabled for macOS, Linux and Windows on arm64/amd64 (24 binaries). Target/build metadata, formats and checksums were checked; the four macOS arm64 executables ran. A separate consumer of the public SDK compiled for all six targets and ran its custom app/context and simulation-screen checks on macOS arm64.
- Silent CLI defaults were checked separately after the refinement: command tests and their race/vet checks passed, all four host commands rebuilt, and all three app launchers were exercised against existing native fixture panes. Successful default invocations emitted no stdout/stderr; `--verbose` returned result JSON; `--status` still returned JSON. Existing pane IDs and the main work association were retained. Regression checks also cover compatibility names, local return, visible failures and unchanged Rawr project output.
- Release-readiness regressions cover the acceptance helper's durable creation/failure journal, refused automatic retries, and verification of every app's host/generation/label/PID/path and foreground before any fixture reload closes a pane. Changed, busy or unknown work is refused.
- A read-only live GitHub smoke against `cli/cli` returned 61 open PRs, the bounded 100 open issues and 20 checks for one selected PR. No real GitHub checkout or repository mutation was performed by this smoke.

### Actual Rex resource walkthrough

Isolated workspaces used real native Rex panes with explicitly labelled local GitHub/VM fixtures and disposable Git repositories. Native accessibility and screen inspection verified issue/browser dispatch, selected-PR checks, PR confirmation and successful checkout routing, Tree-to-center file opening, Changes-to-center diff opening, VM details and a simulated VM terminal swap. The fixture checkout adapter ran a real local Git branch switch; it did not exercise GitHub's network checkout implementation. The VM terminal was explicitly simulated and made no SSH connection. Existing real SSH coverage belongs to the baseline below.

The original directly hosted Bash shell kept its PID through checkout, resource swaps and return to local work. Native split geometry was checked against the original 20% Files / 70% remaining-work split / 50% right-column split. Mixed hide/show retained resource IDs, processes and geometry around the central diff. Reopening an explicitly quit viewer created a fresh owned view while retaining its exited surface. No server restart or launch-command injection was used; unrelated user sessions were preserved.

The README's [native pane screenshot](../assets/rex-resource-panes.png) was captured from this workspace. Its project files are real disposable files and all provider identities/data are fixtures. The main [native Rex GIF](../assets/demo.gif) covers the later source open/quit workflow described above; the [standalone VHS tour](../assets/standalone-demo.gif) is a separate optional recording.

The earlier post-release native capture used the released v0.2.0 macOS arm64 apps from commit `f8835bc` in one newly created Rex work terminal. Bare `ghrawr`, `filerawr` and `exerawr` opened native resource panes from that same shell. It showed fixture issues/checks and VM details, README selection opening a read-only view in the center, the retained original work tab, and `exerawr --local` returning that shell to the resource layout. The resource tab was explicitly selected after local return. Native API checks verified the original shell PID, all three app IDs/process bindings and split geometry through the file swap and local return.

That earlier GIF was 1416×912, 27.56 seconds (1,022,087 bytes), with 14 timed GUI keyframes. Its decoded frames were inspected, including the opened GitHub pane, central file and final retained shell. It has been superseded as the main demo. No provider browser, checkout or SSH connection was performed. The [native fixture helper](https://github.com/metcalfc/rawr/tree/main/scripts/acceptance-native-demo) creates only the initial shell, journals native ownership before creation and refuses automatic setup retries. Pane launch commands were entered through the GUI into that known idle fixture shell. Unrelated sessions were preserved.


The separate post-release VHS refresh used the released v0.2.0 macOS arm64 apps from commit `f8835bc`. It shows filename filtering/local previews, real worktree and staged diffs in a disposable repository, fixture PR/issues filtering and checks, and fixture VM details. The 1024×640 GIF runs for 30.56 seconds (758,510 bytes). The tape's screen-readiness checks completed; decoded GIF frames and the ending were visually inspected. No browser, checkout, SSH connection or Rex action was performed by the recording. Fixture Git uses an isolated environment with signing/hooks disabled, and the recorder receives no provider credentials or Rex session identity. The published v0.2.0 tag and archives retain their original release-time recording; current recorder source and media are on main. Its tape writes `assets/standalone-demo.gif` and cannot overwrite the main native demo.

### Concrete limits found

Default macOS login terminals and wrapped terminals can expose only `login` or a terminal wrapper as their foreground process, with no verifiable idle shell/CWD. Checkout correctly refused those states; prompt text was not used as evidence. Positive native checkout acceptance used a directly hosted Bash with `--noprofile --norc -i`, whose child/foreground process, root and primary screen Rex could verify. This does not establish checkout support for every normal user shell configuration.

The pane-state lock serializes Rawr operations in that scope; it does not lock out other clients' input or background filesystem writers. Failed/timed-out Git checkouts require inspection before retrying. Linux/Windows native execution and Rex integration, terminal References, content search, editor integration, pin/preview policies and extra activity features remain unverified or unimplemented as described in the usage guide.

The fixture helper is [scripts/acceptance-refinement](https://github.com/metcalfc/rawr/blob/v0.2.0/scripts/acceptance-refinement/main.go). It records local workspace IDs under `.acceptance`, refuses duplicate setup, and leaves its final native workspace available for inspection. Local logs, hashes and raw native identifiers remain excluded from public documentation. These are local acceptance checks; CI and published artifacts are tracked separately for the exact source commit.

## Automated checks: v0.1.0 baseline

- The Go unit suite, race suite, and `go vet` passed. Tests cover anchored context, literal argv, native ownership and process fingerprints, interrupted-operation journals, retained panes, terminal swaps, deliberate save/restore, and concurrent atomic state updates.
- Simulation-screen tests cover the three built-in UIs and an unrelated app using only the public UI package: list navigation, scrolling, mouse activation, Unicode rendering, refresh cancellation, stale results, and clean shutdown.
- A separate temporary Go module imported only `rawrkit`, `rawrkit/rex`, and `rawrkit/ui`. Its custom app compiled for all six release targets; its host executable and simulation UI checks passed.
- Release archives cross-compiled with `CGO_ENABLED=0` for macOS, Linux, and Windows on both amd64 and arm64. Archive contents, SHA-256 checksums, host executable versions, and isolated installation were checked.

```sh
go test ./...
go test -race ./...
go vet ./...
make build
make sdk-smoke
make release VERSION=dev COMMIT=local
```

The repository includes a sanitized Rex response fixture in `rawrkit/rex/testdata`. It preserves the verified API shape with synthetic identifiers; tests do not depend on private acceptance captures.

## Real Rex walkthrough: v0.1.0 baseline

The native walkthrough verified that repeated app opens reuse the same panes and processes; mixed hide/show operations retain their IDs, processes, topology, and ratios; and commands run inside an existing project reuse its shared app scope. An unrelated SDK app used the same native lifecycle.

Existing SSH sessions were opened in the center and reused when selected again. Previous local work survived in a native tab and returned to the center with the same process. These operations used native swaps without terminal input injection or resize calls.

Project checks covered opening twice, preserving temporary arrangements on open, explicit saved revisions, exact topology/ratio restore, retained hidden panes, stale-save refusal, and partial restore that preserves extra live work. An explicitly closed app recreated by its standalone command was recognized without duplication.

Restart checks distinguished persisted native surfaces from fresh shell processes: old programs and output were not claimed restored, and no relaunch text was typed into retained terminals. Legacy Go app names were recognized through verified bindings without replacing their live processes.

Read-only service checks used existing GitHub and SSH authentication. A public pull request returned its checks through the Go GitHub adapter, and exe.dev discovery parsed existing machines. No machine lifecycle changes or credential copying were performed.

## Limits

- Real terminal and Rex acceptance was performed on macOS arm64. Other targets were cross-compiled, not verified as running Rex installations.
- The integration is gated to the tested Rex version. A newer version requires capability verification.
- Exact geometry requires Rex's **Autobalance splits** setting to be off.
- Saved setups contain topology and declared launch recipes. Terminal processes, transcripts, and conversations are not snapshots; old running binaries are not hot-upgraded.
- Release archives are unsigned. Local checks do not establish a GitHub Actions result.

Raw local screenshots, terminal captures, machine identities, and historical reports are intentionally not committed. They remain local verification artifacts rather than public test fixtures.
