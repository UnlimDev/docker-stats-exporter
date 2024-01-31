# Docker Stats Prometheus Exporter

Exports the basic metrics for *running* containers.

Based on [Docker API](https://docs.docker.com/engine/api/v1.43/#tag/Container/operation/ContainerList)

Metrics data equivalent to `docker stats` output.


## Running Exporter

```shell
docker run \
        -p 9099:9099 \
        -v "/var/run/docker.sock:/var/run/docker.sock" \
        unlimdev/docker-stats-exporter
```

Inspect the results at http://localhost:9099/metrics

## Configuration

No configuration currently supported.
