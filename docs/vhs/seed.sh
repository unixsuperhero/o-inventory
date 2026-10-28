#!/usr/bin/env bash
# seed.sh DIR: build o-inventory into DIR/tool and make a throwaway world in
# DIR for the README demo: HOME, the XDG directories, a hub that is a fresh
# git repository, and two invented family tools on PATH (o-tea and o-miso,
# shell scripts that answer `catalog`). Nothing reads or writes your own
# ~/hub, nabe registry or tools. It writes DIR/env, which points HOME, the
# XDG directories, O_INV_HUB and PATH inside DIR and defines an o-inventory
# function bound to the build.
#
#	D=$(mktemp -d /tmp/oinv.XXXX) && docs/vhs/seed.sh "$D" && . "$D/env" && o-inventory snapshot
set -euo pipefail

D=${1:?usage: seed.sh DIR}
D=$(cd "$D" && pwd -P)
mkdir -p "$D"/{tool,home/bin,home/hub,config,data,cache,state}
# The build stays off PATH, so the inventory holds only the invented tools.
GOWORK=off go build -o "$D/tool/o-inventory" .

cat > "$D/env" <<ENV
unset TUIOS_ENV TUIOS_SESSION TUIOS_PANE_ID TUIOS_SOCKET O_PROJ_MANIFEST MTM_SERVER
export HOME=$D/home XDG_CONFIG_HOME=$D/config XDG_DATA_HOME=$D/data
export XDG_CACHE_HOME=$D/cache XDG_STATE_HOME=$D/state O_INV_HUB=$D/home/hub
export PATH=$D/home/bin:/usr/bin:/bin GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=demo GIT_AUTHOR_EMAIL=demo@example.com GIT_COMMITTER_NAME=demo GIT_COMMITTER_EMAIL=demo@example.com
o-inventory() { "$D/tool/o-inventory" "\$@"; }
PS1='\$ '
cd "$D/home"
ENV
# shellcheck disable=SC1091
. "$D/env"

# tool NAME VERSION FILE NOUNS BINDINGS: a fake family tool whose catalog
# is the given nouns and bindings.
tool() {
	cat > "$3" <<TOOL
#!/bin/sh
case "\$1" in
  catalog) cat <<'JSON'
{"schema":1,"name":"$1","version":"$2","nouns":[{"name":"app","verbs":[{"name":"quit","kind":"interaction","result":"boolean"}]},$4],
 "bindings":[{"noun":"app","mode":"normal","keys":"q","request":{"noun":"app","verb":"quit"}}$5],"pages":[{"id":"main","title":"Main"}]}
JSON
  ;;
  *) echo "$1: unknown command \$1" >&2; exit 1 ;;
esac
TOOL
	chmod +x "$3"
}
app() { printf '{"name":"%s","kind":"application"%s}' "$1" "${2:+,$2}"; }

leaf="{\"name\":\"leaf\",\"verbs\":[$(app list '"rows":"leaf"'),$(app show),$(app brew)]}"
tool o-tea 0.1.0 "$HOME/bin/o-tea" "$leaf" ''
bowl="{\"name\":\"bowl\",\"verbs\":[$(app list '"rows":"bowl"'),$(app stir)]}"
tool o-miso 0.3.1 "$HOME/bin/o-miso" "$bowl" ',{"noun":"bowl","mode":"normal","keys":"s","request":{"noun":"bowl","verb":"stir"}}'

# The next build of o-tea, which the demo installs: show is gone, steep is
# new, with a key.
leaf2="{\"name\":\"leaf\",\"verbs\":[$(app list '"rows":"leaf"'),$(app brew),$(app steep '"params":{"minutes":{"type":"number"}}')]}"
tool o-tea 0.2.0 "$HOME/o-tea-0.2.0" "$leaf2" ',{"noun":"leaf","mode":"normal","keys":"t","request":{"noun":"leaf","verb":"steep"}}'

git -C "$HOME/hub" init -q -b main
git -C "$HOME/hub" commit -q --allow-empty -m "hub"
