FROM golang:1.22.5-alpine as BUILDER

WORKDIR /app-build

COPY ./ ./

RUN go build


FROM scratch

LABEL authors="Dexter Morganov"
WORKDIR /app

COPY --from=BUILDER /app-build/docker-stats-exporter ./

VOLUME /var/run/docker.sock

EXPOSE "9099"

ENTRYPOINT ["/app/docker-stats-exporter"]

