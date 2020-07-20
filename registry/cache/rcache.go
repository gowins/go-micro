// Package cache provides a registry cache
package cache

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/micro/go-micro/registry"
	log "github.com/micro/go-micro/util/log"
)

// Cache is the registry cache interface
type Cache interface {
	// embed the registry interface
	registry.Registry
	// stop the cache watcher
	Stop()
}

type Options struct {
	// TTL is the cache TTL
	TTL time.Duration
}

type Option func(o *Options)

type cache struct {
	registry.Registry
	opts Options

	// registry cache
	sync.RWMutex
	cache   map[string][]*registry.Service
	ttls    map[string]time.Time
	watched map[string]bool

	exit chan bool

	updating sync.Map
}

var (
	DefaultTTL = time.Minute
)

// opGet 是从 etcd 获取数据的行为
// forced 是否 "强制更新"
type opGet func(service string, forced bool) ([]*registry.Service, error)

func backoff(attempts int) time.Duration {
	if attempts == 0 {
		return time.Duration(0)
	}
	return time.Duration(math.Pow(10, float64(attempts))) * time.Millisecond
}

// isValid checks if the service is valid
func (c *cache) isValid(services []*registry.Service, ttl time.Time) bool {
	// no services exist
	if len(services) == 0 {
		return false
	}

	// ttl is invalid
	if ttl.IsZero() {
		return false
	}

	// time since ttl is longer than timeout
	if time.Since(ttl) > c.opts.TTL {
		return false
	}

	// ok
	return true
}

func (c *cache) quit() bool {
	select {
	case <-c.exit:
		return true
	default:
		return false
	}
}

// cp copies a service. Because we're caching handing back pointers would
// create a race condition, so we do this instead its fast enough
func (c *cache) cp(current []*registry.Service) []*registry.Service {
	var services []*registry.Service

	for _, service := range current {
		// copy service
		s := new(registry.Service)
		*s = *service

		// copy nodes
		var nodes []*registry.Node
		for _, node := range service.Nodes {
			n := new(registry.Node)
			*n = *node
			nodes = append(nodes, n)
		}
		s.Nodes = nodes

		// copy endpoints
		var eps []*registry.Endpoint
		for _, ep := range service.Endpoints {
			e := new(registry.Endpoint)
			*e = *ep
			eps = append(eps, e)
		}
		s.Endpoints = eps

		// append service
		services = append(services, s)
	}

	return services
}

func (c *cache) del(service string) {
	delete(c.cache, service)
	delete(c.ttls, service)
}

func (c *cache) get(service string) ([]*registry.Service, error) {
	// read lock
	c.RLock()

	// check the cache first
	services := c.cache[service]
	// get cache ttl
	ttl := c.ttls[service]

	// got services && within ttl so return cache
	if c.isValid(services, ttl) {
		// make a copy
		cp := c.cp(services)
		// unlock the read
		c.RUnlock()
		// return servics
		return cp, nil
	}

	// get does the actual request for a service and cache it
	var get opGet
	get = func(service string, forced bool) ([]*registry.Service, error) {
		// 针对每个服务设置一把锁
		m := new(sync.Mutex)
		val, _ := c.updating.LoadOrStore(service, m)
		mutx, ok := val.(*sync.Mutex)
		if !ok {
			return nil, fmt.Errorf("invalid logic: lock type error, service is %v", service)
		}

		mutx.Lock()
		defer mutx.Unlock()

		if !forced {
			// read lock
			c.RLock()
			// check the cache first
			services := c.cache[service]
			// get cache ttl
			ttl := c.ttls[service]
			// unlock the read
			c.RUnlock()

			// got services && within ttl so return cache
			if c.isValid(services, ttl) {
				// make a copy
				cp := c.cp(services)
				// return servics
				return cp, nil
			}
		}

		t := time.Now()
		defer func() {
			log.Logf("[rcache-getservice]get service(%v) from etcd at: %v cost: %v forced: %v", service, t.Format(time.RFC3339Nano), time.Since(t), forced)
		}()
		// ask the registry
		services, err := c.Registry.GetService(service)
		if err != nil {
			return nil, err
		}

		// cache results
		c.Lock()
		// ttl 在每次 set 时是会更新的，而且粒度是 service ，所以用这个值也可准确的判断
		// 1. 非定时器更新，理论上应该不会触发条件
		// 2. 定时器更新，判断上一次 watch 更新时间是否还在 5 秒内，如果 5 秒内丢弃此次更新
		ttl := c.ttls[service]
		if !forced || (forced && t.Sub(ttl.Add(-c.opts.TTL)) > time.Second*5) {
			c.set(service, c.cp(services))
		} else {
			log.Logf("ignore(%s) at: %v", service, t.Format(time.RFC3339Nano))
		}
		c.Unlock()

		return services, nil
	}

	// watch service if not watched
	go c.run(service, get)

	// unlock the read lock
	c.RUnlock()

	// get and return services
	return get(service, false) // 不"强制更新"
}

// interval 计算某个服务需要更新的间隔
// x - rand(1 - 19)  周期 41-59s 之间更新一次
func interval(in time.Duration) time.Duration {
	rand.Seed(time.Now().UnixNano())
	// [0,n)
	d := in - time.Duration(rand.Int63n(19)+1)*time.Second

	return d
}

func (c *cache) refreshByTimer(service string, get opGet) {
	d := interval(c.opts.TTL)
	log.Logf("service(%v) will refresh cache by interval(%v)", service, d)

	for {
		if c.quit() {
			return
		}

		// 进入更新时先等下，错开服务启动一刻的批量请求
		j := rand.Int63n(100)
		time.Sleep(time.Duration(j) * time.Millisecond)

		// 重试两次，防止一次失败
		for i := 1; i <= 2; i++ {
			_, err := get(service, true) // "强制更新"
			if err == nil {
				break
			}
			log.Logf("get service(%v) from registry failed(count: %v): %v", service, i, err.Error())
		}

		time.Sleep(d)
	}
}

func (c *cache) set(service string, services []*registry.Service) {
	c.cache[service] = services
	c.ttls[service] = time.Now().Add(c.opts.TTL)
}

func (c *cache) update(res *registry.Result) {
	if res == nil || res.Service == nil {
		return
	}

	c.Lock()
	defer c.Unlock()

	services, ok := c.cache[res.Service.Name]
	if !ok {
		// we're not going to cache anything
		// unless there was already a lookup
		return
	}

	if len(res.Service.Nodes) == 0 {
		switch res.Action {
		case "delete":
			c.del(res.Service.Name)
		}
		return
	}

	// existing service found
	var service *registry.Service
	var index int
	for i, s := range services {
		if s.Version == res.Service.Version {
			service = s
			index = i
		}
	}

	switch res.Action {
	case "create", "update":
		if service == nil {
			c.set(res.Service.Name, append(services, res.Service))
			return
		}

		// append old nodes to new service
		for _, cur := range service.Nodes {
			var seen bool
			for _, node := range res.Service.Nodes {
				if cur.Id == node.Id {
					seen = true
					break
				}
			}
			if !seen {
				res.Service.Nodes = append(res.Service.Nodes, cur)
			}
		}

		services[index] = res.Service
		c.set(res.Service.Name, services)
	case "delete":
		if service == nil {
			return
		}

		var nodes []*registry.Node

		// filter cur nodes to remove the dead one
		for _, cur := range service.Nodes {
			var seen bool
			for _, del := range res.Service.Nodes {
				if del.Id == cur.Id {
					seen = true
					break
				}
			}
			if !seen {
				nodes = append(nodes, cur)
			}
		}

		// still got nodes, save and return
		if len(nodes) > 0 {
			service.Nodes = nodes
			services[index] = service
			c.set(service.Name, services)
			return
		}

		// zero nodes left

		// only have one thing to delete
		// nuke the thing
		if len(services) == 1 {
			c.del(service.Name)
			return
		}

		// still have more than 1 service
		// check the version and keep what we know
		var srvs []*registry.Service
		for _, s := range services {
			if s.Version != service.Version {
				srvs = append(srvs, s)
			}
		}

		// save
		c.set(service.Name, srvs)
	}
}

// run starts the cache watcher loop
// it creates a new watcher if there's a problem
func (c *cache) run(service string, get opGet) {
	// set watcher
	c.Lock()
	if v, ok := c.watched[service]; ok && v == true {
		c.Unlock()
		return
	}

	c.watched[service] = true
	c.Unlock()

	// 开启定时刷新
	go c.refreshByTimer(service, get)

	// delete watcher on exit
	defer func() {
		c.Lock()
		delete(c.watched, service)
		c.Unlock()
	}()

	var a, b int

	for {
		// exit early if already dead
		if c.quit() {
			return
		}

		// jitter before starting
		j := rand.Int63n(100)
		time.Sleep(time.Duration(j) * time.Millisecond)

		// create new watcher
		w, err := c.Registry.Watch(
			registry.WatchService(service),
		)

		if err != nil {
			if c.quit() {
				return
			}

			d := backoff(a)

			if a > 3 {
				log.Log("rcache: ", err, " backing off ", d)
				a = 0
			}

			time.Sleep(d)
			a++

			continue
		}

		// reset a
		a = 0

		// watch for events
		if err := c.watch(w); err != nil {
			if c.quit() {
				return
			}

			d := backoff(b)

			if b > 3 {
				log.Log("rcache: ", err, " backing off ", d)
				b = 0
			}

			time.Sleep(d)
			b++

			continue
		}

		// reset b
		b = 0
	}
}

// watch loops the next event and calls update
// it returns if there's an error
func (c *cache) watch(w registry.Watcher) error {
	defer w.Stop()

	// manage this loop
	go func() {
		// wait for exit
		<-c.exit
		w.Stop()
	}()

	for {
		res, err := w.Next()
		if err != nil {
			return err
		}
		c.update(res)
	}
}

func (c *cache) GetService(service string) ([]*registry.Service, error) {
	// get the service
	services, err := c.get(service)
	if err != nil {
		return nil, err
	}

	// if there's nothing return err
	if len(services) == 0 {
		return nil, registry.ErrNotFound
	}

	// return services
	return services, nil
}

func (c *cache) Stop() {
	select {
	case <-c.exit:
		return
	default:
		close(c.exit)
	}
}

func (c *cache) String() string {
	return "rcache"
}

// New returns a new cache
func New(r registry.Registry, opts ...Option) Cache {
	rand.Seed(time.Now().UnixNano())
	options := Options{
		TTL: DefaultTTL,
	}

	for _, o := range opts {
		o(&options)
	}

	return &cache{
		Registry: r,
		opts:     options,
		watched:  make(map[string]bool),
		cache:    make(map[string][]*registry.Service),
		ttls:     make(map[string]time.Time),
		exit:     make(chan bool),
	}
}
