package grpc

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

// http2每个连接承载的数据链路，与服务端设置保持一致
var MaxConcurrentStreams = uint32(60)

type pool struct {
	size int
	ttl  int64

	sync.Mutex
	conns map[string]*poolList
}

type poolConn struct {
	*grpc.ClientConn
	id      string // 标识连接唯一
	created int64

	countConn uint32 // 此连接承载的连接数

	front *poolConn
	next  *poolConn
}

type poolList struct {
	count int

	head *poolConn
}

func newPool(size int, ttl time.Duration) *pool {
	return &pool{
		size:  size,
		ttl:   int64(ttl.Seconds()),
		conns: make(map[string]*poolList),
	}
}

func (p *pool) getConn(addr string, opts ...grpc.DialOption) (*poolConn, error) {
	p.Lock()
	conns, ok := p.conns[addr]
	if !ok {
		// not existing
		p.conns[addr] = newList()
	}

	conn := conns.popFront()
	now := time.Now().Unix()

	// while we have conns check age and then return one
	// otherwise we'll create a new conn
	for conn != nil {
		// 外部获取的时候检测连接状态
		switch conn.GetState() {
		case connectivity.Connecting:
		case connectivity.Shutdown:
		case connectivity.TransientFailure:
			conn.ClientConn.Close()
			conns.erases(conn.id)
			conn = conns.popFront()
			continue
		default:
			// if conn is old kill it and move on
			if d := now - conn.created; d > p.ttl {
				conn.ClientConn.Close()
				conn = conns.popFront()
				continue
			}
		}
		// 连接增加
		conn.countConn++
		p.Unlock()
		return conn, nil
	}

	p.Unlock()

	// 更好的连接复用
	ops := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBackoffMaxDelay(3 * time.Second),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	opts = append(opts, ops...)

	// create new conn
	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	conn = &poolConn{cc, uuid.New().String(), time.Now().Unix(), 0, nil, nil}

	return conn, nil
}

func (p *pool) release(addr string, conn *poolConn, err error) {
	// don't store the conn if it has errored
	if err != nil {
		conn.ClientConn.Close()
		return
	}

	// otherwise put it back for reuse
	p.Lock()

	conns, ok := p.conns[addr]
	if !ok {
		conns = newList()
	}

	// 超过容量则丢弃
	if conns.size() >= p.size {
		p.Unlock()
		conn.ClientConn.Close()
		return
	}
	conns.push(conn)
	p.conns[addr] = conns
	p.Unlock()
}

// 先进先出的队列
func newList() *poolList {
	return &poolList{}
}

// 把连接放回来
func (l *poolList) push(node *poolConn) {
	head := l.popFront()
	for head != nil {
		if head.id == node.id {
			head.countConn--
			break
		}
		head = l.popFront()
	}
}

// 添加新项
func (l *poolList) emplace(node *poolConn) {
	if l.head != nil {
		// 头的前项是尾，把尾的后一项设为node
		l.head.front.next = node

		node.front = l.head.front
		node.next = l.head

		// 头的前一项设为node
		l.head.front = node
	} else {
		l.head = node
		// 只有一个的时候
		l.head.front = node
		l.head.next = node
	}

	l.count++
}

// 如果没有适合要求的返回nil
func (l *poolList) popFront() *poolConn {
	if l.count > 0 {
		head := l.head
		l.head = head.next
		return head
	}
	return nil
}

// 删除头项
func (l *poolList) erases(id string) {
	if l.count <= 1 {
		l.head = nil
		l.count = 0
		return
	}
	head := l.popFront()
	for head != nil {
		if head.id == id {
			// 设置新的头前项
			l.head.front = head.front

			// 设置尾的前项
			head.front.next = l.head
			l.count--
			break
		}
		head = l.popFront()
	}
}

func (l *poolList) size() int {
	return l.count
}
