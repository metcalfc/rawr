<p align="center">
  <img src="assets/rawr-logo.png" alt="Rawr" width="220">
</p>

# Rawr

Small terminal tools with a little roar.

Rawr is a family of Go apps for [Rex](https://www.superlogical.com/rex). Run one from your shell to open an independent native pane, then get back to work. Rex hosts the terminals; the apps stay anchored to your project.

| Command | What it does |
| --- | --- |
| `filerawr` | Browse your project files and preview their contents. |
| `ghrawr` | Follow GitHub pull requests and checks for your branch. |
| `exerawr` | Pick an existing exe.dev VM and put its SSH shell in the center. |
| `rawr` *(optional)* | Open named projects and deliberately save or restore their setups. |

## Get the little helpers

With Go 1.24 or later (tested with Go 1.27.1):

```sh
go install github.com/metcalfc/rawr/cmd/filerawr@latest
go install github.com/metcalfc/rawr/cmd/ghrawr@latest
go install github.com/metcalfc/rawr/cmd/exerawr@latest
# Optional projects and saved setups:
go install github.com/metcalfc/rawr/cmd/rawr@latest
```

Put your Go bin directory on `PATH`. From a project checkout inside Rex:

```sh
filerawr
ghrawr --repo OWNER/NAME
exerawr

ghrawr --toggle  # hide/show while keeping the app running
exerawr --local # bring retained local work back to the center
```

Selecting a VM keeps previous local work and VM shells running in native tabs. Use `--tui` to run an app's UI in your current interactive terminal. Rawr projects are optional.

To build from a checkout:

```sh
make build
export PATH="$PWD/build/bin:$PATH"
```

See the [usage guide](docs/USAGE.md) for roots, authentication, pane controls, saved setups, compatibility names and distribution builds.

## Make your own rawr

The public [rawrkit SDK](docs/RAWRKIT.md) gives Go apps the same native pane lifecycle, anchored context and optional terminal UI helpers. It has no dependency on Rawr projects or the three built-in apps. Start with the small [noterawr example](examples/noterawr/main.go).

## Verified habitat

Native integration is verified on **macOS**, with **Rex Beta 0.1.0 (1758)** and CLI/server **`0.0.1793645657-gd018d2`**, API **10**. Other Rex versions and remote endpoints are rejected until verified. For fixed pane sizes, turn **Autobalance splits off** in Rex settings.

The Go binaries cross-compile for macOS, Linux and Windows on arm64/amd64; native Rex integration on other platforms is unverified. GitHub uses your existing `git`/`gh` setup, and exe.dev uses existing SSH access. See the [public verification summary](docs/VERIFICATION.md) for concrete coverage and limits.

Rawr is an independent project, not affiliated with or endorsed by Rex or Superlogical. Rex and other product names and marks belong to their respective owners.
