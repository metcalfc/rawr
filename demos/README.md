# Captured native Rex workflow

[Watch the main demo](../assets/demo.gif). It starts from a working terminal in the actual Rex GUI. Running `ghrawr`, `filerawr` and `exerawr` opens independent native resource panes; the original local work stays available. The workflow browses GitHub resources, displays the VM pane, opens a selected local file in the center, then returns the retained shell to the center. GitHub/VM provider data are labelled fixtures.

The animation is assembled from GUI screenshots captured during that workflow. Native Rex chrome, panes and tabs are part of those captures. It does not demonstrate a live-provider checkout, browser action or SSH connection. See [verification](../docs/VERIFICATION.md) for native acceptance and limits; the [separate native screenshot](../assets/rex-resource-panes.png) is also available.

## Reproduce the native capture

Use the verified Rex version with a connected GUI client and turn Autobalance splits off for deliberate pane dimensions. From the current source checkout, build the [fixture setup helper](https://github.com/metcalfc/rawr/tree/main/scripts/acceptance-native-demo) and pass the explicit directory containing the released native v0.2.0 apps:

```sh
mkdir -p .acceptance/native-demo
go build -o .acceptance/native-demo/helper ./scripts/acceptance-native-demo
.acceptance/native-demo/helper setup \
  --bin /absolute/path/to/rawr-apps_v0.2.0_darwin_arm64 \
  --client CONNECTED_REX_CLIENT \
  --manifest .acceptance/native-demo/location.json
.acceptance/native-demo/helper focus \
  --manifest .acceptance/native-demo/location.json
```

Replace the app directory and connected Rex client label for your machine. Setup creates one shell in a disposable fixture project. Its journal refuses duplicate setup; it does not close or reload existing work. The local manifest keeps native identifiers under ignored `.acceptance/`.

1. Type `ghrawr` in the work terminal. Capture the opened native pane, browse issues/checks, and return focus to the still-usable work terminal.
2. Type `filerawr` in that terminal and capture its native file pane.
3. Return focus to the work terminal and type `exerawr`. Capture its native VM pane and fixture details without connecting.
4. In the file pane, select the README and press Enter to open its read-only view in the center. Capture the original work retained in its native history tab.
5. Select **Work · previous**, type `exerawr --local` in the original shell, then explicitly select the original resource tab. The local shell is back in the center and the file viewer stays retained in a tab. `--local` restores work placement; it does not automatically switch the active native tab.
6. Use computer-use screenshots to capture the real Rex window at those checkpoints, keeping the same window bounds. Convert screenshots to PNG if the capture tool returns JPEG. Put the PNGs and frame holds in a concat manifest, then encode the animation with [the native encoder](encode-native.sh):

```sh
./demos/encode-native.sh .acceptance/native-demo/frames.ffconcat
```

The encoder uses ffmpeg's concat input and `palettegen`/`paletteuse` filters and writes `assets/demo.gif` by default. Frame holds set the animation's pacing.

This capture requires the actual Rex application and GUI interaction. The optional VHS runner below records a separate standalone terminal tour. Recorder source and media updates live on [main](https://github.com/metcalfc/rawr/tree/main/demos); the published v0.2.0 tag and release archives remain unchanged.

## Optional standalone VHS tour

[Watch the standalone tour](../assets/standalone-demo.gif). It runs the actual v0.2.0 Go apps in one isolated VHS terminal:

- `filerawr`: Tree, filename filtering, local preview, and Git Changes/diff in a disposable local repository.
- `ghrawr`: PRs, issues, list filtering and selected-PR checks, using labelled fixture provider data.
- `exerawr`: VM selection and details from discovery fixtures, without connecting.

This standalone recording is 1024×640 and 30.56 seconds (about 759 kB), rendered with VHS 0.12.1 using the released macOS arm64 v0.2.0 binaries. Decoded GIF frames were inspected after rendering.

VHS captures standalone `--tui` views. This secondary tour does not display native Rex panes, central work switching, browser actions, PR checkout or SSH connections, and does not contact live GitHub/exe.dev providers.

### Reproduce the standalone tour

Install Git, [VHS](https://github.com/charmbracelet/vhs), `ttyd` and `ffmpeg`. Go 1.24 or later is needed when building the apps; extracted v0.2.0 binaries can be used instead.

```sh
# macOS / Homebrew
brew install vhs ttyd ffmpeg

# From the current source checkout's repository root:
./demos/record.sh
```

The runner builds the apps from its source checkout by default. It creates a disposable `/tmp/rawr-demo.*` workspace and local Git repository, validates the tape, renders `assets/standalone-demo.gif`, and removes its temporary files. To use extracted v0.2.0 app binaries instead:

```sh
RAWR_DEMO_BIN="/path/to/extracted/rawr-apps_v0.2.0_darwin_arm64" ./demos/record.sh
```

The path must contain native executables for the recording machine. A standalone re-record overwrites `assets/standalone-demo.gif`, leaving the main native Rex animation separate. Inspect its frames and update captions if the flow changes. Later app versions may require tape/fixture changes. Recording scripts and fixtures are in the source repository, not the app release archives.

The recorder starts with an allowlisted child environment and fixture `PATH`; the adapters never delegate to real `gh` or `ssh` commands. No provider credentials or Rex session identity are inherited. Fixture commits use a fake identity with user/system Git configuration, signing and hooks disabled. Bash skips user rc/profile files; the caller's home identity is preserved only for the installed VHS browser cache, while app configuration/state paths are private. The tape uses no clipboard contents. The optional `Menlo` font may fall back to another monospace font on Linux.

The runner also saves checkpoint screenshots under ignored `.acceptance/vhs-demo/`. Inspect frames decoded from the finished GIF as well as those checkpoints when reviewing a new recording.

The tape uses the [official VHS command reference for 0.12.1](https://github.com/charmbracelet/vhs/blob/v0.12.1/README.md#vhs-command-reference). Edit `demos/rawr.tape` for timing, dimensions or navigation; `demos/session.sh` prepares the recording terminal, and `demos/fixtures/*.json` supplies provider records. Fixture PR URLs and `.invalid` VM destinations are not opened by this tape.
