//******************************************//
//****************** DEMO *****************//
//****************************************//

//func NewPool() *gobeanstalk.Pool {
//	return &gobeanstalk.Pool{
//		Dial: func() (*gobeanstalk.Conn, error) {
//			conn, err := gobeanstalk.Dial(ADDR)
//			if err != nil {
//				log.Fatal(err)
//			}
//			if err != nil {
//				return nil, err
//			}
//			return conn, nil
//		},
//		MaxIdle:     10,
//		MaxActive:   100,
//		IdleTimeout: 60 * time.Second,
//		MaxLifetime: 180 * time.Second,
//		Wait:        true,
//	}
//}

//main
//func main() {
//	runtime.GOMAXPROCS(runtime.NumCPU())
//	pool := NewPool()
//	defer pool.Close()
//
//	//Producer
//	ch := make(chan int, 10)
//	for i := 1; i <= 10; i++ {
//		go Producer(pool, ch)
//	}
//
//	for i := 1; i <= 10; i++ {
//		<-ch
//	}
//
//	fmt.Println("end...")
//}

//Producer
//func Producer(pool *gobeanstalk.Pool, ch chan int) {
//	conn, err := pool.Get()
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer pool.Release(conn)
//
//	conn.Use(TEST_TUBE)
//	for i := 0; i < 10000; i++ {
//		id, err := conn.Put([]byte(fmt.Sprintf("%s:%d", "msg", i)), 1, 0, 120*time.Second)
//		if err != nil {
//			fmt.Println("[Producer] [", id, "] err:", err)
//		} else {
//			fmt.Println("[Producer]", id)
//		}
//	}
//	ch <- 1
//}

package gobeanstalk

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

var nowFunc = time.Now

// ErrPoolExhausted is returned from a pool connection method (Do, Send,
// Receive, Flush, Err) when the maximum number of database connections in the
// pool has been reached.
var ErrPoolExhausted = errors.New("gobeanstalk: connection pool exhausted")

var (
	errPoolClosed = errors.New("gobeanstalk: connection pool closed")
)

type Pool struct {
	// Dial is an application supplied function for creating and configuring a
	// connection.
	//
	// The connection returned from Dial must not be in a special state
	// (subscribed to pubsub channel, transaction started, ...).
	Dial func() (*Conn, error)

	// Maximum number of idle connections in the pool.
	MaxIdle int

	// Maximum number of connections allocated by the pool at a given time.
	// When zero, there is no limit on the number of connections in the pool.
	MaxActive int

	// Close connections after remaining idle for this duration. If the value
	// is zero, then idle connections are not closed. Applications should set
	// the timeout to a value less than the server's timeout.
	IdleTimeout time.Duration

	// Close connections after MaxLifetime for this duration. If the value
	// is zero, then connections are not closed.
	MaxLifetime time.Duration

	// If Wait is true and the pool is at the MaxActive limit, then Get() waits
	// for a connection to be returned to the pool before returning.
	Wait bool

	// mu protects fields defined below.
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	active int

	// Stack of idleConn with most recently used at the front.
	idle list.List
}

type idleConn struct {
	c *Conn
	t time.Time
}


// Get gets a connection. The application must close the returned connection.
// This method always returns a valid connection so that applications can defer
// error handling to the first use of the connection. If there is an error
// getting an underlying connection, then the connection Err, Do, Send, Flush
// and Receive methods return that error.
func (p *Pool) Get() (*Conn, error) {
	c, err := p.get()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ActiveCount returns the number of active connections in the pool.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	return active
}

// Close releases the resources used by the pool.
func (p *Pool) Close() error {
	p.mu.Lock()
	idle := p.idle
	p.idle.Init()
	p.closed = true
	p.active -= idle.Len()
	if p.cond != nil {
		p.cond.Broadcast()
	}
	p.mu.Unlock()
	for e := idle.Front(); e != nil; e = e.Next() {
		e.Value.(idleConn).c.Close()
	}
	return nil
}

// release decrements the active count and signals waiters. The caller must
// hold p.mu during the call.
func (p *Pool) release() {
	p.active -= 1
	if p.cond != nil {
		p.cond.Signal()
	}
}

// get prunes stale connections and returns a connection from the idle list or
// creates a new connection.
func (p *Pool) get() (*Conn, error) {
	p.mu.Lock()

	// Prune stale connections.

	if timeout := p.IdleTimeout; timeout > 0 {
		for i, n := 0, p.idle.Len(); i < n; i++ {
			e := p.idle.Back()
			if e == nil {
				break
			}
			ic := e.Value.(idleConn)
			if ic.t.Add(timeout).After(nowFunc()) {
				break
			}
			p.idle.Remove(e)
			p.release()
			p.mu.Unlock()
			ic.c.Close()
			p.mu.Lock()
		}
	}

	for {

		// Get idle connection.

		for i, n := 0, p.idle.Len(); i < n; i++ {
			e := p.idle.Front()
			if e == nil {
				break
			}
			ic := e.Value.(idleConn)

			//lifetime expired
			if p.MaxLifetime > 0 && ic.c.createTime.Add(p.MaxLifetime).Before(nowFunc()) {
				p.idle.Remove(e)
				p.release()
				p.mu.Unlock()
				ic.c.Close()
				p.mu.Lock()
				continue
			}

			p.idle.Remove(e)
			p.mu.Unlock()
			return ic.c, nil
		}

		// Check for pool closed before dialing a new connection.

		if p.closed {
			p.mu.Unlock()
			return nil, errPoolClosed
		}

		// Dial new connection if under limit.

		if p.MaxActive == 0 || p.active < p.MaxActive {
			dial := p.Dial
			p.active += 1
			p.mu.Unlock()
			c, err := dial()
			if err != nil {
				p.mu.Lock()
				p.release()
				p.mu.Unlock()
				c = nil
			}
			return c, err
		}

		if !p.Wait {
			p.mu.Unlock()
			return nil, ErrPoolExhausted
		}

		if p.cond == nil {
			p.cond = sync.NewCond(&p.mu)
		}
		p.cond.Wait()
	}
}

//Put back to idleList
func (p *Pool) put(c *Conn, forceClose bool) error {
	p.mu.Lock()
	if !p.closed && !forceClose {
		p.idle.PushFront(idleConn{t: nowFunc(), c: c})
		if p.idle.Len() > p.MaxIdle {
			c = p.idle.Remove(p.idle.Back()).(idleConn).c
		} else {
			c = nil
		}
	}

	if c == nil {
		if p.cond != nil {
			p.cond.Signal()
		}
		p.mu.Unlock()
		return nil
	}

	p.release()
	p.mu.Unlock()
	err := c.Close()
	//c = nil
	return err

}

//Release a used connection, put back to idleList
func (p *Pool) Release(c *Conn, forceClose bool) error {
	p.put(c, forceClose)
	return nil
}