#!/usr/bin/env sh
# Serve the demo workspace on your local network so you can try yip from a
# phone or another computer while developing. Plain HTTP and demo data only:
# don't use this for a real workspace (see docs/operations.md, Remote access).
#
#   http://<this-machine>:7721  the built app embedded in the hub
#   http://<this-machine>:5173  the live-reload dev client (proxies /v1 to the hub)
#
# The demo engineers use the scripted fake provider. Set YIP_LAN_PROVIDERS
# (for example codex,claude) to also offer those installed CLIs with the
# sign-in they already have; engineers switched to them run on your account.
# Ctrl-C stops both.
set -eu
cd "$(dirname "$0")/.."
DATA=${YIP_LAN_DATA:-$HOME/.yip/demo-lan}

[ -x bin/yip ] || make all
./bin/yip demo --data "$DATA" --listen 0.0.0.0:7721 --insecure-http --runner-listen 127.0.0.1:7744 --with-providers "${YIP_LAN_PROVIDERS:-}" &
HUB=$!
trap 'kill "$HUB" 2>/dev/null' EXIT INT TERM

for ip in $(ipconfig getifaddr en0 2>/dev/null) $(ipconfig getifaddr en1 2>/dev/null) $(hostname -I 2>/dev/null); do
  echo "yip on your network: http://$ip:7721 (built)  http://$ip:5173 (live reload)"
done
echo "Sign-in details are in $DATA/demo-credentials.txt"

cd web && YIP_HUB=http://127.0.0.1:7721 npx vite --host 0.0.0.0 --port 5173
