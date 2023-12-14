#!/usr/bin/env bash
# shellcheck shell=bash

EXITCODE=0

# check beast connection inbound from bordercontrol
CONNECTED_BEAST_IN=false
if ss -ntH state established | tr -s " " | cut -d " " -f 3 | grep ":12345" > /dev/null 2>&1; then
    CONNECTED_BEAST_IN=true
    echo "CONNECTED_BEAST_IN=true"
else
    echo "CONNECTED_BEAST_IN=false"
    EXITCODE=1
fi

# check beast connection outbound to mux
CONNECTED_BEAST_OUT=false
if ss -ntH state established | tr -s " " | cut -d " " -f 4 | grep ":12345" > /dev/null 2>&1; then
    CONNECTED_BEAST_OUT=true
    echo "CONNECTED_BEAST_OUT=true"
else
    echo "CONNECTED_BEAST_OUT=false"
    EXITCODE=1
fi

# check nats connection outbound
CONNECTED_NATS_OUT=false
if ss -ntH state established | tr -s " " | cut -d " " -f 4 | grep ":4222" > /dev/null 2>&1; then
    CONNECTED_NATS_OUT=true
    echo "CONNECTED_NATS_OUT=true"
else
    echo "CONNECTED_NATS_OUT=false"
    EXITCODE=1
fi

# check FEEDER_TAG exists
if /command/s6-env bash -c '[[ -n "$FEEDER_TAG" ]]'; then
    echo "FEEDER_TAG_VALID=true"
else
    echo "FEEDER_TAG_VALID=false"
    EXITCODE=1
fi

# update /run/healthcheck
if [ "$(cat /run/healthcheck)" != "$EXITCODE" ]; then
    echo "$EXITCODE" > /run/healthcheck
fi

# if container has been unhealthy for 10mins+ then stop container
if [ "$(cat /run/healthcheck)" != "0" ]; then
    if test "$(find /run/healthcheck -mmin +10)"; then
        # kill container init, which will stop container
        # as container should be started with autoremove, it should be deleted
        kill 1
    fi
fi

exit ${EXITCODE}
