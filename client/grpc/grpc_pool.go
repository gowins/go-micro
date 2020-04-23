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
	isn   int // round robin, 解决随机分布不均匀问题
	conns map[*poolConn]struct{}
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
	return p.pickRandom(addr, opts...)
}

func (p *pool) pickRandom(addr string, opts ...grpc.DialOption) (*poolConn, error) {

	pm, ok := p.pms[addr]

	if p.size != 0 && ok && pm != nil {
		conns := pm.conns

		// pick one at round robin
		pm.isn++
		if pm.isn %= p.size; pm.isn < len(conns) {
			var i int
			for conn := range conns {
				if i == pm.isn {
					// this one
					if conn.isReady() {
						conn.ref++
						return conn, nil
					}

					// not ready, kick it out
					p.removeConn(conns, conn)
					_ = conn.Close()
					break
				}
				i++
			}
		}
	}

	// create new conn
	return p.newConn(addr, opts...)
}

// create new conn
func (p *pool) newConn(addr string, opts ...grpc.DialOption) (*poolConn, error) {
	opts = WithKeepaliveParams(opts...)
	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}
	// just for test
	//if pickDebugMode {
	//	newConnNums++
	//}
	p.Debug("newConn")
	return &poolConn{ClientConn: cc, ref: 1}, err
}

func (p *pool) removeConn(conns map[*poolConn]struct{}, conn *poolConn) {
	if conns != nil && conn != nil {
		delete(conns, conn)
		return
	}
	p.Debug("removeConn err, conns:%v, conn:%v \n", conns, conn)
}

func (p *pool) release(addr string, conn *poolConn, err error) {
	p.Lock()
	defer p.Unlock()

	// 池子大小为0, 也就没有放入的必要了
	if p.size == 0 {
		_ = conn.Close()
		return
	}

	// conn is never nil
	conn.ref--

	// pm may be nil
	if _, ok := p.pms[addr]; !ok {
		p.pms[addr] = &poolManger{conns: make(map[*poolConn]struct{})}
	}

	conns := p.pms[addr].conns

	if err != nil || !conn.isReady() {
		p.removeConn(conns, conn)
		_ = conn.Close()
		return
	}

	// otherwise put it back for reuse
	p.putConn(conns, conn)

	return
}

func (p *pool) putConn(conns map[*poolConn]struct{}, conn *poolConn) {
	if conns != nil && conn != nil {
		if len(conns) < p.size {
			p.Debug("put conn to pool")
			conns[conn] = struct{}{}
			return
		}
		// 不在连接池内并且连接池已经满了，那就释放掉
		if _, ok := conns[conn]; !ok {
			_ = conn.Close()
		}
		return
	}
	p.Debug("putConn err, conns:%v, conn:%v \n", conns, conn)
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

func (cc *poolConn) isReady() bool {
	if cc.GetState() != connectivity.Ready {
		//if !pickDebugMode && cc.GetState() != connectivity.Ready { // just for test
		return false
	}
	return true
}

func (cc *poolConn) Close() error {
	if cc.ref == 0 {
		return cc.ClientConn.Close()
	}
	return nil
}
