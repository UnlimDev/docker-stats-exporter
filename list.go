package main

import (
    "errors"
    "fmt"
    "log"
    "sync"
)

type TOpt struct {
    Name  string
    Value any
}

type ThreadList struct {
    sync.Mutex
    items map[string]TThread
}

func (t *ThreadList) Exists(key string) bool {
    _, found := t.items[key]
    return found
}

func (t *ThreadList) Get(key string) (TThread, bool) {
    t.Lock()
    item, found := t.items[key]
    t.Unlock()
    return item, found
}

func (t *ThreadList) GetKeys() []string {
    var res []string
    if t.items == nil {
        return res
    }

    t.Lock()
    for key, _ := range t.items {
        res = append(res, key)
    }
    t.Unlock()
    return res
}

func (t *ThreadList) Put(key string, item TThread) error {
    t.Lock()
    defer t.Unlock()

    if _, found := t.items[key]; found {
        return errors.New(fmt.Sprintf("item with key %s already present in the list", key))
    }

    if t.items == nil {
        t.items = make(map[string]TThread)
    }

    t.items[key] = item
    return nil
}

func (t *ThreadList) Del(key string) {
    t.Lock()
    delete(t.items, key)
    t.Unlock()
}

func (t *ThreadList) StopAll() {
    t.Lock()
    defer t.Unlock()

    if t.items == nil {
        return
    }
    for key, item := range t.items {
        if er := item.Stop(); er != nil {
            log.Println("Error stopping thread:", key, er)
        }
    }
}
