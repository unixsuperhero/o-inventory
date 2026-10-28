# README demo

`demo.tape` records `assets/demo.gif`, the animation under the README header. It runs `o-inventory` over a throwaway hub and two invented tools that `seed.sh` writes.

Regenerate from the repository root (needs [VHS](https://github.com/charmbracelet/vhs) and Go; `brew install vhs`):

```sh
vhs docs/vhs/demo.tape
```

It takes under a minute and writes a GIF of about 29 seconds and under 400 KB.

## Your data stays out of it

Off camera, the tape makes a fresh `/tmp/oinv.XXXX` and runs `seed.sh` on it. The script builds `o-inventory` into `DIR/tool`, off PATH, so the inventory holds only the invented tools, and writes an `env` file that points `HOME`, every `XDG_*` directory, `O_INV_HUB` and `PATH` inside it, unsets `TUIOS_*`, `O_PROJ_MANIFEST` and `MTM_SERVER`, turns off your git config and defines an `o-inventory` function bound to the build. The hub is a fresh git repository in the sandbox, so `--commit` commits there. The tools `o-tea` and `o-miso` are shell scripts that answer `catalog`; `~/o-tea-0.2.0` is the next build of `o-tea` the demo installs (`leaf.show` removed, `leaf.steep` added with a `--minutes` param and the key `t`). There is no nabe registry, manifest or o-proj on PATH, so hosts and projects stay empty. The tape deletes the directory when it ends.

To look at the demo data by hand:

```sh
D=$(mktemp -d /tmp/oinv.XXXX) && docs/vhs/seed.sh "$D" && (. "$D/env" && o-inventory snapshot && o-inventory overview); rm -rf "$D"
```

## Changing it

Edit the steps between `Show` and `Hide`. The screen holds about 28 lines: `clear` before a long output. To stay small, keep the window size, the run short and the pauses few. Check a few frames before committing, for example:

```sh
ffmpeg -i assets/demo.gif -vf fps=1 /tmp/frame%02d.png
```
