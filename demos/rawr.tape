# Current v0.2.0 standalone UI tour; not a native Rex workspace recording.
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
Set TypingSpeed 25ms

Hide
Type "source demos/session.sh"
Enter
Wait+Screen /RAWR v0.2.0/
Show
Sleep 2s

# Files: browse, filter, preview, then inspect actual worktree/staged diffs.
Type "filerawr --tui"
Enter
Wait+Screen /1 Tree/
Sleep 1s
Screenshot .acceptance/vhs-demo/tree.png
Type "/main.go"
Enter
Sleep 800ms
Type "p"
Wait+Screen /Preview:/
Sleep 2s
Screenshot .acceptance/vhs-demo/file-preview.png
Type "q"
Type "/"
Ctrl+U
Enter
Type "2"
Wait+Screen /worktree/
Wait+Screen /staged/
Sleep 2s
Screenshot .acceptance/vhs-demo/changes.png
Enter
Wait+Screen /diff --git/
Sleep 2500ms
Screenshot .acceptance/vhs-demo/worktree-diff.png
Type "q"
Down
Type "p"
Wait+Screen /Staged diff:/
Sleep 2s
Screenshot .acceptance/vhs-demo/staged-diff.png
Type "q"

Hide
Type "q"
Wait+Screen /\$ /
Ctrl+L
Show

# GitHub: PR checks, repository issues, then filter each resource list.
Type "ghrawr --tui"
Enter
Wait+Screen /#42/
Sleep 1s
Type "c"
Wait+Screen /Pane safety/
Sleep 2500ms
Screenshot .acceptance/vhs-demo/pr-checks.png
Type "q"
Type "2"
Wait+Screen /Browse terminal references/
Sleep 1500ms
Type "/references"
Enter
Wait+Screen /1 matching/
Sleep 2s
Screenshot .acceptance/vhs-demo/issues-filter.png
Type "1"
Type "/project"
Enter
Wait+Screen /1 matching/
Sleep 1500ms
Screenshot .acceptance/vhs-demo/pr-filter.png

Hide
Type "q"
Wait+Screen /\$ /
Ctrl+L
Show

# VMs: discovery and details only; no SSH or browser action is performed.
Type "exerawr --tui"
Enter
Wait+Screen /demo-api/
Sleep 1s
Down
Type "i"
Wait+Screen /SSH: demo-worker.invalid/
Sleep 2500ms
Screenshot .acceptance/vhs-demo/vm-details.png
Type "q"
Wait+Screen /demo-sandbox/
Down
Type "i"
Wait+Screen /Status: stopped/
Sleep 2s
Screenshot .acceptance/vhs-demo/vm-stopped.png

Hide
Type "q"
Type "q"
Wait+Screen /\$ /
Ctrl+L
Type "rawr_demo_outro"
Enter
Wait+Screen /Three tools/
Show
Sleep 2s
