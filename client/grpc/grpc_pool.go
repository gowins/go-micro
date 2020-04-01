package grpc

import (
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

type pool struct {
	size int
	ttl  int64

	maxIdle int

	sync.Mutex
	conns map[string]*poolList
}

type poolConn struct {
	*grpc.ClientConn
	created int64

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
			conn = conns.popFront()
			continue
		case connectivity.Idle:
		case connectivity.Ready:
			// if conn is old kill it and move on
			if d := now - conn.created; d > p.ttl {
				conn.ClientConn.Close()
				conn = conns.popFront()
				continue
			}
		}
		p.Unlock()
		return conn, nil
	}

	p.Unlock()

	// create new conn
	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	conn = &poolConn{cc, time.Now().Unix(), nil, nil}

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
	conns.emplace(conn)
	p.conns[addr] = conns
	p.Unlock()
}

func newList() *poolList {
	return &poolList{}
}

// list interface
func (l *poolList) emplace(node *poolConn) {
	if l.head != nil {
		end := l.head.front

		end.next = node

		node.front = end
		node.next = l.head
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
		front := l.head
		l.erase()
		return front
	}
	return nil
}

// 删除头
func (l *poolList) erase() {
	if l.count == 0 {
		return
	} else if l.count == 1 {
		l.count = 0
		l.head = nil
		return
	}
	head := l.head

	head.next.front = head.front
	head.front.next = head.next

	l.count--
}

func (l *poolList) size() int {
	return l.count
}
