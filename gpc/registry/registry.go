package registry

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type GPCRegistry struct {
	timeout time.Duration
	mu      sync.Mutex
	servers map[string]*ServerItem
}

type ServerItem struct {
	Addr  string
	start time.Time
}

const (
	defaultPath    = "/_gpc_/registry"
	defaultTimeout = time.Minute * 5
)

func New(timeout time.Duration) *GPCRegistry {
	return &GPCRegistry{
		servers: make(map[string]*ServerItem),
		timeout: timeout,
	}
}

var DefaultGPCRegister = New(defaultTimeout)

func (g *GPCRegistry) putServer(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.servers[addr]
	if s == nil {
		g.servers[addr] = &ServerItem{Addr: addr, start: time.Now()}
	} else {
		s.start = time.Now()
	}
}

func (g *GPCRegistry) aliveServers() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var alive []string
	for addr, s := range g.servers {
		if g.timeout == 0 || s.start.Add(g.timeout).After(time.Now()) {
			alive = append(alive, addr)
		} else {
			delete(g.servers, addr)
		}
	}
	sort.Strings(alive)
	return alive
}

func (g *GPCRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		w.Header().Set("X-GPC-Servers", strings.Join(g.aliveServers(), ","))
	case "POST":
		addr := req.Header.Get("X-GPC-Servers")
		if addr == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		g.putServer(addr)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (g *GPCRegistry) HandleHTTP(registryPath string) {
	http.Handle(registryPath, g)
	log.Println("rpc registry path: ", registryPath)
}

func HandleHTTP() {
	DefaultGPCRegister.HandleHTTP(defaultPath)
}

func Heartbeat(registry, addr string, duration time.Duration) {
	if duration == 0 {
		duration = defaultTimeout - time.Duration(1)*time.Minute
	}
	var err error
	err = sendHeartbeat(registry, addr)
	go func() {
		t := time.NewTicker(duration)
		for err == nil {
			<-t.C
			err = sendHeartbeat(registry, addr)
		}
	}()
}

func sendHeartbeat(registry, addr string) error {
	log.Println(addr, "send heart beat to registry", registry)
	httpClient := &http.Client{}
	req, _ := http.NewRequest("POST", registry, nil)
	req.Header.Set("X-GPC-Server", addr)
	if _, err := httpClient.Do(req); err != nil {
		log.Println("rpc server: heart beat err:", err.Error())
		return err
	}
	return nil
}
