package gpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	MagicNumber      = 0x3bef5c
	connected        = "200 Connected to GPC"
	defaultRPCPath   = "/_gpc_"
	defaultDebugPath = "/debug/gpc"
)

type Option struct {
	MagicNumber    int
	CodecType      Type
	ConnectTimeout time.Duration
	HandleTimeout  time.Duration
}

var DefaultOption = &Option{
	MagicNumber:    MagicNumber,
	CodecType:      GobType,
	ConnectTimeout: time.Second * 10,
}

type Server struct {
	serviceMap sync.Map
}

func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

func Accept(lis net.Listener)         { DefaultServer.Accept(lis) }
func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }
func HandleHTTP()                     { DefaultServer.HandleHTTP() }

func (se *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := se.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

func (se *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := se.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service" + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method" + methodName)
	}
	return
}

func (se *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error: ", err)
			return
		}
		go se.ServeConn(conn)
	}
}

func (se *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { _ = conn.Close() }()
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Panicf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	f := NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type: %s", opt.CodecType)
		return
	}
	se.serveCodec(f(conn), opt.HandleTimeout)
}

var invalidRequest = struct{}{}

func (se *Server) serveCodec(cc Codec, timeout time.Duration) {
	sending := new(sync.Mutex)
	wg := new(sync.WaitGroup)
	for {
		req, err := se.readRequest(cc)
		if err != nil {
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			se.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		go se.handleRequest(cc, req, sending, wg, timeout)
	}
	wg.Wait()
	_ = cc.Close()
}

type request struct {
	h            *Header
	argv, replyv reflect.Value
	mtype        *methodType
	svc          *service
}

func (se *Server) readRequestHeader(cc Codec) (*Header, error) {
	var h Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error: ", err)
		}
		return nil, err
	}
	return &h, nil
}

func (se *Server) readRequest(cc Codec) (*request, error) {
	h, err := se.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = se.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read arg err: ", err)
		return req, err
	}
	return req, nil
}

func (se *Server) sendResponse(cc Codec, h *Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error", err)
	}
}

func (se *Server) handleRequest(cc Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	called := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			se.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{}
			return
		}
		se.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()
	if timeout == 0 {
		<-called
		<-sent
		return
	}
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		se.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}
}

func (se *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Print("rpc hijacking", req.RemoteAddr, ":", err.Error())
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.0 "+connected+"\n\n")
	se.ServeConn(conn)
}

func (se *Server) HandleHTTP() {
	http.Handle(defaultRPCPath, se)
	http.Handle(defaultDebugPath, debugHTTP{se})
	log.Println("rpc server debug path: ", defaultDebugPath)
}
