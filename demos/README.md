# v0.2.0 terminal UI tour

[Watch the GIF](../assets/demo.gif). It runs the actual v0.2.0 Go apps in one isolated VHS terminal:

- `filerawr`: Tree, filename filtering, local preview, and Git Changes/diff in a disposable local repository.
- `ghrawr`: PRs, issues, list filtering and selected-PR checks, using labelled fixture provider data.
- `exerawr`: VM selection and details from discovery fixtures, without connecting.

The current recording is 1024×640 and 30.56 seconds (about 759 kB), rendered with VHS 0.12.1 using the released macOS arm64 v0.2.0 binaries. Decoded GIF frames were inspected after rendering.

The recording shows standalone `--tui` views. It does not show native Rex panes, central work switching, browser actions, PR checkout or SSH connections, and does not contact live GitHub/exe.dev providers. The separate [native screenshot](../assets/rex-resource-panes.png) shows real Rex panes with labelled GitHub/VM fixtures. See [verification](../docs/VERIFICATION.md) for native acceptance and its limits.

This is a media refresh after the v0.2.0 release. The published tag and release archives are unchanged and retain their earlier recording/runner. Use the [current recorder source on main](https://github.com/metcalfc/rawr/tree/main/demos) to reproduce this tour.

## Reproduce the tour

Install Git, [VHS](https://github.com/charmbracelet/vhs), `ttyd` and `ffmpeg`. Go 1.24 or later is needed when building the apps; extracted v0.2.0 binaries can be used instead.

```sh
# macOS / Homebrew
brew install vhs ttyd ffmpeg

# From the current source checkout's repository root:
./demos/record.sh
```

The runner builds the apps from its source checkout by default. It creates a disposable `/tmp/rawr-demo.*` workspace and local Git repository, validates the tape, renders `assets/demo.gif`, and removes its temporary files. To use extracted v0.2.0 app binaries instead:

```sh
RAWR_DEMO_BIN="/path/to/extracted/rawr-apps_v0.2.0_darwin_arm64" ./demos/record.sh
```

The path must contain native executables for the recording machine. A re-record overwrites `assets/demo.gif`; inspect its frames and update the captions if the flow changes. Later app versions may require tape/fixture changes. Recording scripts and fixtures are in the source repository, not the app release archives.

The recorder starts with an allowlisted child environment and fixture `PATH`; the adapters never delegate to real `gh` or `ssh` commands. No provider credentials or Rex session identity are inherited. Fixture commits use a fake identity with user/system Git configuration, signing and hooks disabled. Bash skips user rc/profile files; the caller's home identity is preserved only for the installed VHS browser cache, while app configuration/state paths are private. The tape uses no clipboard contents. The optional `Menlo` font may fall back to another monospace font on Linux.

The runner also saves checkpoint screenshots under ignored `.acceptance/vhs-demo/`. Inspect frames decoded from the finished GIF as well as those checkpoints when reviewing a new recording.

The tape uses the [official VHS command reference for 0.12.1](https://github.com/charmbracelet/vhs/blob/v0.12.1/README.md#vhs-command-reference). Edit `demos/rawr.tape` for timing, dimensions or navigation; `demos/session.sh` prepares the recording terminal, and `demos/fixtures/*.json` supplies provider records. Fixture PR URLs and `.invalid` VM destinations are not opened by this tape.
