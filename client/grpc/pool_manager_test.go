package grpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/uber-go/atomic"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

func Test_poolManager_get(t *testing.T) {
	wg := sync.WaitGroup{}
	lock := &sync.Mutex{}
	cond := sync.NewCond(lock)

	go func() {
		for i := 0; i < 100; i++ {
			time.Sleep(time.Second)

			lock.Lock()
			cond.Broadcast()
			lock.Unlock()
		}
	}()

	pmgr := newManager("127.0.0.1:50054", 10, 10)
	c := 2000
	for i := 0; i < 30; i++ {
		for j := 0; j < c; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				lock.Lock()
				cond.Wait()
				lock.Unlock()

				invoke(pmgr, t)
				invoke(pmgr, t)
			}()
		}

		time.Sleep(time.Second)
	}

	wg.Wait()

	fmt.Println("len ", len(pmgr.data))
	fmt.Println("created ", pmgr.c.Load())
	fmt.Println("req ", req.Load())
	fmt.Println("req t", reqt.Load())
	fmt.Println("req / per", reqt.Load()/req.Load())

	for conn := range pmgr.data {
		fmt.Println("refConn ", conn.refCount)
	}
}

var req atomic.Int64
var reqt atomic.Int64

func invoke(pmgr *poolManager, t *testing.T) {
	now := time.Now()
	defer func() {
		req.Add(1)
		reqt.Add(time.Since(now).Nanoseconds())
	}()
	conn, err := pmgr.get(grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	err = invokeWithConn(conn.ClientConn)
	pmgr.put(conn, err)
}

func invokeWithConn(conn *grpc.ClientConn) error {
	c := pb.NewGreeterClient(conn)
	ctx, _ := context.WithTimeout(context.TODO(), time.Second*2)
	rsp, err := c.SayHello(ctx, &pb.HelloRequest{Name: "John"})
	if err != nil {
		return err
	}
	if rsp.Message != "Hello John" {
		return fmt.Errorf("Got unexpected response %v \n", rsp.Message)
	}

	return nil
}

func invokeWithErr(pmgr *poolManager, t *testing.T) {
	conn, err := pmgr.get(grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	_ = invokeWithConn(conn.ClientConn)
	pmgr.put(conn, errors.New("error here. "))
}

func invokeWithCanceledErr(pmgr *poolManager, t *testing.T) {
	conn, err := pmgr.get(grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	err = invokeWithConn(conn.ClientConn)
	pmgr.put(conn, err)
}

func ExampleTicketGet() {
	tickets := newTicker(10)
	tickets.get()
	fmt.Println(tickets.size())
	tickets.release()
	fmt.Println(tickets.size())

	// Output:
	// 9
	// 10
}

func TestPool(t *testing.T) {
	Convey("pool size", t, func() {

		size := 10
		ttl := 2
		addr := "127.0.0.1:50054"
		pm := newManager(addr, size, int64(ttl))

		Convey("只请求一次,map跟slice都有一个连接", func() {
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次等待ttl过期,map跟slice都还有一个连接", func() {
			invoke(pm, t)
			time.Sleep(time.Second * time.Duration(ttl))
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次等待ttl过期后再请求,map有一个连接,而slice会有两个,其中一个是过期连接", func() {
			invoke(pm, t)
			time.Sleep(time.Second * time.Duration(ttl))
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 2)
		})

		Convey("先请求一次等待ttl过期再请求两次,map跟slice都只会有一个连接", func() {
			invoke(pm, t)
			time.Sleep(time.Second * time.Duration(ttl))
			invoke(pm, t)
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次触发错误,map跟slice都没有连接,再第二次正常请求,map跟slice都只会有一个连接", func() {
			invokeWithErr(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 0)
			So(len(pm.indexes), ShouldEqual, 0)

			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次,再第二次请求一次,map没有连接,而slice会有一个是待关闭连接", func() {
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)

			invokeWithErr(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 0)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次,再第二次请求有Cancel错误,map跟slice都有一个连接,不受特定错误影响", func() {
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)

			invokeWithCanceledErr(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
		})

		Convey("先请求一次没有错误,第二次拿出来先不放回去,第三次请求触发错误之后再发起第二次请求", func() {

			// 第一次一切正常
			invoke(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)

			// 第二次先取出来
			conn, _ := pm.get(grpc.WithInsecure())

			// 第三次报错
			invokeWithErr(pm, t)
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 0)
			So(len(pm.indexes), ShouldEqual, 1)
			So(conn.closable, ShouldBeTrue)
			So(conn.closed, ShouldBeFalse)

			// 这时候第二次才请求
			err := invokeWithConn(conn.ClientConn)
			pm.put(conn, err)

			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 0)
			So(len(pm.indexes), ShouldEqual, 1)
			So(conn.closed, ShouldBeTrue)

			// 第四次请求
			conn1, _ := pm.get(grpc.WithInsecure())
			So(pm.tickets.size(), ShouldEqual, size-1)
			err = invokeWithConn(conn1.ClientConn)
			pm.put(conn1, err)
			So(pm.tickets.size(), ShouldEqual, size)
		})
	})

	Convey("create new conn", t, func() {

		size := 2
		ttl := 3
		addr := "127.0.0.1:50054"
		pm := newManager(addr, size, int64(ttl))

		Convey("先请求一次,再并发请求requestPerConn次,map跟slice都只有有一个连接", func() {
			invoke(pm, t)

			wg := sync.WaitGroup{}
			lock := &sync.Mutex{}
			cond := sync.NewCond(lock)

			go func() {
				time.Sleep(time.Second * 2)
				lock.Lock()
				cond.Broadcast()
				lock.Unlock()
			}()
			for i := 0; i < requestPerConn; i++ {
				wg.Add(1)
				go func() {
					lock.Lock()
					cond.Wait()
					lock.Unlock()

					invoke(pm, t)
					wg.Done()
				}()
			}
			wg.Wait()
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 1)
			So(len(pm.indexes), ShouldEqual, 1)
			So(pm.c.Load(), ShouldEqual, 1)
		})

		Convey("先请求一次,再并发请求requestPerConn+1次,map跟slice都有两个连接", func() {
			invoke(pm, t)

			wg := sync.WaitGroup{}
			lock := &sync.Mutex{}
			cond := sync.NewCond(lock)

			go func() {
				time.Sleep(time.Second * 2)
				lock.Lock()
				cond.Broadcast()
				lock.Unlock()
			}()
			for i := 0; i < requestPerConn+1; i++ {
				wg.Add(1)
				go func() {
					lock.Lock()
					cond.Wait()
					lock.Unlock()

					invoke(pm, t)
					wg.Done()
				}()
			}
			wg.Wait()
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, 2)
			So(len(pm.indexes), ShouldEqual, 2)
			So(pm.c.Load(), ShouldEqual, 2)
		})

		Convey("先并发两次请求,再并发请求requestPerConn*size次,map跟slice都有两个连接", func() {
			conn1, _ := pm.get(grpc.WithInsecure())
			conn2, _ := pm.get(grpc.WithInsecure())
			err1 := invokeWithConn(conn1.ClientConn)
			err2 := invokeWithConn(conn2.ClientConn)
			pm.put(conn1, err1)
			pm.put(conn2, err2)

			wg := sync.WaitGroup{}
			lock := &sync.Mutex{}
			cond := sync.NewCond(lock)

			go func() {
				time.Sleep(time.Second * 2)
				lock.Lock()
				cond.Broadcast()
				lock.Unlock()
			}()
			for i := 0; i < requestPerConn*size; i++ {
				wg.Add(1)
				go func() {
					lock.Lock()
					cond.Wait()
					lock.Unlock()

					invoke(pm, t)
					wg.Done()
				}()
			}
			wg.Wait()
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, size)
			So(len(pm.indexes), ShouldEqual, size)
			So(pm.c.Load(), ShouldEqual, 2)
		})

		Convey("先并发两次请求,再并发请求(requestPerConn*size)+1次,map跟slice都有两个连接", func() {
			conn1, _ := pm.get(grpc.WithInsecure())
			conn2, _ := pm.get(grpc.WithInsecure())
			err1 := invokeWithConn(conn1.ClientConn)
			err2 := invokeWithConn(conn2.ClientConn)
			pm.put(conn1, err1)
			pm.put(conn2, err2)

			wg := sync.WaitGroup{}
			lock := &sync.Mutex{}
			cond := sync.NewCond(lock)

			go func() {
				time.Sleep(time.Second * 2)
				lock.Lock()
				cond.Broadcast()
				lock.Unlock()
			}()
			for i := 0; i < (requestPerConn*size)+1; i++ {
				wg.Add(1)
				go func() {
					lock.Lock()
					cond.Wait()
					lock.Unlock()

					invoke(pm, t)
					wg.Done()
				}()
			}
			wg.Wait()
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, size)
			So(len(pm.indexes), ShouldEqual, size)
			So(pm.c.Load(), ShouldEqual, 2)
		})

		Convey("先请求一次,再并发请求(requestPerConn*size)+1次,map跟slice都有三个连接", func() {
			invoke(pm, t)

			wg := sync.WaitGroup{}
			lock := &sync.Mutex{}
			cond := sync.NewCond(lock)

			go func() {
				time.Sleep(time.Second * 2)
				lock.Lock()
				cond.Broadcast()
				lock.Unlock()
			}()
			for i := 0; i < (requestPerConn*size)+1; i++ {
				wg.Add(1)
				go func() {
					lock.Lock()
					cond.Wait()
					lock.Unlock()

					invoke(pm, t)
					wg.Done()
				}()
			}
			wg.Wait()
			So(pm.tickets.size(), ShouldEqual, size)
			So(len(pm.data), ShouldEqual, size)
			So(len(pm.indexes), ShouldEqual, size)
			So(pm.c.Load(), ShouldEqual, 3)
		})
	})

}
