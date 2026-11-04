# Verification

Local verification was performed on macOS arm64 on November 4, 2026, using Go 1.27.1 and Rex `0.0.1793645657-gd018d2` (API 10). The Go module requires Go 1.24 or later.

## Automated checks

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

## Real Rex walkthrough

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
