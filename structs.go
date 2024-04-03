package main

import (
    "github.com/docker/docker/api/types"
    "time"
)

type TContainerStatistic struct {
    Id           string                        `json:"id"`
    Name         string                        `json:"name"`
    Read         time.Time                     `json:"read"`
    PreRead      time.Time                     `json:"preread"`
    NumProcs     uint                          `json:"num_procs"`
    PidsStats    types.PidsStats               `json:"pids_stats"`
    BlkioStats   types.BlkioStats              `json:"blkio_stats"`
    StorageStats types.StorageStats            `json:"storage_stats"`
    CPUStats     types.CPUStats                `json:"cpu_stats"`
    CPUStatsPre  types.CPUStats                `json:"precpu_stats"`
    MemoryStats  types.MemoryStats             `json:"memory_stats"`
    Networks     map[string]types.NetworkStats `json:"networks"`
    Labels       map[string]string
}

type TClbOnStatistic func(stat *TContainerStatistic)
type TClbOnRemove func(id string)

type TThread interface {
    Exec() error
    Stop() error

    SetOpt(opt TOpt) error
    GetOpt(name string) *TOpt
}
