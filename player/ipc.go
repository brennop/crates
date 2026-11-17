package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// message is any line mpv sends: a reply (request_id set, no event) or an
// event.
type message struct {
	RequestID *int64          `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
	EntryID   int64           `json:"playlist_entry_id"`
}

// conn is one IPC connection. One reader goroutine routes replies to waiting
// callers by request_id and everything else to onEvent. Writes go through
// one mutex.
type conn struct {
	nc      net.Conn
	timeout time.Duration
	onEvent func(message)
	onDown  func(error) // the socket closed without close() being called

	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]*call // nil once the reader has exited
	closing bool
	done    chan struct{}
}

type call struct {
	ch chan reply
	// hook runs on the reader goroutine when the reply is a success, before
	// any later message is handled.
	hook func(json.RawMessage)
}

type reply struct {
	data json.RawMessage
	err  error
}

func newConn(nc net.Conn, timeout time.Duration, onEvent func(message), onDown func(error)) *conn {
	c := &conn{
		nc:      nc,
		timeout: timeout,
		onEvent: onEvent,
		onDown:  onDown,
		pending: map[int64]*call{},
		done:    make(chan struct{}),
	}
	go c.read()
	return c
}

// request sends cmd (a []any of positional arguments or a map of named ones)
// and waits for its reply.
func (c *conn) request(ctx context.Context, hook func(json.RawMessage), cmd any) (json.RawMessage, error) {
	name := commandName(cmd)
	cl := &call{ch: make(chan reply, 1), hook: hook}
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = cl
	c.mu.Unlock()

	line, err := json.Marshal(struct {
		Command   any   `json:"command"`
		RequestID int64 `json:"request_id"`
	}{cmd, id})
	if err != nil {
		c.forget(id)
		return nil, err
	}
	c.wmu.Lock()
	c.nc.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err = c.nc.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("mpv %s: %w", name, err)
	}

	t := time.NewTimer(c.timeout)
	defer t.Stop()
	select {
	case r := <-cl.ch:
		if ce, ok := r.err.(*CommandError); ok {
			ce.Command = name
		}
		return r.data, r.err
	case <-t.C:
		c.forget(id)
		return nil, fmt.Errorf("mpv %s: %w", name, ErrTimeout)
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *conn) read() {
	br := bufio.NewReader(c.nc)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.handle(line)
		}
		if err != nil {
			c.shutdown(err)
			return
		}
	}
}

func (c *conn) handle(line []byte) {
	var m message
	if json.Unmarshal(line, &m) != nil {
		return
	}
	if m.Event != "" {
		c.onEvent(m)
		return
	}
	if m.RequestID == nil {
		return
	}
	c.mu.Lock()
	cl := c.pending[*m.RequestID]
	delete(c.pending, *m.RequestID)
	c.mu.Unlock()
	if cl == nil {
		return // timed out or cancelled
	}
	if m.Error != "success" {
		cl.ch <- reply{err: &CommandError{Msg: m.Error}}
		return
	}
	if cl.hook != nil {
		cl.hook(m.Data)
	}
	cl.ch <- reply{data: m.Data}
}

func (c *conn) shutdown(err error) {
	c.mu.Lock()
	pending, closing := c.pending, c.closing
	c.pending = nil
	c.mu.Unlock()
	for _, cl := range pending {
		cl.ch <- reply{err: ErrClosed}
	}
	close(c.done)
	if !closing && c.onDown != nil {
		c.onDown(err)
	}
}

// close shuts the connection without reporting it as lost.
func (c *conn) close() {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.nc.Close()
	<-c.done
}

func commandName(cmd any) string {
	switch v := cmd.(type) {
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case map[string]any:
		if s, ok := v["name"].(string); ok {
			return s
		}
	}
	return "command"
}
