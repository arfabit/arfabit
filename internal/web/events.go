package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// eventStream pushes updates to every open page.
//
// Server-sent events rather than polling: the page never reloads, so a reader's
// scroll position, text selection and search are never disturbed (§14).
type eventStream struct {
	mu       sync.Mutex
	next     int
	watchers map[int]chan string
}

func newEventStream() *eventStream {
	return &eventStream{watchers: map[int]chan string{}}
}

// send delivers a named event to everyone watching.
//
// A watcher whose buffer is full is skipped rather than waited for: a stalled
// browser tab must never hold up a rip.
func (e *eventStream) send(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	message := fmt.Sprintf("event: %s\ndata: %s\n\n", name, data)

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, ch := range e.watchers {
		select {
		case ch <- message:
		default:
		}
	}
}

func (e *eventStream) add() (int, chan string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.next++
	ch := make(chan string, 32)
	e.watchers[e.next] = ch
	return e.next, ch
}

// closeAll ends every open stream.
//
// Server-sent event connections are held open for as long as a page is on
// screen, so an orderly shutdown has to close them: waiting for them to finish
// on their own would wait forever.
func (e *eventStream) closeAll() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id, ch := range e.watchers {
		close(ch)
		delete(e.watchers, id)
	}
}

func (e *eventStream) remove(id int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ch, ok := e.watchers[id]; ok {
		close(ch)
		delete(e.watchers, id)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported here", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := s.events.add()
	defer func() {
		// closeAll may have removed and closed this already.
		defer func() { _ = recover() }()
		s.events.remove(id)
	}()

	// An immediate comment opens the stream so the browser stops waiting.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case message, open := <-ch:
			if !open {
				return
			}
			fmt.Fprint(w, message)
			flusher.Flush()
		}
	}
}
