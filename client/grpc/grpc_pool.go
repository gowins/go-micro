package grpc

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

var (
	defaultDebugMode        = false           // debug模式
	defaultKeepAliveTime    = time.Second * 5 // 保活间隔时间
	defaultKeepAliveTimeout = time.Second * 2 //

	// just for test
	pickDebugMode = false // 调试复用情况
	newConnNums   = 0     // 新建连接数
)

func pool_init() {
	// keepalive time
	if kp := os.Getenv("MICRO_CLIENT_POOL_KEEPALIVE_TIME"); kp != "" {
		if keepalive, err := strconv.Atoi(kp); err == nil && keepalive > 0 {
			defaultKeepAliveTime = time.Duration(keepalive) * time.Second
		}
	}
	// keepalive timeout
	if kpout := os.Getenv("MICRO_CLIENT_POOL_KEEPALIVE_TIMEOUT"); kpout != "" {
		if keepaliveTimeOut, err := strconv.Atoi(kpout); err == nil && keepaliveTimeOut > 0 {
			defaultKeepAliveTimeout = time.Duration(keepaliveTimeOut) * time.Second
		}
	}
	// debug mode
	if debug := os.Getenv("MICRO_CLIENT_POOL_DEBUG"); debug == "true" {
		defaultDebugMode = true
	}
}

type pool struct {
	size  int
	ttl   int64
	debug bool

	KeepAliveTime    time.Duration
	KeepAliveTimeout time.Duration

	sync.Mutex
	pms map[string]*poolManger
}

type poolManger struct {
	pool *pool
	size int

	conns map[*poolConn]struct{}
	queue []*poolConn
}

type poolConn struct {
	*grpc.ClientConn
	ref     int // 引用计数
	created int64
}

func newPool(size int, ttl time.Duration) *pool {

	pool_init()

	return &pool{
		size:  size,
		debug: defaultDebugMode,
		pms:   make(map[string]*poolManger),
	}
}

func (p *pool) getConn(addr string, opts ...grpc.DialOption) (*poolConn, error) {
	p.Lock()
	defer p.Unlock()
	return p.pick(addr, opts...)
}

func (p *pool) pick(addr string, opts ...grpc.DialOption) (*poolConn, error) {

	// no pool
	if p.size == 0 {
		// create new conn
		return newConn(addr, opts...)
	}

	if pm, ok := p.pms[addr]; ok && pm != nil {

		// pick one at round robin
		conn := pm.dequeue()
		if conn == nil {
			// create new conn
			p.Debug("create new conn.")
			return newConn(addr, opts...)
		}

		// this one
		if conn.isReady() {
			conn.ref++
			pm.enqueue(conn)
			return conn, nil
		}

		// not ready, kick it out
		pm.removeConn(conn)
	}

	// create new conn
	p.Debug("create new conn.")
	return newConn(addr, opts...)
}

// create new conn
func newConn(addr string, opts ...grpc.DialOption) (*poolConn, error) {
	opts = WithKeepaliveParams(opts...)

	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	return &poolConn{ClientConn: cc, ref: 1}, err
}

func (p *pool) release(addr string, conn *poolConn, err error) {
	p.Lock()
	defer p.Unlock()

	// conn is never nil
	conn.ref--

	// no pool
	if p.size == 0 {
		_ = conn.Close()
		return
	}

	// pm may be nil
	var (
		pm *poolManger
		ok bool
	)
	if pm, ok = p.pms[addr]; !ok || pm == nil {
		pm = newPoolManger(p.size)
		pm.pool = p
		p.pms[addr] = pm
	}

	if err != nil || !conn.isReady() {
		pm.removeConn(conn)
		return
	}

	// otherwise put it back for reuse
	pm.putConn(conn)
	return
}

func (p *pool) Debug(a ...interface{}) {
	if !p.debug {
		return
	}
	if len(a) > 1 {
		fmt.Printf(a[0].(string), a[1:]...)
		return
	}
	fmt.Println("[debug] ", a)
}

// ----------------------------------------------
// poolConn
// ----------------------------------------------
func (cc *poolConn) isReady() bool {
	if cc.GetState() == connectivity.Ready {
		return true
	}
	return false
}

func (cc *poolConn) Close() error {
	if cc.ref == 0 {
		return cc.ClientConn.Close()
	}
	return nil
}

// ----------------------------------------------
// poolManger
// ----------------------------------------------
func newPoolManger(size int) *poolManger {
	return &poolManger{
		size:  size,
		conns: make(map[*poolConn]struct{}),
		queue: make([]*poolConn, 0, 0),
	}
}

func (pm *poolManger) enqueue(conn *poolConn) {
	// FIFO
	pm.queue = append(pm.queue, conn)
}

func (pm *poolManger) dequeue() *poolConn {
	// The pool is not full yet.
	if len(pm.conns) < pm.size {
		return nil
	}

	// FIFO
	for k, conn := range pm.queue {
		if _, ok := pm.conns[conn]; ok {
			pm.queue = pm.queue[k:]
			return conn
		}
		_ = conn.Close()
	}

	if len(pm.queue) > 0 {
		pm.pool.Debug("All connections in the queue are not available.")
		pm.queue = make([]*poolConn, 0, 0)
	}

	return nil
}

func (pm *poolManger) removeConn(conn *poolConn) {
	pm.pool.Debug("Remove connection.")
	delete(pm.conns, conn)
	_ = conn.Close()
}

func (pm *poolManger) putConn(conn *poolConn) {
	// It already exists in the pool
	if _, ok := pm.conns[conn]; ok {
		return
	}

	if len(pm.conns) >= pm.size {
		pm.pool.Debug("The pool is full, releasing excess connection")
		_ = conn.Close()
		return
	}

	pm.pool.Debug("Put it into the pool")
	pm.enqueue(conn)
	pm.conns[conn] = struct{}{}
}
