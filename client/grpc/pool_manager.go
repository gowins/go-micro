package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uber-go/atomic"
	"google.golang.org/grpc"

	"github.com/micro/go-micro/util/log"

	"google.golang.org/grpc/connectivity"
)

const (
	requestPerConn = 8
)

type poolManager struct {
	sync.Mutex

	// 管理当前可选的连接
	indexes []*poolConn
	// 真实可用的连接
	data map[*poolConn]struct{}
	// 真实 pool 的大小，也就是最大池子长度
	size int
	// 连接有效期
	ttl int64
	// 连接地址
	addr string
	// 最后使用时间
	updatedAt time.Time
	//
	tickers chan struct{}
	//
	c atomic.Int32
}

type connState uint8

const (
	invalid connState = iota
	poolFull
	lessThanRef
	moreThanRef
	exceededTTL
)

func newManager(addr string, size int, ttl int64) *poolManager {
	tickerCount := size
	manager := &poolManager{
		indexes: make([]*poolConn, 0, 0),
		data:    make(map[*poolConn]struct{}),
		size:    size,
		ttl:     ttl,
		addr:    addr,
		tickers: make(chan struct{}, tickerCount),
	}

	for i := 0; i < tickerCount; i++ {
		manager.tickers <- struct{}{}
	}

	return manager
}

func (m *poolManager) isValid(conn *poolConn) connState {
	// 无效 1
	if conn == nil {
		return invalid
	}

	// 无效 2
	// 已经从真实数据中移除
	if _, ok := m.data[conn]; !ok {
		return invalid
	}

	// 无效 3
	// 已经过期
	if int64(time.Since(conn.created).Seconds()) >= m.ttl {
		return invalid
	}

	// 无效 4
	// 如果连接状态都不可用了则直接是无效连接
	if conn.GetState() != connectivity.Ready {
		return invalid
	}

	// ===================================================================
	// 有效 1
	// 如果池子已经到达了上限了，那么只要此连接状态是可用的，就认为是有效的连接
	if len(m.data) >= m.size {
		return poolFull
	}
	// 有效 2
	// 如果池子还没有满，但是当前连接已经到达了每个连接上承载的请求数时，认为些连接无效
	if conn.refCount < requestPerConn {
		return lessThanRef
	} else {
		return moreThanRef
	}
	// ===================================================================

	// 其它情况均为无效
	return invalid
}

func (m *poolManager) get(opts ...grpc.DialOption) (*poolConn, error) {
	conn, found := m.tryFindOne()
	if found {
		return conn, nil
	}

	type pair struct {
		conn *poolConn
		err  error
	}

	ctx, _ := context.WithTimeout(context.Background(), time.Second*2)
	ch := make(chan *pair, 1)

	go func() {
		<-m.tickers
		conn, found := m.tryFindOne()
		if found {
			m.tickers <- struct{}{}
			ch <- &pair{conn, nil}
			return
		}
		conn, err := m.create(opts...)
		ch <- &pair{conn, err}
	}()

	select {
	case p := <-ch:
		return p.conn, p.err
	case <-ctx.Done():
		return nil, fmt.Errorf("create new connection timeout. ")
	}
}

func (m *poolManager) tryFindOne() (*poolConn, bool) {
	m.Lock()
	defer m.Unlock()
	m.updatedAt = time.Now() // 更新最后使用时间

	for idx, conn := range m.indexes {
		// 直接移除，下一个请求直接不再选择
		// 标识可关闭
		state := m.isValid(conn)
		if state == invalid {
			// TODO
			conn.closable = true
			delete(m.data, conn)
			continue
		} else if state == moreThanRef {
			continue
		}

		// 在返回前处理下索引对象，将其移动到最末尾
		m.indexes = m.indexes[idx+1:]
		m.indexes = append(m.indexes, conn)

		// 增加当前连接的引用计数
		conn.refCount++
		println(conn, "=====>", conn.refCount)
		return conn, true
	}

	return nil, false
}

func (m *poolManager) create(opts ...grpc.DialOption) (*poolConn, error) {
	conn, found := m.tryFindOne()
	if found {
		return conn, nil
	}

	// 如果从 manager 中找不到可用的连接时，新建一个连接
	cc, err := grpc.Dial(m.addr, opts...)
	if err != nil {
		return nil, err
	}

	conn = &poolConn{ClientConn: cc, created: time.Now(), refCount: 1, closable: false}
	m.c.Inc()
	return conn, nil
}

func (m *poolManager) put(conn *poolConn, err error) {
	m.Lock()
	defer m.Unlock()

	if conn == nil {
		return
	}

	conn.refCount--

	// 1. 任何一次请求错误了就从连接池中移除，并且标识存在失败过
	// 标识可关闭
	if err != nil {
		conn.closable = true
		delete(m.data, conn)
	}

	// 2. 如果池子里已经满了则应该可关闭
	if _, ok := m.data[conn]; !ok && len(m.data) >= m.size {
		conn.closable = true
	}

	// 如果已经失败过，要执行后续处理
	if conn.closable {
		// 判断 refCount 是否为 0， 如果为 0 则直接关闭了
		if conn.refCount <= 0 {
			if err := conn.ClientConn.Close(); err != nil {
				log.Log("poolManager:", err)
			}
		}

		return
	}

	// 如果不在池子里，池子也不满则放入池子，并且开始复用
	if _, ok := m.data[conn]; !ok {
		// 因为如果下一个选择还是会判断是否有效的，这里加入可提前让连接复用
		m.indexes = append(m.indexes, conn)
		m.data[conn] = struct{}{}
		println("release ticker.")
		m.tickers <- struct{}{}
		println("ticker len: ", len(m.tickers))
	}

	return
}

func (m *poolManager) cleanup() bool {
	m.Lock()
	defer m.Unlock()

	if time.Now().Sub(m.updatedAt) < time.Minute*30 {
		return false
	}

	for conn := range m.data {
		if conn.refCount > 0 {
			log.Log("invalid: conn haven't been closed. ", m.addr)
		}
		_ = conn.ClientConn.Close()
	}

	return true
}
