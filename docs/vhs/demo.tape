# The README demo: o-inventory over a throwaway hub and two invented tools.
# Regenerate from the repository root:  vhs docs/vhs/demo.tape
#
# seed.sh builds o-inventory and makes everything under a fresh /tmp/oinv.XXXX:
# HOME, XDG dirs, a git hub and the fake tools o-tea and o-miso on PATH. The
# recording never reads or writes your own ~/hub, nabe registry or tools.

Output assets/demo.gif

Require go

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1080
Set Height 600
Set Padding 16
Set WindowBar Colorful
Set Framerate 15
Set TypingSpeed 45ms
Set CursorBlink false

# Off camera: build and seed.
Hide
Type "export D=$(mktemp -d /tmp/oinv.XXXX) && docs/vhs/seed.sh $D && . $D/env && clear"
Enter
Wait@90s /\$$/
Show
Sleep 600ms

# The first snapshot, committed in the hub, and what it holds.
Type "o-inventory snapshot --commit"
Enter
Sleep 1.2s
Type "o-inventory overview"
Enter
Sleep 2s

# A new build of o-tea: show is gone, steep is new with a key.
Type "cp ~/o-tea-0.2.0 ~/bin/o-tea"
Enter
Sleep 500ms
Type "o-inventory snapshot --commit"
Enter
Sleep 1.2s
Type "o-inventory diff"
Enter
Sleep 2.5s
Type "clear"
Enter
Sleep 300ms

# The changelog is rendered from the snapshots, never written by hand.
Type "o-inventory changelog --commit"
Enter
Sleep 1s
Type "sed -n 5,15p ~/hub/CHANGELOG.md"
Enter
Sleep 3s
Type "git -C ~/hub log --oneline"
Enter
Sleep 1.5s
Type "o-inventory verb list --format table"
Enter
Sleep 3s

Hide
Type "cd / && rm -rf $D"
Enter
Sleep 500ms
