package grpc

import (
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

type pool struct {
	size uint
	ttl  int64

	sync.Mutex
	conns map[string]*poolList
}

type poolConn struct {
	*grpc.ClientConn
	created int64

	next *poolConn
}

type poolList struct {
	count uint

	head *poolConn
}

func newPool(size uint, ttl time.Duration) *pool {
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
		conns = newList()
	}

	conn := conns.popFront()
	now := time.Now().Unix()

	// while we have conns check age and then return one
	// otherwise we'll create a new conn
	for conn != nil {
		// 外部获取的时候检测连接状态
		switch conn.GetState() {
		case connectivity.Shutdown:
		case connectivity.TransientFailure:
			conn.ClientConn.Close()
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
		p.Unlock()
		return conn, nil
	}

	p.Unlock()

	// create new conn
	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	conn = &poolConn{cc, time.Now().Unix(), nil}

	return conn, nil
}

func (p *pool) release(addr string, conn *poolConn) {
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

// 链表
func newList() *poolList {
	return &poolList{
		count: 0,
		head:  nil,
	}
}

// 尾部添加新项
func (l *poolList) emplace(node *poolConn) {
	if l.count == 0 {
		l.head = node
		node.next = nil
	} else {
		end := l.head
		for i := uint(0); i < l.count-1; i++ {
			end = end.next
		}
		end.next = node
	}
	l.count++
}

// 获取头并移除
func (l *poolList) popFront() *poolConn {
	if l.count > 0 {
		head := l.head
		l.head = head.next
		l.count--
		return head
	}
	return nil
}

func (l *poolList) size() uint {
	return l.count
}
