#!/bin/sh
# Cyberspace.miyoo — a cyberspace.online terminal for OnionOS
appdir=/mnt/SDCARD/App/Cyberspace.miyoo
cd "$appdir" || exit 1
# Coming from Dialup (the old name)? Bring its login, drafts and settings along once.
old=/mnt/SDCARD/App/Dialup/data
if [ ! -d data ] && [ -d "$old" ]; then
  cp -r "$old" data
  rm -f data/dialup.log data/stdout.log*
fi
mkdir -p data
log="$appdir/data/stdout.log"
if [ -f "$log" ] && [ "$(wc -c < "$log")" -gt 262144 ]; then mv "$log" "$log.1"; fi

export SSL_CERT_FILE="$appdir/cacert.pem"
export GODEBUG=tlsmlkem=0
touch /tmp/stay_awake            # don't auto-sleep while typing a long entry

"$appdir/cyberspace" >> "$log" 2>&1

rm -f /tmp/stay_awake
sync
