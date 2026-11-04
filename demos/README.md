# Terminal demo

[Watch the GIF](../assets/demo.gif). The recording runs the actual Go `filerawr`, `ghrawr`, and `exerawr` terminal UIs in one VHS terminal. File browsing and preview use real files in a disposable workspace. GitHub pull requests/checks and VM records come from the explicitly labeled fixture adapters in this directory; they are not live provider results.

The demo does not record or modify a Rex session, open a remote shell, contact GitHub or exe.dev, or demonstrate native Rex pane operations. Native pane acceptance is described in [verification](../docs/VERIFICATION.md).

## Reproduce

Install Go, Git, [VHS](https://github.com/charmbracelet/vhs), `ttyd`, and `ffmpeg`. VHS 0.12.1, ttyd 1.7.7, and ffmpeg 9.0.2 were used for the published recording.

```sh
# macOS / Homebrew
brew install vhs ttyd ffmpeg

# From the repository root:
./demos/record.sh
```

The runner builds the three current Go commands, creates a disposable `/tmp/rawr-demo.*` workspace, validates the tape, renders `assets/demo.gif`, and removes its temporary files. To record already-built or released binaries instead:

```sh
RAWR_DEMO_BIN="$PWD/build/bin" ./demos/record.sh
```

Only the new VHS shell receives the fixture `PATH`; the adapters never delegate to the real `gh` or `ssh` commands. The session has no Rex identity or provider credential variables. The tape uses no clipboard contents. The optional `Menlo` font may fall back to another monospace font on Linux.

The `.tape` uses the [official VHS command reference for 0.12.1](https://github.com/charmbracelet/vhs/blob/v0.12.1/README.md#vhs-command-reference). Edit `demos/rawr.tape` to change timing, font, dimensions, or navigation; edit `demos/fixtures/*.json` to change provider data. Fixture PR URLs and `.invalid` machine destinations are never opened by the tape.
