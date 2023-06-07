#!/usr/bin/env bash

# first two connections should succeed, and time out after 10 seconds (as no TLS handshake)
# second two connections should be immediately dropped

for x in `seq 1 4`;
do
        netcat feed.push.plane.watch 22345 &
done
