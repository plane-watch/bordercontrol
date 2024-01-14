# plane.watch Border Control

[![Airliner emitting visible radio waves. The radio waves are being received by a ground station. The ground station is surrounded by a border.](artwork.png)](https://hotpot.ai/art-generator)

[![codecov](https://codecov.io/gh/plane-watch/bordercontrol/graph/badge.svg?token=E60EZBIPZN)](https://codecov.io/gh/plane-watch/bordercontrol)

How plane.watch receives data from feeders.

Designed to be horizontally scalable, sat behind TCP load balancer(s).

* [plane.watch Border Control](#planewatch-border-control)
  * [Overview](#overview)
  * [Configuring the environment](#configuring-the-environment)
    * [Operation](#operation)
      * [Starting the environment](#starting-the-environment)
      * [Stopping the environment](#stopping-the-environment)
      * [Rebuilding the environment](#rebuilding-the-environment)
      * [Updating just feed-in containers](#updating-just-feed-in-containers)
      * [Re-reading SSL certificates](#re-reading-ssl-certificates)
      * [Kicking a Feeder](#kicking-a-feeder)
      * [Feed-In Container Health](#feed-in-container-health)
  * [Statistics](#statistics)
    * [NATS](#nats)
      * [Per-feeder](#per-feeder)
      * [All Feeders](#all-feeders)
    * [Human Readable](#human-readable)
    * [API](#api)
    * [Prometheus Metrics](#prometheus-metrics)
  * [NATS Detail](#nats-detail)
    * [`pw_bordercontrol.ping`](#pw_bordercontrolping)
    * [`pw_bordercontrol.stunnel.reloadcertkey`](#pw_bordercontrolstunnelreloadcertkey)
    * [`pw_bordercontrol.feedinimage.rebuild`](#pw_bordercontrolfeedinimagerebuild)
    * [`pw_bordercontrol.feeder.kick`](#pw_bordercontrolfeederkick)
    * [`pw_bordercontrol.feeders.metrics`](#pw_bordercontrolfeedersmetrics)
    * [`pw_bordercontrol.feeder.metrics`](#pw_bordercontrolfeedermetrics)
    * [`pw_bordercontrol.feeder.metrics.beast`](#pw_bordercontrolfeedermetricsbeast)
    * [`pw_bordercontrol.feeder.metrics.mlat`](#pw_bordercontrolfeedermetricsmlat)
    * [`pw_bordercontrol.feeder.connected.beast`](#pw_bordercontrolfeederconnectedbeast)
    * [`pw_bordercontrol.feeder.connected.mlat`](#pw_bordercontrolfeederconnectedmlat)

## Overview

* Terminates incoming stunnel'd BEAST and MLAT connections from feeders
  * bordercontrol expects the SSL SNI sent from the client to be the feeder's "API key" (a UUID)
  * SNIs in incoming connections are checked against a list of valid UUIDs pulled from ATC.
  * Connections with invalid SNIs are rejected.
* Builds and starts regional MLAT servers (muxes)
* Builds and manages "feed-in" container image
* When feeders connect:
  * A "feed-in" container is automatically created and started for the specific feeder, which:
    * Runs `pw_ingest` which receives and decodes `BEAST` and puts the position data into NATS
  * BEAST data is sent to the "feed-in" container
  * MLAT connections are proxied to `mlat-server` instances

## Configuring the environment

In the root of the repository, create a `.env` file containing the following:

| Environment Variable        | CLI Flag Equiv.            | R/O | Description                                                                    | Example                                           |
|-----------------------------|----------------------------|-----|--------------------------------------------------------------------------------|---------------------------------------------------|
| `ATC_UPDATE_FREQ`           | `--atcupdatefreq`          | O   | Frequency (in minutes) for valid feeder updates from ATC                       | 1                                                 |
| `ATC_URL`                   | `--atcurl`                 | R   | URL to ATC API                                                                 | `http://10.0.6.2:3000`                            |
| `ATC_USER`                  | `--atcuser`                | R   | Username/email for ATC API                                                     | `controltower@plane.watch`                        |
| `ATC_PASS`                  | `--atcpass`                | R   | Password for ATC API                                                           | REDACTED                                          |
| `BC_CERT_FILE`              | `--cert`                   | O   | Path (within the bordercontrol container) of the X509 cert                     | `/etc/ssl/private/push.plane.watch/fullchain.pem` |
| `BC_KEY_FILE`               | `--key`                    | O   | Path (within the bordercontrol container) of the X509 key                      | `/etc/ssl/private/push.plane.watch/privkey.pem`   |
| `BC_LISTEN_API`             | `--listenapi`              | O   | Address and TCP port server will listen on for API, stats & Prometheus metrics | `:8080`                                           |
| `BC_LISTEN_BEAST`           | `--listenbeast`            | O   | Address and TCP port to listen on for BEAST connections                        | `0.0.0.0:12345`                                   |
| `BC_LISTEN_MLAT`            | `--listenmlat`             | O   | Address and TCP port to listen on for MLAT connections                         | `0.0.0.0:12346`                                   |
| `FEED_IN_CONTAINER_PREFIX`  | `--feedincontainerprefix`  | O   | Feed-in container prefix                                                       | `feed-in-`                                        |
| `FEED_IN_CONTAINER_NETWORK` | `--feedincontainernetwork` | O   | Feed-in container network                                                      | `bordercontrol_feeder`                            |
| `FEED_IN_IMAGE`             | `--feedinimage`            | O   | Feed-in image name                                                             | `feed-in`                                         |
| `PW_INGEST_SINK`            | `--pwingestpublish`        | R   | URL passed through to `pw_ingest` in feed-in containers                        | `nats://nats-ingest.plane.watch:4222`             |
| `NATS`                      | `--natsurl`                | O   | NATS URL for stats/control                                                     | `nats://nats-ingest.plane.watch:4222`             |
| `NATS_INSTANCE`             | `--natsinstance`           | O   | NATS instance identifier for this instance of Bordercontrol                    | `prod-bordercontrol-01`                           |

Note: *O = **O**ptional, R = **R**equired*.

Example:

```bash
# ATC API connection details
ATC_URL=http://atc.yourdomain.tld:3000
ATC_USER=REDACTED
ATC_PASS=REDACTED
# SSL Key/Cert
BC_CERT_FILE=/path/to/fullchain.pem
BC_KEY_FILE=/path/to/privkey.pem
# pw_ingest
PW_INGEST_SINK=nats://nats-ingest.youdomain.tld:4222
NATS=nats://nats-ingest.youdomain.tld:4222
```

Create a the `docker-compose-local.yml` file (see the example), under `services:` -> `bordercontrol:`, ensure the path holding SSL certs/keys is mapped as a volume.

### Operation

#### Starting the environment

From the root of the repository, run `docker compose up -d --build`.

This will build the images, containers and launch bordercontrol.

#### Stopping the environment

From the root of the repository, run `docker compose down`.

This will stop and remove multiplexer and bordercontrol containers. After approximately 10 minutes, the feed-in containers will stop and be removed.

#### Rebuilding the environment

From the root of the repository, run `docker compose build --pull && docker compose up -d`.

Any updated containers will be rebuilt and recreated.

#### Updating just feed-in containers

This can be done via rebuilding the `feed-in-builder` container, or a NATS request to `pw_bordercontrol.stunnel.reloadcertkey` with the body containing the NATS instance or wildcard (`*`) for all instances.

* From the root of the repository, run: `docker compose build --pull feed-in-builder`.
* via NATS: `nats req --timeout=2m pw_bordercontrol.feedinimage.rebuild "*"`

For the feed-in containers, bordercontrol checks these every 5 minutes. Containers are updated one at a time. After updating a container, bordercontrol waits 30 seconds, to hopefully prevent any visible impact on the web UI. These timeouts/sleeps can be skipped by sending a `SIGUSR1` signal to bordercontrol. This is shown in the logs:

```
Mon Nov  6 20:51:43 UTC 2023 INF caught signal, proceeding immediately goroutine=checkFeederContainers signal="user defined signal 1"
Mon Nov  6 20:51:43 UTC 2023 INF out of date container being killed for recreation container=feed-in-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx goroutine=checkFeederContainers
```

#### Re-reading SSL certificates

Bordercontrol will non-disruptively re-read SSL certificate(s) if it receives a `SIGHUP`, or a NATS request to `pw_bordercontrol.stunnel.reloadcertkey` with the body containing the NATS instance or wildcard (`*`) for all instances.

* via `SIGHUP`: `docker exec bordercontrol pkill -HUP bordercontrol`
* via NATS: `nats req pw_bordercontrol.stunnel.reloadcertkey "*"`

#### Kicking a Feeder

If the need arises to kick a feeder, this can be done via NATS:

| Subject                        | Header    | Body           | Description                                                           |
|--------------------------------|-----------|----------------|-----------------------------------------------------------------------|
| `pw_bordercontrol.feeder.kick` | *Ignored* | Feeder API Key | Immediately removes the feed-in container associated with the feeder. |

Example:

```
# nats req pw_bordercontrol.feeder.kick <Feeder API Key>
13:33:35 Sending request on "pw_bordercontrol.feeder.kick"
13:33:35 Received with rtt 254.043895ms
+ACK
```

#### Feed-In Container Health

Healthchecks are run within the feed-in containers.

The healthcheck will check the following:

* Checks BEAST connection inbound from bordercontrol
* Checks NATS connection outbound

If any of the above checks fail, the container is marked as unhealthy.

If the feed-in container has been unhealthy for longer than 10 minutes, the container will terminate.

As the containers are launched via bordercontrol with "autoremove" enabled, the stopped container will be removed automatically.

This ensures that only containers of active feeders are started and running.

## Statistics

### NATS

Bordercontrol will report statistics via NATS.

On all replies, the bordercontrol instance is returned as a header, so the instance the feeder is connected to can be identified if needed.

#### Per-feeder

| Subject                                   | Header    | Body           | Description                                                                                                                                                                                                                                                                                                                                                   |
|-------------------------------------------|-----------|----------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `pw_bordercontrol.feeder.connected.beast` | *Ignored* | Feeder API Key | Returns `true` if feeder matching API Key has a BEAST protocol connection.                                                                                                                                                                                                                                                                                    |
| `pw_bordercontrol.feeder.connected.mlat`  | *Ignored* | Feeder API Key | Returns `true` if feeder matching API Key has a MLAT protocol connection.                                                                                                                                                                                                                                                                                     |
| `pw_bordercontrol.feeder.metrics`         | *Ignored* | Feeder API Key | Returns JSON containing metrics for all connections. Eg: `{"feeder_code":"YPPH-0003","label":"Bayswater2","beast_connected":true,"beast_bytes_in":10120,"beast_bytes_out":0,"beast_connection_time":"2024-01-04T15:14:03.892056108Z","mlat_connected":true,"mlat_bytes_in":140,"mlat_bytes_out":458,"mlat_connection_time":"2024-01-04T15:13:44.397506876Z"}` |
| `pw_bordercontrol.feeder.metrics.beast`   | *Ignored* | Feeder API Key | Returns JSON containing metrics for BEAST connection. Eg: `{"bytes_in":305177,"bytes_out":0,"connection_time":"2024-01-04T14:37:47.87329718Z"}`                                                                                                                                                                                                               |
| `pw_bordercontrol.feeder.metrics.mlat`    | *Ignored* | Feeder API Key | Returns JSON containing metrics for MLAT connection. Eg: `{"bytes_in":29172,"bytes_out":744,"connection_time":"2024-01-04T14:37:27.133693249Z"}`                                                                                                                                                                                                              |

If the requested feeder is not connected to bordercontrol, bordercontrol will silently ignore the message. This ensures that in scale-out environments, you can simply use the first (and hopefully only) reply. It is suggested to use a short timeout too (100ms should be *plenty*).

#### All Feeders

| Subject                            | Header    | Body      | Description                                                                                                                                                                                                                                                                                                                                                                                            |
|------------------------------------|-----------|-----------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `pw_bordercontrol.feeders.metrics` | *Ignored* | *Ignored* | Returns JSON containing metrics for all feeders, all connections. Eg: `{"<feeder api key redacted>":{"feeder_code":"YPPH-0003","label":"Bayswater2","beast_connected":true,"beast_bytes_in":258,"beast_bytes_out":0,"beast_connection_time":"2024-01-04T15:28:28.697372348Z","mlat_connected":true,"mlat_bytes_in":140,"mlat_bytes_out":458,"mlat_connection_time":"2024-01-04T15:28:10.295874658Z"}}` |

### Human Readable

Bordercontrol will display a simple HTML table of feeders with statistics at `http://dockerhost:8080`

### API

Bordercontrol supports the following API calls to `http://dockerhost:8080`:

| Query Type | URL Path                | Returns                              |
|------------|-------------------------|--------------------------------------|
| `GET`      | `/api/v1/feeder/<UUID>` | Statistics for single feeder by UUID |
| `GET`      | `/api/v1/feeders/`      | Statistics for all feeders           |

These queries return a JSON object with keys `Data` and `Error`. If `Error == ""`, then the `Data` key should contain the requested data. If `Error != ""`, then the error details are contained within `Error` and any data in `Data` should be discarded.

### Prometheus Metrics

Prometheus metrics are published at `http://dockerhost:8080/metrics`.

## NATS Detail

### `pw_bordercontrol.ping`

Type: request

| Subject                 | Header    | Body      | Description                                                              |
|-------------------------|-----------|-----------|--------------------------------------------------------------------------|
| `pw_bordercontrol.ping` | *Ignored* | *Ignored* | Each instance of bordercontrol will reply with basic status information. |

Bordercontrol will respond with:

* Header `instance` with the instance identifier defined by `--natsinstance` / `NATS_INSTANCE`
* Header `uptime` with the time this instance has been running
* Header `version` with the version of this instance
* Body of `pong`

When submitting this request, set `replies=0` so the NATS client listens until timeout is reached. This will ensure responses are received from all running instances.

Example:

```text
# nats req --replies=0 pw_bordercontrol.ping -
01:09:41 Sending request on "pw_bordercontrol.ping"
01:09:41 Received with rtt 1.324181ms
01:09:41 instance: 29146a332fdf
01:09:41 uptime: 10h42m40.528380969s
01:09:41 version: 0.0.1 (39376d7), 2024-01-05T14:25:14Z

pong


```

### `pw_bordercontrol.stunnel.reloadcertkey`

Type: request

| Subject                                  | Header    | Body                                                        | Description                                 |
|------------------------------------------|-----------|-------------------------------------------------------------|---------------------------------------------|
| `pw_bordercontrol.stunnel.reloadcertkey` | *Ignored* | Bordercontrol instance or wildcard (`*`) for all instances. | Bordercontrol will reload SSL/TLS cert/key. |

Bordercontrol will acknowledge the message when certs/keys are reloaded.

When submitting this request using a wildcard, set `replies=0` so the NATS client listens until timeout is reached. This will ensure responses are received from all running instances.

Example with wildcard:

```text
# nats req --replies=0 pw_bordercontrol.stunnel.reloadcertkey "*"
01:24:13 Sending request on "pw_bordercontrol.stunnel.reloadcertkey"
01:24:13 Received with rtt 1.586432ms
+ACK

```

Example specifying instance:

```text
# nats req pw_bordercontrol.stunnel.reloadcertkey 29146a332fdf
01:27:04 Sending request on "pw_bordercontrol.stunnel.reloadcertkey"
01:27:04 Received with rtt 1.997614ms
+ACK

```

### `pw_bordercontrol.feedinimage.rebuild`

Type: request

| Subject                                | Header    | Body                                                        | Description                                   |
|----------------------------------------|-----------|-------------------------------------------------------------|-----------------------------------------------|
| `pw_bordercontrol.feedinimage.rebuild` | *Ignored* | Bordercontrol instance or wildcard (`*`) for all instances. | Bordercontrol will rebuild the feed-in image. |

Bordercontrol reply with the outcome of the build. More detailed build logs are logged to the bordercontrol log.

When submitting this request:

* If using a wildcard, set `replies=0` so the NATS client listens until timeout is reached. This will ensure responses are received from all running instances.
* Set `timeout` to an appropriate value to enable the build to complete before the timeout is reached.

Example:

```text
# nats req --timeout=2m pw_bordercontrol.feedinimage.rebuild 29146a332fdf
01:34:06 Sending request on "pw_bordercontrol.feedinimage.rebuild"
01:34:42 Received with rtt 35.981288461s
01:34:42 instance: 29146a332fdf
01:34:42 result: ok

{"stream":"Successfully tagged feed-in:latest\n"}

```

Relevant logs from bordercontrol:

```text
Sat Jan  6 01:37:18 UTC 2024 DBG build output stream="Step 1/15 : FROM ghcr.io/plane-watch/pw-pipeline:pw_ingest as pipeline"
Sat Jan  6 01:37:19 UTC 2024 DBG build output stream="Step 2/15 : FROM debian:bookworm-20231218-slim"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 3/15 : ARG S6_OVERLAY_VERSION=3.1.6.0"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 4/15 : SHELL [\"/bin/bash\", \"-o\", \"pipefail\", \"-c\"]"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 5/15 : RUN set -x &&     TEMP_PACKAGES=() &&     KEPT_PACKAGES=() &&     TEMP_PACKAGES+=(curl) &&     TEMP_PACKAGES+=(git) &&     TEMP_PACKAGES+=(file) &&     KEPT_PACKAGES+=(ca-certificates) &&     KEPT_PACKAGES+=(procps) &&     KEPT_PACKAGES+=(iproute2) &&     KEPT_PACKAGES+=(xz-utils) &&     apt-get update &&     apt-get install -y --no-install-recommends         \"${KEPT_PACKAGES[@]}\"         \"${TEMP_PACKAGES[@]}\"         &&     git clone       --depth=1       https://github.com/mikenye/docker-healthchecks-framework.git       /opt/healthchecks-framework       &&     rm -rf       /opt/healthchecks-framework/.git*       /opt/healthchecks-framework/*.md       /opt/healthchecks-framework/tests       &&     apt-get remove -y \"${TEMP_PACKAGES[@]}\" &&     apt-get autoremove -y &&     rm -rf /src/* /tmp/* /var/lib/apt/lists/*"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 6/15 : COPY --from=pipeline /app/pw_ingest /usr/local/bin/pw_ingest"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 7/15 : COPY rootfs/ /"
Sat Jan  6 01:37:22 UTC 2024 DBG build output stream="Step 8/15 : ADD https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz /tmp"
Sat Jan  6 01:37:23 UTC 2024 DBG build output stream="Step 9/15 : RUN tar -C / -Jxpf /tmp/s6-overlay-noarch.tar.xz"
Sat Jan  6 01:37:23 UTC 2024 DBG build output stream="Step 10/15 : ADD https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-x86_64.tar.xz /tmp"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Step 11/15 : RUN tar -C / -Jxpf /tmp/s6-overlay-x86_64.tar.xz"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Step 12/15 : ENV S6_LOGGING=0     S6_VERBOSITY=1"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Step 13/15 : ENV PW_INGEST_INPUT_MODE=listen     PW_INGEST_INPUT_PROTO=beast     PW_INGEST_INPUT_ADDR=0.0.0.0     PW_INGEST_INPUT_PORT=12345"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Step 14/15 : ENTRYPOINT [ \"/init\" ]"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Step 15/15 : HEALTHCHECK --interval=300s --timeout=40s --start-period=600s --retries=5 CMD /scripts/healthcheck.sh"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Successfully built 8e3321e421fc"
Sat Jan  6 01:37:25 UTC 2024 DBG build output stream="Successfully tagged feed-in:latest"
```

### `pw_bordercontrol.feeder.kick`

Type: request

| Subject                        | Header    | Body              | Description                                                                    |
|--------------------------------|-----------|-------------------|--------------------------------------------------------------------------------|
| `pw_bordercontrol.feeder.kick` | *Ignored* | API Key of feeder | Bordercontrol will immediately kill and remove the feeder's feed-in container. |

Bordercontrol will acknowledge when the feeder is kicked.

Only the instance(s) with connections from the specified feeder will respond.

Example:

```text
# nats req pw_bordercontrol.feeder.kick <API Key Redacted>
01:42:24 Sending request on "pw_bordercontrol.feeder.kick"
01:42:25 Received with rtt 263.201835ms
+ACK

```

Relevant logs from bordercontrol:

```
Sat Jan  6 01:42:24 UTC 2024 INF killing feed-in container container=feed-in-<API Key Redacted>
Sat Jan  6 01:42:24 UTC 2024 ERR error writing to server error="write tcp 172.18.0.13:42626->172.18.0.15:12345: write: broken pipe" code=YPPH-0003 connNum=5 connections=1/1 dst=feed-in-<API Key Redacted> func=["feeder_conn.go","proxyClientConnection"] label=Bayswater2 mux=mux-wa port=12345 proto=BEAST proxy=ClientToServer src=<IP redacted> uuid=<API Key Redacted>
```

### `pw_bordercontrol.feeders.metrics`

Type: request

| Subject                            | Header    | Body      | Description                                                  |
|------------------------------------|-----------|-----------|--------------------------------------------------------------|
| `pw_bordercontrol.feeders.metrics` | *Ignored* | *Ignored* | Bordercontrol will send statistics of all connected feeders. |

Bordercontrol reply with all connected feeder metrics in JSON format.

When submitting this request:

* It is recommended to use `replies=0` so the NATS client listens until timeout is reached. This will ensure responses are received from all running instances.

Example:

```text
# nats req --replies=0 pw_bordercontrol.feeders.metrics -
09:02:31 Sending request on "pw_bordercontrol.feeders.metrics"
09:02:31 Received with rtt 957.873µs
09:02:31 instance: 29146a332fdf

{"<API Key Redacted>":{"feeder_code":"YPPH-0003","label":"Bayswater2","beast_connected":true,"beast_bytes_in":40845491,"beast_bytes_out":0,"beast_connection_time":"2024-01-06T01:42:30.572867107Z","mlat_connected":true,"mlat_bytes_in":7489647,"mlat_bytes_out":53888,"mlat_connection_time":"2024-01-05T14:27:08.508839791Z"}}

```

### `pw_bordercontrol.feeder.metrics`

Type: request

| Subject                           | Header    | Body              | Description                                              |
|-----------------------------------|-----------|-------------------|----------------------------------------------------------|
| `pw_bordercontrol.feeder.metrics` | *Ignored* | API Key of feeder | Bordercontrol will send statistics of individual feeder. |

The instance of bordercontrol with the connected feeder reply with the feeder's metrics in JSON format. If feeder is not connected, no response will be received.

Example:

```text
# nats req pw_bordercontrol.feeder.metrics <API Key Redacted>
09:06:36 Sending request on "pw_bordercontrol.feeder.metrics"
09:06:36 Received with rtt 1.011305ms
09:06:36 instance: 29146a332fdf

{"feeder_code":"YPPH-0003","label":"Bayswater2","beast_connected":true,"beast_bytes_in":41143080,"beast_bytes_out":0,"beast_connection_time":"2024-01-06T01:42:30.572867107Z","mlat_connected":true,"mlat_bytes_in":7524410,"mlat_bytes_out":54133,"mlat_connection_time":"2024-01-05T14:27:08.508839791Z"}

```

### `pw_bordercontrol.feeder.metrics.beast`

Type: request

| Subject                                 | Header    | Body              | Description                                                             |
|-----------------------------------------|-----------|-------------------|-------------------------------------------------------------------------|
| `pw_bordercontrol.feeder.metrics.beast` | *Ignored* | API Key of feeder | Bordercontrol will send BEAST protocol statistics of individual feeder. |

The instance of bordercontrol with the connected feeder reply with the feeder's BEAST metrics in JSON format. If feeder is not connected, no response will be received.

Example:

```text
# nats req pw_bordercontrol.feeder.metrics.beast <API Key Redacted>
09:18:18 Sending request on "pw_bordercontrol.feeder.metrics.beast"
09:18:18 Received with rtt 1.307498ms
09:18:18 instance: 29146a332fdf

{"feeder_code":"YPPH-0003","label":"Bayswater2","bytes_in":42261164,"bytes_out":0,"connection_time":"2024-01-06T01:42:30.572867107Z"}

```

### `pw_bordercontrol.feeder.metrics.mlat`

Type: request

| Subject                                | Header    | Body              | Description                                                            |
|----------------------------------------|-----------|-------------------|------------------------------------------------------------------------|
| `pw_bordercontrol.feeder.metrics.mlat` | *Ignored* | API Key of feeder | Bordercontrol will send MLAT protocol statistics of individual feeder. |

The instance of bordercontrol with the connected feeder reply with the feeder's MLAT metrics in JSON format. If feeder is not connected, no response will be received.

Example:

```text
# nats req pw_bordercontrol.feeder.metrics.mlat <API Key Redacted>
09:14:48 Sending request on "pw_bordercontrol.feeder.metrics.mlat"
09:14:48 Received with rtt 3.270292ms
09:14:48 instance: 29146a332fdf

{"feeder_code":"YPPH-0003","label":"Bayswater2","bytes_in":7608127,"bytes_out":54571,"connection_time":"2024-01-05T14:27:08.508839791Z"}

```

### `pw_bordercontrol.feeder.connected.beast`

Type: request

| Subject                                   | Header    | Body              | Description                                                        |
|-------------------------------------------|-----------|-------------------|--------------------------------------------------------------------|
| `pw_bordercontrol.feeder.connected.beast` | *Ignored* | API Key of feeder | Bordercontrol will return `true` if feeder has a BEAST connection. |

The instance of bordercontrol with a BEAST connection for the specified feeder will return `true`. If feeder has no BEAST connection, no response will be received.

Example:

```text
# nats req pw_bordercontrol.feeder.connected.beast <API Key Redacted>
09:21:45 Sending request on "pw_bordercontrol.feeder.connected.beast"
09:21:45 Received with rtt 965.63µs
09:21:45 instance: 29146a332fdf

true

```

### `pw_bordercontrol.feeder.connected.mlat`

Type: request

| Subject                                  | Header    | Body              | Description                                                       |
|------------------------------------------|-----------|-------------------|-------------------------------------------------------------------|
| `pw_bordercontrol.feeder.connected.mlat` | *Ignored* | API Key of feeder | Bordercontrol will return `true` if feeder has a MLAT connection. |

The instance of bordercontrol with a MLAT connection for the specified feeder will return `true`. If feeder has no MLAT connection, no response will be received.

Example:

```text
# nats req pw_bordercontrol.feeder.connected.mlat <API Key Redacted>
09:24:32 Sending request on "pw_bordercontrol.feeder.connected.mlat"
09:24:32 Received with rtt 1.698948ms
09:24:32 instance: 29146a332fdf

true

```

