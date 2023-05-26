# plane.watch Border Control

How plane.watch receives data from feeders.

Designed to be horizontally scalable, sat behind TCP load balancer(s).

* [plane.watch Border Control](#planewatch-border-control)
  * [Overview](#overview)
  * [Operations](#operations)
    * [Statistics](#statistics)
      * [Human Readable](#human-readable)
      * [API](#api)
    * [Configuring the environment](#configuring-the-environment)
    * [Starting the environment](#starting-the-environment)
    * [Stopping the environment](#stopping-the-environment)
    * [Updating the environment](#updating-the-environment)
    * [Re-reading SSL certificates](#re-reading-ssl-certificates)
  * [Feed-In Container Health](#feed-in-container-health)

## Overview

* Terminates incoming stunnel'd BEAST and MLAT connections from feeders
  * bordercontrol expects the SSL SNI sent from the client to be the feeder's "API key" (a UUID)
  * SNIs in incoming connections are checked against a list of valid UUIDs pulled from ATC.
  * Connections with invalid SNIs are rejected.
* Builds and starts regional BEAST multiplexers / MLAT servers
* Builds "feed-in" container image
* When feeders connect:
  * A "feed-in" container is automatically created and started for the specific feeder, which:
    * Runs `readsb`
    * Runs `pw_ingest` which connects to `readsb` and puts the position data into NATS
    * For the old environment, forwards BEAST data into the regional mux
  * BEAST data is sent to the "feed-in" container
  * MLAT connections are proxied to `mlat-server` running on the regional mux

## Operations

### Statistics

#### Human Readable

Bordercontrol listens on TCP port `8080` for http requests, and will display a simple HTML table of feeders with statistics.

#### API

Bordercontrol supports the following API calls:

| Query Type | URL Path | Returns |
| ---------- | -------- | ------- |
| `GET` | `/api/v1/feeder/<UUID>` | Statistics for single feeder by UUID |
| `GET` | `/api/v1/feeders/` | Statistics for all feeders |

These queries return JSON.

### Configuring the environment

In the root of the repository, create a `.env` file containing the following:

| Environment Variable | Description | Example |
| -------------------- | ----------- | ------- |
| `ATC_URL` | URL to ATC API | `http://10.0.6.2:3000` |
| `ATC_USER` | Username/email for ATC API | `controltower@plane.watch` |
| `ATC_PASS` | Password for ATC API | REDACTED |
| `BC_CERT_FILE` | Path (within the bordercontrol container) of the X509 cert | `/etc/ssl/private/push.plane.watch/fullchain.pem` |
| `BC_KEY_FILE` | Path (within the bordercontrol container) of the X509 key | `/etc/ssl/private/push.plane.watch/privkey.pem` |
| `PW_INGEST_SINK` | URL passed through to `pw_ingest` in feed-in containers | `nats://nats-ingest.plane.watch:4222` |

Example:

```bash
# ATC API connection details
ATC_URL=http://10.0.6.2:3000
ATC_USER=controltower@plane.watch
ATC_PASS=REDACTED
# SSL Key/Cert
BC_CERT_FILE=/etc/ssl/private/push.plane.watch/fullchain.pem
BC_KEY_FILE=/etc/ssl/private/push.plane.watch/privkey.pem
# pw_ingest
PW_INGEST_SINK=nats://nats-ingest.plane.watch:4222
```

Create a the `docker-compose-local.yml` file (see the example), under `services:` -> `bordercontrol:`, ensure the path holding SSL certs/keys is mapped as a volume.

### Starting the environment

From the root of the repo: `git submodule add -f git@github.com:plane-watch/pw-pipeline.git` (note you may need to rmdir the pw-pipeline dir).

From the root of the repository, run `docker compose up -d --build`.

This will build the images, containers and launch bordercontrol.

### Stopping the environment

From the root of the repository, run `docker compose down`.

This will stop and remove multiplexer and bordercontrol containers. After approximately 10 minutes, the feed-in containers will stop and be removed.

### Updating the environment

From the root of the repository, run `docker compose up -d --build`.

Any updated containers will be re-created. For the feed-in containers, bordercontrol will update these one at a time to hopefully prevent any visible impact on the web UI.

### Re-reading SSL certificates

Bordercontrol will non-disruptively re-read SSL certificate(s) if it receives a SIGHUP.

The easiest way to do this is `docker exec bordercontrol pkill -HUP bordercontrol`.

## Feed-In Container Health

Healthchecks are run within the feed-in containers.

The healthcheck will check the following:

* Checks BEAST connection inbound from bordercontrol
* Checks BEAST connection outbound to mux
* Checks NATS connection outbound

If any of the above checks fail, the container is marked as unhealthy.

If the feed-in container has been unhealthy for longer than 10 minutes, the container will terminate.

As the containers are launched via bordercontrol with "autoremove" enabled, the stopped container will be removed automatically.

This ensures that only containers of active feeders are started and running.
