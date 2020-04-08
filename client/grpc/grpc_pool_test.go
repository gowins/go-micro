package grpc

import (
	"net"
	"testing"
	"time"

	"context"

	"github.com/google/uuid"
	"github.com/onsi/gomega"

	"google.golang.org/grpc"

	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

func testPool(t *testing.T, size uint, ttl time.Duration) {
	// setup server
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	s := grpc.NewServer()
	pb.RegisterGreeterServer(s, &greeterServer{})

	go s.Serve(l)
	defer s.Stop()

	// zero pool
	p := newPool(size, ttl)

	for i := 0; i < 10; i++ {
		// get a conn
		cc, err := p.getConn(l.Addr().String(), grpc.WithInsecure())
		if err != nil {
			t.Fatal(err)
		}

		rsp := pb.HelloReply{}

		err = cc.Invoke(context.TODO(), "/helloworld.Greeter/SayHello", &pb.HelloRequest{Name: "John"}, &rsp)
		if err != nil {
			t.Fatal(err)
		}

		if rsp.Message != "Hello John" {
			t.Fatalf("Got unexpected response %v", rsp.Message)
		}

		// release the conn
		p.release(l.Addr().String(), cc)

		p.Lock()
		if i := p.conns[l.Addr().String()].size(); i > size {
			p.Unlock()
			t.Fatalf("pool size %d is greater than expected %d", i, size)
		}
		p.Unlock()
	}
}

func TestGRPCPool(t *testing.T) {
	testPool(t, 1, time.Minute)
	testPool(t, 2, time.Minute)
}

func TestList(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	l := newList()

	cc, err := grpc.Dial("127.0.0.1:8080", grpc.WithInsecure())
	g.Expect(err).ShouldNot(gomega.HaveOccurred())

	p := &poolConn{cc, uuid.New().String(), 1, nil}

	l.emplace(p)
	g.Expect(l.size()).Should(gomega.Equal(uint(1)))
	g.Expect(p).Should(gomega.Equal(l.head))

	p1 := &poolConn{nil, uuid.New().String(), 2, nil}
	l.emplace(p1)
	g.Expect(l.size()).Should(gomega.Equal(uint(2)))
	g.Expect(p1).Should(gomega.Equal(l.head.next))

	p2 := &poolConn{nil, uuid.New().String(), 3, nil}
	l.emplace(p2)
	g.Expect(l.size()).Should(gomega.Equal(uint(3)))
	t.Log("head:", *l.head, "next1:", *l.head.next)

	pop := l.popFront()
	g.Expect(l.size()).Should(gomega.Equal(uint(2)))
	g.Expect(pop.created).Should(gomega.Equal(p.created))
	t.Log("head:", *l.head, "next1:", *l.head.next)

	pop = l.popFront()
	t.Log("pop1:", *pop)
	g.Expect(l.size()).Should(gomega.Equal(uint(1)))

	pop = l.popFront()
	g.Expect(l.size()).Should(gomega.Equal(uint(0)))
	g.Expect(pop.created).Should(gomega.Equal(p2.created))

	pop = l.popFront()
	g.Expect(l.size()).Should(gomega.Equal(uint(0)))
	g.Expect(pop).Should(gomega.BeNil())
}

func BenchmarkList(b *testing.B) {
	l := newList()
	var p *poolConn
	for i := 0; i < b.N; i++ {
		p = &poolConn{}
		l.emplace(p)
	}
	b.Log(l.size())

	for i := 0; i < b.N; i++ {
		l.popFront()
	}
}
