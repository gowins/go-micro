package grpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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

	pmgr := newManager("127.0.0.1:50054", 5, int64(time.Second*10))
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		ch <- struct{}{}
	}()
	for i := 0; i < 100; i++ {
		<-ch
		c := 200
		wg.Add(c)
		for j := 0; j < c; j++ {
			go func() {
				defer wg.Done()

				lock.Lock()
				cond.Wait()
				lock.Unlock()

				invoke(pmgr, t)
				invoke(pmgr, t)
			}()
		}

		println("----------------")

		if i < 99 {
			break
		}
	}

	wg.Wait()

	fmt.Println("len ", len(pmgr.data))
	fmt.Println("created ", pmgr.c.Load())
}

func invoke(pmgr *poolManager, t *testing.T) {
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
