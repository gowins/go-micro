package micro

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/micro/go-micro/client"
	"github.com/micro/go-micro/config/cmd"
	"github.com/micro/go-micro/metadata"
	"github.com/micro/go-micro/server"
	"github.com/micro/go-micro/util/log"
)

type service struct {
	opts Options

	once sync.Once
}

func newService(opts ...Option) Service {
	options := newOptions(opts...)

	options.Client = &clientWrapper{
		options.Client,
		metadata.Metadata{
			HeaderPrefix + "From-Service": options.Server.Options().Name,
		},
	}

	return &service{
		opts: options,
	}
}

// Init initialises options. Additionally it calls cmd.Init
// which parses command line flags. cmd.Init is only called
// on first Init.
func (s *service) Init(opts ...Option) {
	// process options
	for _, o := range opts {
		o(&s.opts)
	}

	s.once.Do(func() {
		// Initialise the command flags, overriding new service
		_ = s.opts.Cmd.Init(
			cmd.Broker(&s.opts.Broker),
			cmd.Registry(&s.opts.Registry),
			cmd.Transport(&s.opts.Transport),
			cmd.Client(&s.opts.Client),
			cmd.Server(&s.opts.Server),
		)
	})
}

func (s *service) Options() Options {
	return s.opts
}

func (s *service) Client() client.Client {
	return s.opts.Client
}

func (s *service) Server() server.Server {
	return s.opts.Server
}

func (s *service) String() string {
	return "micro"
}

func (s *service) Start() error {
	for _, fn := range s.opts.BeforeStart {
		if err := fn(); err != nil {
			return err
		}
	}

	if err := s.opts.Server.Start(); err != nil {
		return err
	}

	for _, fn := range s.opts.AfterStart {
		if err := fn(); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) Stop() error {
	var gerr error

	for _, fn := range s.opts.BeforeStop {
		if err := fn(); err != nil {
			gerr = err
		}
	}

	if err := s.opts.Server.Stop(); err != nil {
		return err
	}

	log.Log("[ExitProgress] Call AfterStop. before.")
	for _, fn := range s.opts.AfterStop {
		if err := fn(); err != nil {
			gerr = err
		}
	}
	log.Log("[ExitProgress] Call AfterStop. end.")

	return gerr
}

func (s *service) Run() (err error) {
	if err = s.Start(); err != nil {
		return
	}

	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	freezing := false
	suspCh := make(chan os.Signal, 1)
	// The SIGUSR1 and SIGUSR2 signals are set aside for you to use any way you want.
	// So you know that \(^o^)/ !
	// https://www.gnu.org/software/libc/manual/html_node/Miscellaneous-Signals.html
	signal.Notify(suspCh, syscall.SIGUSR1)

	select {
	// Waiting for suspend signal
	case <-suspCh:
		freezing = true
	// Waiting for quit signal
	case <-quitCh:
	// Waiting for context cancel
	case <-s.opts.Context.Done():
	}

	if err = s.Stop(); err != nil {
		log.Logf("[ExitProgress] Call stop, err: %v", err)
	}

	// Later, we still need waiting for quit signal if the service is suspending.
	if freezing {
		<-quitCh
	}
	return
}
