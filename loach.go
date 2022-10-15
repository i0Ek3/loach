package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mx "github.com/i0Ek3/glib/math"
)

var (
	p Pool
	m Machine
)

type Machine struct {
	URL        *url.URL
	Alive      bool
	Weight     int
	Connection int
	Reverse    *httputil.ReverseProxy
	rw         sync.RWMutex
}

func (m *Machine) InitWeight(w ...int) {
	if len(w) == 0 {
		m.Weight = rand.Intn(10) + 1
	}
	m.Weight = w[0]
}

func (m *Machine) SetAlive(alive bool) {
	m.rw.Lock()
	m.Alive = alive
	m.rw.Unlock()
}

func (m *Machine) IsAlive() (alive bool) {
	m.rw.RLock()
	alive = m.Alive
	m.rw.RUnlock()

	return
}

type Pool struct {
	ma      []*Machine
	current uint64
}

func (p *Pool) AddMachine(ma *Machine) {
	p.ma = append(p.ma, ma)
}

func (p *Pool) SetMachineStatus(url *url.URL, alive bool) {
	for _, m := range p.ma {
		if m.URL.String() == url.String() {
			m.SetAlive(alive)
			break
		}
	}
}

func (p *Pool) GetNextMachineIndex() int {
	return int(atomic.AddUint64(&p.current, uint64(1)) % uint64(len(p.ma)+1))
}

func (p *Pool) GetNextAvailableMachine() *Machine {
	idx := p.GetNextMachineIndex()
	size := len(p.ma) + idx
	for i := idx; i < size; i++ {
		pos := i % len(p.ma)
		if p.ma[pos].IsAlive() {
			if i != idx {
				atomic.StoreUint64(&p.current, uint64(idx))
			}

			return p.ma[pos]
		}
	}

	return nil
}

func (p *Pool) GetMachineWeights(url *url.URL) (weights []int) {
	for _, m := range p.ma {
		if m.IsAlive() {
			if m.URL.String() == url.String() {
				weights = append(weights, m.Weight)
			}
		}
	}

	return
}

func (p *Pool) GetMaximumWeightMachine(url *url.URL) *Machine {
	weights := p.GetMachineWeights(url)
	for i, m := range p.ma {
		if m.Weight == mx.Tmax(weights) {
			atomic.StoreUint64(&p.current, uint64(i))

			return m
		}
	}

	return nil
}

func (p *Pool) GetMachineConnections(url *url.URL) (connections []int) {
	for _, m := range p.ma {
		if m.IsAlive() {
			if m.URL.String() == url.String() {
				connections = append(connections, m.Connection)
			}
		}
	}

	return
}

func (p *Pool) GetMinimumConnectionMachine(url *url.URL) *Machine {
	connections := p.GetMachineConnections(url)
	for i, m := range p.ma {
		if m.Connection == mx.Tmin(connections) {
			atomic.StoreUint64(&p.current, uint64(i))

			return m
		}
	}

	return nil
}

func (p *Pool) HeartbeatCheck() {
	for _, m := range p.ma {
		alive := isMachineAlive(m.URL)
		m.SetAlive(alive)
		if !alive {
			log.Printf("machine %s already down\n", m.URL)
		} else {
			log.Printf("machine %s still alive\n", m.URL)
		}
	}
}

func HeartbeatCheckWithTimer() {
	timer := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-timer.C:
			log.Println("starting heartbeat check...")
			p.HeartbeatCheck()
			log.Println("heartbeat check done")
		}
	}
}

type (
	Attempt int
	Retry   int
)

var (
	R Retry
	A Attempt
)

func GetRetryFromContext(req *http.Request) int {
	if retry, ok := req.Context().Value(R).(int); ok {
		return retry
	}

	return 0
}

func GetAttemptFromContext(req *http.Request) int {
	if attempt, ok := req.Context().Value(A).(int); ok {
		return attempt
	}

	return 1
}

func AttemptHelper(w http.ResponseWriter, req *http.Request) {
	attempt := GetAttemptFromContext(req)
	if attempt > times {
		log.Printf("%s(%s) reached maximum attempt times, terminating...\n", req.RemoteAddr, req.URL.Path)
		http.Error(w, "machine unavailable", http.StatusServiceUnavailable)

		return
	}
}

// Round Robin
func RR(w http.ResponseWriter, req *http.Request) {
	roundRobin(w, req)
}

func roundRobin(w http.ResponseWriter, req *http.Request) {
	AttemptHelper(w, req)
	machine := p.GetNextAvailableMachine()
	if machine != nil {
		machine.Reverse.ServeHTTP(w, req)

		return
	}
	http.Error(w, "machine unavailable", http.StatusServiceUnavailable)
}

// Weighted Round Robin
func WRR(w http.ResponseWriter, req *http.Request) {
	weightedRoundRobin(w, req)
}

func weightedRoundRobin(w http.ResponseWriter, req *http.Request) {
	m.InitWeight()
	AttemptHelper(w, req)
	machine := p.GetMaximumWeightMachine(req.URL)
	if machine != nil {
		machine.Reverse.ServeHTTP(w, req)

		return
	}
	http.Error(w, "machine unavailable", http.StatusServiceUnavailable)
}

// Least Connections
func LC(w http.ResponseWriter, req *http.Request) {
	leastConnections(w, req)
}

func leastConnections(w http.ResponseWriter, req *http.Request) {
	AttemptHelper(w, req)
	machine := p.GetMinimumConnectionMachine(req.URL)
	if machine != nil {
		machine.Reverse.ServeHTTP(w, req)
		machine.Connection += 1

		return
	}
	http.Error(w, "machine unavailable", http.StatusServiceUnavailable)
}

func isMachineAlive(url *url.URL) bool {
	timeout := 3 * time.Second
	conn, err := net.DialTimeout("tcp", url.Host, timeout)
	if err != nil {
		log.Println("machine unreachable, error:", err)

		return false
	}
	defer conn.Close()

	return true
}

type F func(http.ResponseWriter, *http.Request)

func getFuncName(v any, delimiter ...rune) string {
	fName := runtime.FuncForPC(reflect.ValueOf(v).Pointer()).Name()
	fields := strings.FieldsFunc(fName, func(r rune) bool {
		for _, d := range delimiter {
			if r == d {
				return true
			}
		}

		return false
	})
	if l := len(fields); l > 0 {
		return fields[l-1]
	}

	return ""
}

var (
	// for command line use
	list  string
	port  int
	times int
	mode  string
)

func run(f F) {
	if len(list) == 0 {
		log.Fatal("the number of machine must >= 1 then load balance works")
	}

	tokens := strings.Split(list, ",")
	for _, t := range tokens {
		url, err := url.Parse(t)
		if err != nil {
			log.Fatal(err)
		}

		pxy := httputil.NewSingleHostReverseProxy(url)
		pxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			log.Printf("[%s] %s\n", url.Host, err.Error())
			retry := GetRetryFromContext(req)
			if retry < times {
				select {
				case <-time.After(10 * time.Millisecond):
					ctx := context.WithValue(req.Context(), R, retry+1)
					pxy.ServeHTTP(w, req.WithContext(ctx))
				}

				return
			}

			p.SetMachineStatus(url, false)

			attempt := GetAttemptFromContext(req)
			log.Printf("%s(%s) retrying %d times...\n", req.RemoteAddr, req.URL.Path, attempt)

			ctx := context.WithValue(req.Context(), A, attempt+1)
			f(w, req.WithContext(ctx))
		}
		p.AddMachine(&Machine{
			URL:     url,
			Alive:   true,
			Reverse: pxy,
		})
		log.Printf("machine %s is online now\n", url)
	}

	server := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(f),
	}

	go HeartbeatCheckWithTimer()
	log.Printf("load balancer running at :%d, with strategy %s\n", port, getFuncName(f, '.'))

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func init() {

	// 24022 =  l  o a c h
	//    26 % 12 15 1 3 8
	// 			2  4 0 2 2

	flag.StringVar(&list, "list", "", "load balanced machines, i.e. http://:24023,http://:24024,...")
	flag.IntVar(&port, "port", 24022, "available port to serve")
	flag.IntVar(&times, "times", 3, "retry times")
	flag.StringVar(&mode, "mode", "RR", "load balance strategy, optional WRR, LC")

	flag.Parse()
}

func main() {
	var f F
	switch mode {
	case "WRR":
		f = WRR
	case "LC":
		f = LC
	default:
		f = RR
	}
	run(f)
}
