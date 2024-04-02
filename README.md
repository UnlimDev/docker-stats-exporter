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

### Filter monitored containers

Available since: v0.1.0

The environment variable `DOCKER_STATS_FILTER_LABELS` allows filter containers with specific [labels](https://docs.docker.com/reference/cli/docker/container/run/#label) to monitor.

For example, container executed with additional label `com.mydomain.service=web`:

```shell
docker run --label "com.mydomain.service=web" nginx:latest
```

To monitor containers with the label `com.mydomain.service=web` only, pass the additional configuration to exporter:

```shell
docker run \
        -e "DOCKER_STATS_FILTER_LABELS=com.mydomain.service=web" \
        -v "/var/run/docker.sock:/var/run/docker.sock" \
        unlimdev/docker-stats-exporter
```

Additionally, it's possible to omit the value of label to monitor containers with any value of the same label:

```shell
docker run \
        -e "DOCKER_STATS_FILTER_LABELS=com.mydomain.service" \
        -v "/var/run/docker.sock:/var/run/docker.sock" \
        unlimdev/docker-stats-exporter
```

## Security Considerations

Exposing any monitoring metrics with no proper protection was never been a good idea.
Therefore, consider using private network interfaces to [expose](https://docs.docker.com/network/#published-ports) metrics port, e.g.:

```shell
docker run \
        -p 10.0.0.10:9099:9099 \
        -v "/var/run/docker.sock:/var/run/docker.sock" \
        unlimdev/docker-stats-exporter
```

