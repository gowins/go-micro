package grpc

import (
	"sync"
	"time"

	"github.com/google/uuid"
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
	id      string
	created int64

	next *poolConn
}

type poolList struct {
	maxSize uint // 最大容量
	count   uint // 当前容量
	current uint // 当前指向
	head    *poolConn
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
	var conn *poolConn
	now := time.Now().Unix()
	if ok {
		// existing
		conn = conns.popFront()

		// while we have conns check age and then return one
		// otherwise we'll create a new conn
		for conn != nil {
			// 外部获取的时候检测连接状态
			switch conn.GetState() {
			case connectivity.Connecting:
			case connectivity.TransientFailure:
			case connectivity.Shutdown:
				conn.ClientConn.Close()
				conns.erase(conn)
				conn = conns.popFront()
				continue
			default:
				// if conn is old kill it and move on
				if d := now - conn.created; d > p.ttl {
					conn.ClientConn.Close()
					conns.erase(conn)
					conn = conns.popFront()
					continue
				}
			}
			p.Unlock()
			return conn, nil
		}
	}

	p.Unlock()

	// create new conn
	cc, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	conn = &poolConn{cc, uuid.New().String(), now, nil}

	p.release(addr, conn)

	return conn, nil
}

func (p *pool) release(addr string, conn *poolConn) {
	// otherwise put it back for reuse
	p.Lock()

	conns, ok := p.conns[addr]
	if !ok {
		conns = newList(p.size)
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
func newList(max uint) *poolList {
	if max == 0 {
		max = 100
	}
	return &poolList{
		maxSize: max,
		count:   0,
		current: 0,
		head:    nil,
	}
}

// 尾部添加新项
func (l *poolList) emplace(node *poolConn) {
	if l.count == l.maxSize || node == nil {
		return
	}
	node.next = nil
	if l.count == 0 {
		l.head = node
	} else {
		end := l.head
		isExist := false
		for i := uint(0); i < l.count; i++ {
			if end.id == node.id {
				isExist = true
				break
			}
			if i != l.count-1 {
				end = end.next
			}
		}
		if isExist {
			return
		}
		end.next = node
	}
	l.count++
	//log.Printf("pool list count: %d", l.count)
}

// 获取下一项
func (l *poolList) popFront() *poolConn {
	l.current++
	if l.current > l.maxSize {
		l.current = 1
	} else if l.current > l.count+1 {
		l.current = l.count + 1
	}

	current := l.head
	for i := uint(1); i < l.current; i++ {
		current = current.next
	}

	//log.Printf("pop front pool list current: %d, count: %d", l.current, l.count)
	return current
}

// 移除
func (l *poolList) erase(node *poolConn) {
	end := l.head
	if end.id == node.id {
		l.head = end.next
		l.count--
		return
	}
	for i := uint(1); i < l.count; i++ {
		if end.next.id == node.id {
			end.next = end.next.next
			l.count--
			break
		}
		end = end.next
	}
	if l.current >= l.count {
		l.current = l.count
	}
}

func (l *poolList) size() uint {
	return l.count
}
