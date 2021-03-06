// Package dns resolves ids to dns srv records
package dns

import (
	"fmt"
	"net"

	"github.com/micro/go-micro/network/resolver"
)

type Resolver struct{}

// Resolve assumes ID is a domain name e.g micro.mu
func (r *Resolver) Resolve(id string) ([]*resolver.Record, error) {
	_, addrs, err := net.LookupSRV("network", "udp", id)
	if err != nil {
		return nil, err
	}
	var records []*resolver.Record
	for _, addr := range addrs {
		address := addr.Target
		if addr.Port > 0 {
			address = fmt.Sprintf("%s:%d", addr.Target, addr.Port)
		}
		records = append(records, &resolver.Record{
			Address: address,
		})
	}
	return records, nil
}
