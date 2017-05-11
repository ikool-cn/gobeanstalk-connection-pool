# gobeanstalk-connection-pool
Go Beanstalkd Client Library Forked From https://github.com/iwanbk/gobeanstalk. Add Connection Pool Features

## INSTALL
	go get github.com/ikool-cn/gobeanstalk-connection-pool


## USAGE
```go
package main

import (
	"github.com/ikool-cn/gobeanstalk-connection-pool"
	"time"
	"fmt"
	"runtime"
	"log"
)

const (
	ADDR         = "10.0.0.101:11300"
	TEST_TUBE    = "test"
	DEFAULT_TUBE = "default"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	pool := NewPool()
	defer pool.Close()

	//Producer
	chProducer := make(chan int, 10)
	for i := 1; i <= 10; i++ {
		go Producer(pool, chProducer)
	}

	//Consumer
	chConsumer := make(chan int, 10)
	for i := 1; i <= 10; i++ {
		go Consumer(pool, chConsumer)
	}

	for i := 1; i <= 10; i++ {
		<-chProducer
		<-chConsumer
	}

	fmt.Println("end...")
}

//Consumer
func Consumer(pool *gobeanstalk.Pool, ch chan int) {
	for {
		conn, err := pool.Get()
		if err != nil {
			log.Println(err)
		}
		conn.Watch(TEST_TUBE)
		conn.Ignore(DEFAULT_TUBE)
		job, err := conn.Reserve()
		if err != nil {
			pool.Release(conn, true)
			log.Println(err)
		}
		fmt.Printf("[Consumer][%s] id:%d, body:%s\n", TEST_TUBE, job.ID, string(job.Body))
		err = conn.Delete(job.ID)
		if err != nil {
			pool.Release(conn, true)
			log.Println(err)
		}
		pool.Release(conn, false)
	}
	ch <- 1
}

//Producer
func Producer(pool *gobeanstalk.Pool, ch chan int) {
	conn, err := pool.Get()
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Release(conn, false)

	conn.Use(TEST_TUBE)
	for i := 0; i < 10000; i++ {
		id, err := conn.Put([]byte(fmt.Sprintf("%s:%d", "msg", i)), 1, 0, 120*time.Second)
		if err != nil {
			fmt.Println("[Producer] [", id, "] err:", err)
		} else {
			fmt.Println("[Producer]", id)
		}
	}
	ch <- 1
}

func NewPool() *gobeanstalk.Pool {
	return &gobeanstalk.Pool{
		Dial: func() (*gobeanstalk.Conn, error) {
			conn, err := gobeanstalk.Dial(ADDR)
			if err != nil {
				log.Fatal(err)
			}
			if err != nil {
				return nil, err
			}
			return conn, nil
		},
		MaxIdle:     10,
		MaxActive:   100,
		IdleTimeout: 60 * time.Second,
		MaxLifetime: 180 * time.Second,
		Wait:        true,
	}
}
```

## Watch the connection
```
telnet 10.0.0.101 11300
stats
```
![image](https://github.com/ikool-cn/gobeanstalk-connection-pool/blob/master/img/screenshot.png)

## Author

* [ikool](https://github.com/ikool-cn)