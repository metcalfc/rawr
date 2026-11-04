# Earlier standalone terminal recording

[Watch the existing GIF](../assets/demo.gif). It follows the v0.1.0-era interaction and is kept as a labelled reference. It runs the actual Go `filerawr`, `ghrawr`, and `exerawr` terminal UIs in one VHS terminal. File browsing and local preview use real files in a disposable workspace. GitHub PR/check and VM records come from the explicitly labelled fixture adapters here, not live provider results.

This recording does not show native Rex panes, central file/diff or SSH switching, PR checkout, or v0.2.0 resource controls. It does not record or modify a Rex session, open a remote shell or contact GitHub/exe.dev. Native acceptance belongs in [verification](../docs/VERIFICATION.md); the GIF is not evidence that a native workflow or v0.2.0 feature passed. The separate [v0.2.0 native screenshot](../assets/rex-resource-panes.png) shows real Rex panes with labelled GitHub/VM fixture data.

## Reproduce the earlier flow

Use the [v0.1.0 recording source](https://github.com/metcalfc/rawr/tree/v0.1.0/demos) in a separate checkout to reproduce that earlier interaction. Install Go, Git, [VHS](https://github.com/charmbracelet/vhs), `ttyd`, and `ffmpeg`. VHS 0.12.1, ttyd 1.7.7, and ffmpeg 9.0.2 were used for the published GIF.

```sh
# macOS / Homebrew
brew install vhs ttyd ffmpeg

# From the recording checkout's repository root:
./demos/record.sh
```

The runner builds the apps from whichever checkout it is run in. It creates a disposable `/tmp/rawr-demo.*` workspace, validates the tape, renders `assets/demo.gif`, and removes its temporary files. To use extracted v0.1.0 app binaries instead of building apps:

```sh
RAWR_DEMO_BIN="/path/to/extracted/rawr-apps_v0.1.0_darwin_arm64" ./demos/record.sh
```

Using v0.2.0 or later binaries records their UI and may require adapting the tape and fixture commands. The v0.1.0 GIF does not automatically become a demonstration of the newer version. A re-record overwrites `assets/demo.gif`; inspect the frames and update its caption/version scope together. VHS still captures a terminal rather than the surrounding native Rex workspace. Recording scripts and fixtures are available in the source repository, not the app release archives.

Only the new VHS shell receives the fixture `PATH`; the adapters never delegate to the real `gh` or `ssh` commands. The session has no Rex identity or provider credential variables. The tape uses no clipboard contents. The optional `Menlo` font may fall back to another monospace font on Linux.

The `.tape` uses the [official VHS command reference for 0.12.1](https://github.com/charmbracelet/vhs/blob/v0.12.1/README.md#vhs-command-reference). Edit `demos/rawr.tape` to change timing, font, dimensions, or navigation; edit `demos/fixtures/*.json` to change sample provider data. Fixture PR URLs and `.invalid` machine destinations are not opened by this tape.
