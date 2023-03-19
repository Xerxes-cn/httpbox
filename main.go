package main

import (
	"context"
	"httpbox/gpc"
	"httpbox/gpc/registry"
	"httpbox/gpc/xclient"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

//func onlyForV2() glask.HandlerFunc {
//	return func(c *glask.Context) {
//		t := time.Now()
//		c.Next()
//		log.Printf("[%d] %s in %v for group v2", c.StatusCode, c.Req.RequestURI, time.Since(t))
//	}
//}

//func main() {
//	g := glask.New()
//	g.GET("/", func(c *glask.Context) {
//		c.HTML(http.StatusOK, "<h1>Hello World</h1>", nil)
//	})
//	v1 := g.Group("/v1")
//	{
//		v1.GET("/hello", func(c *glask.Context) {
//			c.String(http.StatusOK, "hello %s, you're at %s\n", c.Query("name"), c.Path)
//		})
//
//		v1.GET("/hello/:name", func(c *glask.Context) {
//			c.String(http.StatusOK, "hello %s, you're at %s\n", c.Param("name"), c.Path)
//		})
//	}
//
//	v2 := g.Group("/v2")
//	v2.Use(onlyForV2())
//	{
//		v2.GET("/hello/*filepath", func(c *glask.Context) {
//			c.JSON(http.StatusOK, glask.H{"filepath": c.Param("filepath")})
//		})
//
//		v2.POST("/login", func(c *glask.Context) {
//			c.JSON(http.StatusOK, glask.H{
//				"username": c.PostForm("username"),
//				"password": c.PostForm("password"),
//			})
//		})
//	}
//
//	g.Run(":9999")
//}

type Foo int

type Args struct {
	Num1, Num2 int
}

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num2 + args.Num1
	return nil
}

func (f Foo) Power(args Args, reply *int) error {
	*reply = args.Num2 * args.Num1
	return nil
}

func (f Foo) Sleep(args Args, reply *int) error {
	time.Sleep(time.Second * time.Duration(args.Num1))
	*reply = args.Num1 + args.Num2
	return nil
}

func startRegistry(wg *sync.WaitGroup) {
	l, _ := net.Listen("tcp", ":9999")
	registry.HandleHTTP()
	wg.Done()
	_ = http.Serve(l, nil)
}

func startServer(addr string, wg *sync.WaitGroup) {
	var foo Foo
	l, _ := net.Listen("tcp", ":0")
	server := gpc.NewServer()
	_ = server.Register(&foo)
	registry.Heartbeat(addr, "tcp@"+l.Addr().String(), 0)
	wg.Done()
	server.Accept(l)
}

func f(xc *xclient.XClient, ctx context.Context, typ, serviceMethod string, args *Args) {
	var reply int
	var err error
	switch typ {
	case "call":
		err = xc.Call(ctx, serviceMethod, args, &reply)
	case "broadcast":
		err = xc.Broadcast(ctx, serviceMethod, args, &reply)
	}
	if err != nil {
		log.Printf("%s %s error: %v", typ, serviceMethod, err.Error())
	} else {
		log.Printf("%s %s success: %d + %d = %d", typ, serviceMethod, args.Num1, args.Num2, reply)
	}
}

func call(registry string) {
	d := xclient.NewGPCRegistryDiscovery(registry, 0)
	xc := xclient.NewXClient(d, xclient.RandomSelect, nil)
	defer func() { _ = xc.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f(xc, context.Background(), "call", "Foo.Sum", &Args{Num1: i, Num2: i * i})
		}(i)
	}
	wg.Wait()
}

func broadcast(registry string) {
	d := xclient.NewGPCRegistryDiscovery(registry, 0)
	xc := xclient.NewXClient(d, xclient.RandomSelect, nil)
	defer func() { _ = xc.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f(xc, context.Background(), "broadcast", "Foo.Sum", &Args{Num1: i, Num2: i * i})
			ctx, _ := context.WithTimeout(context.Background(), time.Second*2)
			f(xc, ctx, "broadcast", "Foo.Sleep", &Args{Num1: i, Num2: i * i})

		}(i)
	}
	wg.Wait()
}

func main() {
	log.SetFlags(0)
	registryAddr := "http://localhost:9999/_gpc_/registry"
	var wg sync.WaitGroup
	wg.Add(1)
	go startRegistry(&wg)
	wg.Wait()

	time.Sleep(time.Second)
	wg.Add(2)
	go startServer(registryAddr, &wg)
	go startServer(registryAddr, &wg)
	wg.Wait()
	time.Sleep(time.Second)
	call(registryAddr)
	broadcast(registryAddr)
}
