package grpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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
	c := pb.NewGreeterClient(conn.ClientConn)
	ctx, _ := context.WithTimeout(context.TODO(), time.Second*2)
	rsp, err := c.SayHello(ctx, &pb.HelloRequest{Name: "John"})
	if err != nil {
		t.Fatal(err)
	}

	if rsp.Message != "Hello John" {
		t.Fatalf("Got unexpected response %v \n", rsp.Message)
	}
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
