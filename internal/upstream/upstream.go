// Package upstream предоставляет тип Upstreams для случайного выбора upstream-сервера.
package upstream

import (
	"math/rand"
)

// Upstreams хранит список upstream-серверов и обеспечивает случайный выбор.
type Upstreams struct {
	m    map[string]struct{}
	keys []string
}

// NewUpstreams создаёт Upstreams из переданного списка адресов.
func NewUpstreams(address []string) *Upstreams {
	u := &Upstreams{
		m:    make(map[string]struct{}, len(address)),
		keys: make([]string, len(address)),
	}
	for i, addr := range address {
		u.m[addr] = struct{}{}
		u.keys[i] = addr
	}
	return u
}

// Random возвращает случайный upstream-адрес. Если список пуст, возвращает false.
func (u *Upstreams) Random() (string, bool) {
	if len(u.keys) == 0 {
		return "", false
	}
	return u.keys[rand.Intn(len(u.keys))], true
}

func (u *Upstreams) Append(address string) {
	if _, ok := u.m[address]; ok {
		return
	}
	u.m[address] = struct{}{}
	u.keys = append(u.keys, address)
}
