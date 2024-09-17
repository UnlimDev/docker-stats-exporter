package main

import (
    "context"
    "fmt"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/filters"
    "github.com/docker/docker/client"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "log"
    "net/http"
    "os"
    "os/signal"
    "regexp"
    "strings"
    "syscall"
    "time"
)

const (
    defaultHttpPort = 9099

    RefreshContainersListInterval = 2 * time.Second // TODO: make it configurable
    RefreshContainersTickInterval = 100 * time.Millisecond
)

const (
    metricNameSpace    = "docker_stats"
    metricSubContainer = "container"
)

var httpServer *http.Server
var statsThreads *ThreadList

var labelRegex = regexp.MustCompile("[\\W-]")
var scrapeLabels []string

var registry *prometheus.Registry
var containersCount *prometheus.GaugeVec

var memUsageVec *prometheus.GaugeVec
var memLimitVec *prometheus.GaugeVec

var cpuCountVec *prometheus.GaugeVec
var cpuUsageTotalVec *prometheus.GaugeVec
var cpuUsageKernelVec *prometheus.GaugeVec
var cpuUsageUserVec *prometheus.GaugeVec
var cpuUsageSystem *prometheus.GaugeVec
var cpuPercentage *prometheus.GaugeVec

var netRxBytesVec *prometheus.GaugeVec
var netRxPacketsVec *prometheus.GaugeVec
var netRxErrorVec *prometheus.GaugeVec
var netRxDropVec *prometheus.GaugeVec

var netTxBytesVec *prometheus.GaugeVec
var netTxPacketsVec *prometheus.GaugeVec
var netTxErrorVec *prometheus.GaugeVec
var netTxDropVec *prometheus.GaugeVec

// Docker API Client
var cli *client.Client

func getLabels(normalize bool) []string {
    labels := strings.Split(strings.TrimSpace(os.Getenv("DOCKER_STATS_LABELS_SCRAPE")), ",")

    var res []string
    for _, lbl := range labels {
        if lbl == "" {
            continue
        }

        if normalize {
            res = append(res, labelRegex.ReplaceAllLiteralString(lbl, "_"))
        } else {
            res = append(res, lbl)
        }
    }

    // TODO: optionally exclude ID from list
    res = append([]string{"id", "name"}, res...)

    return res
}

func getContainerVector(name string, description string, labels []string) *prometheus.GaugeVec {
    return prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: metricNameSpace,
            Subsystem: metricSubContainer,
            Name:      name,
            Help:      description,
        },
        labels,
    )
}

func main() {
    chStop := make(chan os.Signal, 1)
    signal.Notify(chStop, os.Interrupt, os.Kill, syscall.SIGTERM)

    // Scrape Handler
    registry = prometheus.NewRegistry()
    handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
    http.Handle("/metrics", handler)

    http.Handle("/", http.HandlerFunc(showIndexPage))

    httpServer = &http.Server{
        Addr:    fmt.Sprintf(":%d", defaultHttpPort),
        Handler: nil,
    }

    go func(srv *http.Server) {
        log.Println("Start scrape server on port:", defaultHttpPort)
        if sErr := srv.ListenAndServe(); sErr != nil {
            log.Fatal("Can not start http server:", sErr)
        }
    }(httpServer)

    // Init master docker API client
    if c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err != nil {
        panic(err)
    } else {
        cli = c
        log.Println("[INFO] Docker Client version:", cli.ClientVersion())

        if version, er := cli.ServerVersion(context.Background()); er != nil {
            log.Println("Error getting server version:", er)
        } else {
            log.Println("[INFO] Docker Server Version:", version.Version, "(", version.APIVersion, ")")
        }
    }

    statsThreads = new(ThreadList)
    scrapeLabels = getLabels(false)
    initMetrics()

    var updTime time.Time

    // Process container filters
    containersFilter := filters.NewArgs()

    for _, label := range strings.Split(os.Getenv("DOCKER_STATS_FILTER_LABELS"), " ") {
        if label == "" {
            continue
        }
        log.Println("Filter containers by label:", label)
        containersFilter.Add("label", label)
    }

    for {
        select {
        case <-chStop:
            stopProgram()
            return
        default:
        }

        if time.Since(updTime) <= RefreshContainersListInterval {
            time.Sleep(RefreshContainersTickInterval)
            continue
        }
        updTime = time.Now()

        containerList, err := cli.ContainerList(context.Background(), container.ListOptions{
            All:     false,
            Filters: containersFilter,
        })
        if err != nil {
            panic(fmt.Sprintf("Error getting container list: %s", err))
        }

        containersCount.With(prometheus.Labels{}).Set(float64(len(containerList)))

        for _, cont := range containerList {
            if statsThreads.Exists(cont.ID) {
                continue
            }

            mon := new(TContainerMonitor)
            mon.Id = cont.ID
            mon.OnStatRead = containerStatisticRead
            mon.OnRemove = containerStopped

            if e := mon.Exec(); e != nil {
                log.Println("Error executing container monitor:", e)
                continue
            }
            if e := statsThreads.Put(cont.ID, mon); e != nil {
                log.Println("Error adding thread to list: ", e)
            }
            log.Println("Start monitoring for container:", cont.ID[0:12])
        }
        // Stop monitoring removed containers
        for _, key := range statsThreads.GetKeys() {
            present := false
            for _, cont := range containerList {
                if cont.ID == key {
                    present = true
                    break
                }
            }
            if !present {
                if th, found := statsThreads.Get(key); found {
                    if er := th.Stop(); er != nil {
                        log.Println("Error stopping container monitor:", er)
                    }
                }
            }
        }
    }
}

func stopProgram() {
    statsThreads.StopAll()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := httpServer.Shutdown(ctx); err != nil {
        log.Fatal("Can not gracefully stop metrics server:", err)
    }

    return
}

func initMetrics() {
    labels := getLabels(true)

    containersCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: metricNameSpace,
            Subsystem: metricSubContainer,
            Name:      "count",
            Help:      "Count of running containers",
        },
        []string{},
    )
    registry.MustRegister(containersCount)

    memUsageVec = getContainerVector("memory_usage", "Actual value of memory usage by container", labels)
    registry.MustRegister(memUsageVec)

    memLimitVec = getContainerVector("memory_limit", "The limit of memory container can use", labels)
    registry.MustRegister(memLimitVec)

    cpuUsageSystem = getContainerVector("cpu_system", "CPU System Usage", labels)
    registry.MustRegister(cpuUsageSystem)

    cpuUsageTotalVec = getContainerVector("cpu_total", "CPU Usage Total", labels)
    registry.MustRegister(cpuUsageTotalVec)

    cpuUsageKernelVec = getContainerVector("cpu_kernel", "CPU Usage in Kernel Mode", labels)
    registry.MustRegister(cpuUsageKernelVec)

    cpuUsageUserVec = getContainerVector("cpu_user", "CPU Usage in User Mode", labels)
    registry.MustRegister(cpuUsageUserVec)

    cpuPercentage = getContainerVector("cpu_pcnt", "CPU Usage percentage", labels)
    registry.MustRegister(cpuPercentage)

    netLabels := append(labels, "iface")

    netRxBytesVec = getContainerVector("rx_bytes", "Received Bytes", netLabels)
    netRxPacketsVec = getContainerVector("rx_packets", "Received Packets", netLabels)
    netRxErrorVec = getContainerVector("rx_error", "Received Errors", netLabels)
    netRxDropVec = getContainerVector("rx_drop", "Incoming packets dropped", netLabels)
    registry.MustRegister(netRxBytesVec, netRxPacketsVec, netRxErrorVec, netRxDropVec)

    netTxBytesVec = getContainerVector("tx_bytes", "Transmitted Bytes", netLabels)
    netTxPacketsVec = getContainerVector("tx_packets", "Transmitted Packets", netLabels)
    netTxErrorVec = getContainerVector("tx_error", "Transmission Errors", netLabels)
    netTxDropVec = getContainerVector("tx_drop", "Outgoing packets dropped", netLabels)
    registry.MustRegister(netTxBytesVec, netTxPacketsVec, netTxErrorVec, netTxDropVec)
}

func containerStatisticRead(stat *TContainerStatistic) {
    labels := make(map[string]string)

    for _, labelName := range scrapeLabels {
        if labelName == "id" {
            labels["id"] = stat.Id[0:12]
            continue
        }
        if labelName == "name" {
            labels["name"] = strings.Replace(stat.Name, "/", "", 1) // remove leading slash
            continue
        }

        promLabel := labelRegex.ReplaceAllLiteralString(labelName, "_")

        if _, ok := stat.Labels[labelName]; ok {
            labels[promLabel] = stat.Labels[labelName]
        } else {
            labels[promLabel] = ""
        }
    }

    memUsageVec.With(labels).Set(float64(stat.MemoryStats.Usage))
    memLimitVec.With(labels).Set(float64(stat.MemoryStats.Limit))

    // TODO: Add available processors

    cpuUsageSystem.With(labels).Set(float64(stat.CPUStats.SystemUsage))

    cpuUsageTotalVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.TotalUsage))
    cpuUsageKernelVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.UsageInKernelmode))
    cpuUsageUserVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.UsageInUsermode))

    cpuPercentage.With(labels).Set(calculateCPUPercentUnix(stat))

    for iface, netStat := range stat.Networks {
        // Add extra label with interface name
        labels["iface"] = iface

        netRxBytesVec.With(labels).Set(float64(netStat.RxBytes))
        netRxPacketsVec.With(labels).Set(float64(netStat.RxPackets))
        netRxErrorVec.With(labels).Set(float64(netStat.RxErrors))
        netRxDropVec.With(labels).Set(float64(netStat.RxDropped))

        netTxBytesVec.With(labels).Set(float64(netStat.TxBytes))
        netTxPacketsVec.With(labels).Set(float64(netStat.TxPackets))
        netTxErrorVec.With(labels).Set(float64(netStat.TxErrors))
        netTxDropVec.With(labels).Set(float64(netStat.TxDropped))

        // TODO: Add rx/tx bps/pps
    }
}

func containerStopped(containerId string) {
    log.Println("Stop container monitoring:", containerId[0:12])

    thread, found := statsThreads.Get(containerId)
    if !found {
        log.Println("Container with ID is not monitored:", containerId)
        return
    }
    // Stop and remove container monitor
    if er := thread.Stop(); er != nil {
        log.Println("Error stopping container monitor:", containerId, er)
    }
    statsThreads.Del(containerId)

    // Clear container metrics
    name := thread.GetOpt("name")
    labels := prometheus.Labels{
        "id":   containerId[0:12],
        "name": strings.Replace(name.Value.(string), "/", "", 1),
    }

    deleteLabeledMetric(labels,
        memUsageVec,
        memLimitVec,
        cpuUsageSystem,
        cpuUsageKernelVec,
        cpuUsageUserVec,
        cpuUsageTotalVec,
        cpuPercentage,
    )
}

func deleteLabeledMetric(labels prometheus.Labels, vectors ...*prometheus.GaugeVec) {
    for _, vector := range vectors {
        if vector == nil {
            continue
        }
        if vector.DeletePartialMatch(labels) <= 0 {
            log.Println("[WARN] Metric with labels hasn't been deleted:", labels)
        }
    }
}

func calculateCPUPercentUnix(stat *TContainerStatistic) float64 {
    var (
        cpuPercent = 0.0
        // calculate the change for the cpu usage of the container in between readings
        cpuDelta = float64(stat.CPUStats.CPUUsage.TotalUsage) - float64(stat.CPUStatsPre.CPUUsage.TotalUsage)
        // calculate the change for the entire system between readings
        systemDelta = float64(stat.CPUStats.SystemUsage) - float64(stat.CPUStatsPre.SystemUsage)
    )

    if systemDelta > 0.0 && cpuDelta > 0.0 {
        cpuPercent = (cpuDelta / systemDelta) * 100.0
        if len(stat.CPUStats.CPUUsage.PercpuUsage) > 0 {
            cpuPercent *= float64(len(stat.CPUStats.CPUUsage.PercpuUsage))
        }
    }
    return cpuPercent
}

func showIndexPage(w http.ResponseWriter, r *http.Request) {
    fmt.Fprint(w, "<h1>docker-stats-exporter</h1><hr>")
    fmt.Fprint(w, "<p>Light-weight Prometheus exporter for Docker stats <a href=\"/metrics\">metrics</a></p>")
}
