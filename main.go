package main

import (
    "context"
    "fmt"
    "github.com/docker/docker/api/types"
    "github.com/docker/docker/client"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "log"
    "net/http"
    "os"
    "os/signal"
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

// Docker API Client
var cli *client.Client

func getContainerVector(name string, description string) *prometheus.GaugeVec {
    return prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: metricNameSpace,
            Subsystem: metricSubContainer,
            Name:      name,
            Help:      description,
        },
        []string{
            "id",
            "name",
        },
    )
}

func main() {
    chStop := make(chan os.Signal, 1)
    signal.Notify(chStop, os.Interrupt, os.Kill, syscall.SIGTERM)

    // Scrape Handler
    registry = prometheus.NewRegistry()
    handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

    http.Handle("/metrics", handler)
    httpServer = &http.Server{
        Addr:    fmt.Sprintf(":%d", defaultHttpPort),
        Handler: nil,
    }

    go func(srv *http.Server) {
        log.Println("Start scrape server on port:", defaultHttpPort)
        if sErr := srv.ListenAndServe(); sErr != nil {
            log.Println(sErr)
            //log.Fatal("Can not start http server:", sErr)
        }
    }(httpServer)

    // Init master docker API client
    if c, err := client.NewClientWithOpts(client.FromEnv); err != nil {
        panic(err)
    } else {
        cli = c
    }

    statsThreads = new(ThreadList)
    initMetrics()

    var updTime time.Time

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

        // TODO: configurable containers list
        containerList, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: false})
        if err != nil {
            panic(fmt.Sprintf("Error getting container list: %s", err))
        }

        containersCount.With(prometheus.Labels{}).Set(float64(len(containerList)))

        for _, container := range containerList {
            if statsThreads.Exists(container.ID) {
                continue
            }

            mon := new(TContainerMonitor)
            mon.Id = container.ID
            mon.OnStatRead = containerStatisticRead
            mon.OnRemove = containerStopped

            if e := mon.Exec(); e != nil {
                log.Println("Error executing container monitor:", e)
                continue
            }
            if e := statsThreads.Put(container.ID, mon); e != nil {
                log.Println("Error adding thread to list: ", e)
            }
            log.Println("Start monitoring for container:", container.ID[0:12])
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
    log.Println("[DEBUG] Stop container monitoring threads")
    statsThreads.StopAll()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := httpServer.Shutdown(ctx); err != nil {
        log.Fatal("Can not gracefully stop metrics server:", err)
    }

    return
}

func initMetrics() {
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

    memUsageVec = getContainerVector("memory_usage", "Actual value of memory usage by container")
    registry.MustRegister(memUsageVec)

    memLimitVec = getContainerVector("memory_limit", "The limit of memory container can use")
    registry.MustRegister(memLimitVec)

    cpuUsageSystem = getContainerVector("cpu_system", "CPU System Usage")
    registry.MustRegister(cpuUsageSystem)

    cpuUsageTotalVec = getContainerVector("cpu_total", "CPU Usage Total")
    registry.MustRegister(cpuUsageTotalVec)

    cpuUsageKernelVec = getContainerVector("cpu_kernel", "CPU Usage in Kernel Mode")
    registry.MustRegister(cpuUsageKernelVec)

    cpuUsageUserVec = getContainerVector("cpu_user", "CPU Usage in User Mode")
    registry.MustRegister(cpuUsageUserVec)

    cpuPercentage = getContainerVector("cpu_pcnt", "CPU Usage percentage")
    registry.MustRegister(cpuPercentage)
}

func containerStatisticRead(stat *TContainerStatistic) {
    labels := prometheus.Labels{
        "id":   stat.Id[0:12],
        "name": stat.Name,
    }

    memUsageVec.With(labels).Set(float64(stat.MemoryStats.Usage))
    memLimitVec.With(labels).Set(float64(stat.MemoryStats.Limit))

    // TODO: Add available processors

    cpuUsageSystem.With(labels).Set(float64(stat.CPUStats.SystemUsage))

    cpuUsageTotalVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.TotalUsage))
    cpuUsageKernelVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.UsageInKernelmode))
    cpuUsageUserVec.With(labels).Set(float64(stat.CPUStats.CPUUsage.UsageInUsermode))

    cpuPercentage.With(labels).Set(calculateCPUPercentUnix(stat))
}

func containerStopped(containerId string) {
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
    log.Println("Stop container monitoring:", containerId[0:12])

    // Clear container metrics
    name := thread.GetOpt("name")

    labels := prometheus.Labels{
        "id":   containerId[0:12],
        "name": name.Value.(string),
    }

    memUsageVec.Delete(labels)
    memLimitVec.Delete(labels)

    cpuUsageSystem.Delete(labels)

    cpuUsageTotalVec.Delete(labels)
    cpuUsageKernelVec.Delete(labels)
    cpuUsageUserVec.Delete(labels)
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
