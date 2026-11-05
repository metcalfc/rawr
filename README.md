<p align="center">
  <img src="assets/rawr-logo.png" alt="Rawr" width="220">
</p>

# Rawr

Small apps that open native resource panes in [Rex](https://www.superlogical.com/rex). Browse beside your terminal; use resource actions to drive the main work pane.

Run one from a Rex terminal in your project:

```sh
ghrawr    # GitHub PRs, issues and checks
filerawr  # Files and Git changes
exerawr   # exe.dev VMs and SSH
```

The optional `rawr` command manages projects and saved setups.

![Rawr panes opening in Rex, then closing with q](assets/demo.gif)

*Demo uses current source and fixture data.*

## Install

Install current source with Go 1.24+ for the behavior shown above:

```sh
go install github.com/metcalfc/rawr/cmd/ghrawr@main
go install github.com/metcalfc/rawr/cmd/filerawr@main
go install github.com/metcalfc/rawr/cmd/exerawr@main
go install github.com/metcalfc/rawr/cmd/rawr@main # optional
```

[Prebuilt v0.2.0 binaries for macOS/Linux](https://github.com/metcalfc/rawr/releases/tag/v0.2.0) are also available; they predate close-on-quit. See the [setup and usage guide](docs/USAGE.md) for Rex compatibility and dependencies.

## Write your own

Use [rawrkit](docs/RAWRKIT.md) for shared pane behavior and terminal UI. Start with the [noterawr example](examples/noterawr/main.go) and replace its resources and actions.

```sh
go get github.com/metcalfc/rawr@main
```
