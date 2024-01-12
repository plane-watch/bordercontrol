# feed-in

a.k.a "feed-in" containers.

Spawns an instance of `pw_ingest` configured as-per below.

## Environment Variables

| Variable | Detail | Default |
| -------- | ------ | ------- |
| `FEEDER_LAT` | (Optional) Feeder latitude | *unset* |
| `FEEDER_LON` | (Optional) Feeder longitude | *unset* |
| `FEEDER_TAG` | (Required) Feeder tag | *unset* |
| `PW_INGEST_INPUT_MODE` | Must be set to either `listen` or `fetch` | `listen` |
| `PW_INGEST_INPUT_PROTO` | Must be either `avr`, `beast` or `sbs1` | `beast` |
| `PW_INGEST_INPUT_ADDR`  | The IP address to listen on / fetch from | `0.0.0.0` |
| `PW_INGEST_INPUT_PORT`  | The TCP port to listen on / fetch from | `12345` |
| `PW_INGEST_SINK` | (Required) The place to send decoded JSON in URL Form, eg `nats://user:pass@host:port/vhost?ttl=60` | *unset* |
| `PW_INGEST_ENABLE_ADSC` | (Optional) Set to `true` to enable ADS-C | `false` |
