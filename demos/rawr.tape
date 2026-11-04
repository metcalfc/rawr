# Run ./demos/record.sh from the repository root. See demos/README.md.
Output assets/demo.gif

Require bash
Require git
Require filerawr
Require ghrawr
Require exerawr

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 20
Set Width 1024
Set Height 640
Set Padding 20
Set WindowBar Colorful
Set Theme "Catppuccin Mocha"
Set Framerate 12
Set CursorBlink false
Set TypingSpeed 20ms

Hide
Type "source demos/session.sh"
Enter
Sleep 300ms
Show
Sleep 2s

Type `filerawr --tui --context-file "$RAWR_DEMO_CONTEXT"`
Enter
Sleep 2s
Right
Sleep 700ms
Down
Enter
Sleep 3s
Type "q"
Sleep 500ms

Hide
Type "q"
Sleep 200ms
Ctrl+L
Show
Type `ghrawr --tui --context-file "$RAWR_DEMO_CONTEXT"`
Enter
Sleep 2s
Type "c"
Sleep 3s
Type "q"
Down
Sleep 1s
Up
Type "r"
Sleep 1s

Hide
Type "q"
Sleep 200ms
Ctrl+L
Show
Type `exerawr --tui --context-file "$RAWR_DEMO_CONTEXT"`
Enter
Sleep 2s
Down
Sleep 1s
Down
Sleep 1s
Up
Type "r"
Sleep 1s

Hide
Type "q"
Sleep 200ms
Ctrl+L
Type "printf 'Three real Go UIs. One shared rawrkit.\n\nGitHub + VM data: fixtures, not live providers.\nNative Rex panes are outside this terminal recording.\n'"
Enter
Show
Sleep 2s
