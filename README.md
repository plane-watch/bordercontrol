# plane.watch Border Control

[![codecov](https://codecov.io/gh/plane-watch/pw-bordercontrol/graph/badge.svg?token=E60EZBIPZN)](https://codecov.io/gh/plane-watch/pw-bordercontrol)

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
      * [Feed-In Container Health](#feed-in-container-health)
  * [Statistics](#statistics)
    * [NATS](#nats)
      * [Per-feeder](#per-feeder)
      * [All Feeders](#all-feeders)
    * [Human Readable](#human-readable)
    * [API](#api)
    * [Prometheus Metrics](#prometheus-metrics)

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

