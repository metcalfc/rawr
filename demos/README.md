# Captured native Rex workflow

[Watch the main demo](../assets/demo.gif). In an existing Rex work window, bare `ghrawr`, `filerawr` and `exerawr` open independent native panes. Focusing each app's main list and pressing `q` quits it; Rex removes the pane while the original work shell stays alive. The capture uses development build `fd98579`, after v0.2.0, with labelled provider fixtures. Released v0.2.0 does not include close-on-exit.

The animation contains 14 timed GUI keyframes over 20.36 seconds (1,234,900 bytes). Its 1416×968 frame contains the real 1416×912 Rex image plus a 56-pixel caption bar below it. Those captions annotate actual command/`q` actions; they are outside the Rex UI. This is not continuous screen video. No central file opening, PR checkout, browser action or VM connection is shown. See [verification](../docs/VERIFICATION.md) for acceptance and limits; the [separate native screenshot](../assets/rex-resource-panes.png) is from an earlier walkthrough.

## Reproduce the native capture

Use the verified Rex version with a connected GUI client and turn Autobalance splits off for deliberate pane dimensions. Build apps from a source checkout containing close-on-exit; the captured source was `fd98579`. Build the [fixture setup helper](https://github.com/metcalfc/rawr/tree/main/scripts/acceptance-native-demo) and point it to those source-built apps:

```sh
make build
mkdir -p .acceptance/native-demo
go build -o .acceptance/native-demo/helper ./scripts/acceptance-native-demo
.acceptance/native-demo/helper setup \
  --bin "$PWD/build/bin" \
  --client CONNECTED_REX_CLIENT \
  --manifest .acceptance/native-demo/location.json
.acceptance/native-demo/helper focus \
  --manifest .acceptance/native-demo/location.json
```

Replace the connected Rex client label for your machine. Setup creates one shell in a disposable fixture project. Its journal refuses duplicate setup; it does not close or reload existing work. The local manifest keeps native identifiers under ignored `.acceptance/`. Newly launched source-built resource panes use close-on-exit; an already-running pane retains its original launch options.

1. Type `ghrawr` in the work terminal and capture its native pane opening.
2. Return focus to the same terminal, type `filerawr`, and capture its pane opening.
3. Return focus again, type `exerawr`, and capture all three native resource panes beside the original work.
4. Focus the main list in `exerawr`, `filerawr`, then `ghrawr`, pressing `q` in each and capturing its pane disappearing. In details/help, `q` first backs out; quit from the main list.
5. Return to the original work terminal and run `echo 'Still working here.'`. Capture that output with the shell still available.
6. Use computer-use screenshots to capture the real Rex window at those checkpoints, keeping the same window bounds. Convert screenshots to PNG if the capture tool returns JPEG. Optional caption bars belong below the Rex image. This capture renders SVG bars with `rsvg-convert` and appends them with ffmpeg's `scale`/`vstack` filters. Put the resulting PNGs and frame holds in a concat manifest, then encode the animation with [the native encoder](encode-native.sh):

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
