package tracer

import (
	"log"

	"github.com/uber-go/atomic"
)

var defaultTracer = newManagerTracer()

func TurnOn() {
	defaultTracer.turnOn = true
}

// AddTrace 添加事件日志
func AddTrace(msg ...interface{}) {
	defaultTracer.addTrace(msg)
}

// Inc 增加创建的连接数量
func Inc(addr string) {
	defaultTracer.Inc(addr)
}

// managerTracer 管理 pool manager 的日志
type managerTracer struct {
	turnOn bool
	count  atomic.Uint32
}

func newManagerTracer() *managerTracer {
	return &managerTracer{
		turnOn: false,
	}
}

func (t *managerTracer) addTrace(msg ...interface{}) {
	if t.turnOn {
		log.Println(msg)
	}
}

func (t *managerTracer) Inc(addr string) {
	if t.turnOn {
		t.count.Inc()
		t.addTrace("created new connection, addr is: ", addr)
	}
}
