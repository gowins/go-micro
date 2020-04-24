package grpc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

func TestGRPCPool(t *testing.T) {
	defaultDebugMode = true
	// Total number of requests = conns * duration/1s * 2
	testPoolConcurrency(t, 51, "127.0.0.1:50053", 100, 2)
}

func testPoolConcurrency(t *testing.T, poolSize int, addr string, conns int, duration int) {
	go runServer(t)
	rand.Seed(time.Now().UnixNano())

	p := newPool(poolSize, 0)

	fmt.Println("第一波 带错误")
	invokeConcurrency(t, p, addr, conns, duration, true)
	invokeConcurrency(t, p, addr, conns, duration, false)
	time.Sleep(time.Second * 2)
	fmt.Println("第二波 不带错误,不应该出现 new conn")
	invokeConcurrency(t, p, addr, conns, duration, false)

	if len(p.pms[addr].conns) != poolSize {
		t.Fatalf("池子没有被塞满,当前池子大小: %d \n", len(p.pms[addr].conns))
	}
	fmt.Println("netstat -an | grep 50053 |grep ESTABLISHED | wc -l", "数量应该为size的两倍", poolSize*2)
	fmt.Println("连接池已塞满")
	time.Sleep(time.Second * 5)

}

func invokeConcurrency(t *testing.T, p *pool, addr string, conns int, duration int, withErr bool) {
	wg := sync.WaitGroup{}
	lock := &sync.Mutex{}
	cond := sync.NewCond(lock)

	go func() {
		for {
			time.Sleep(1 * time.Second)
			lock.Lock()
			cond.Broadcast()
			lock.Unlock()
		}
	}()

	for i := 0; i < conns; i++ {
		wg.Add(duration)
		go func() {
			for i1 := 0; i1 < duration; i1++ {

				lock.Lock()
				cond.Wait()
				lock.Unlock()

				for i2 := 0; i2 < 2; i2++ {
					// get a conn
					invoke(t, p, addr, withErr)
				}
				wg.Done()
			}
		}()
	}
	wg.Wait()
}

func invoke(t *testing.T, p *pool, addr string, withErr bool) {
	cc, err := p.getConn(addr, grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}

	c := pb.NewGreeterClient(cc.ClientConn)
	ctx, _ := context.WithTimeout(context.TODO(), time.Second*2)
	rsp, err := c.SayHello(ctx, &pb.HelloRequest{Name: "John"})

	if err != nil {
		t.Fatal(err)
	}

	if rsp.Message != "Hello John" {
		t.Fatalf("Got unexpected response %v \n", rsp.Message)
	}

	if withErr && rand.Intn(10) == 8 {
		fmt.Println("故意错误")
		err = errors.New("ha ha ha")
	}
	// release the conn
	p.release(addr, cc, err)
}

type server struct{}

func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	time.Sleep(time.Millisecond * 1500)
	return &pb.HelloReply{Message: "Hello John"}, nil
}

func runServer(t *testing.T) {
	lis, err := net.Listen("tcp", ":50053")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterGreeterServer(s, &server{})

	if err := s.Serve(lis); err != nil {
		t.Fatalf("failed to serve: %v", err)
	}
}
