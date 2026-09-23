package diagnostics

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

type flushingWriter struct{ w *statusWriter }
type flushCapability interface {
	http.Flusher
	FlushError() error
}

func (f flushingWriter) Flush() { _ = f.FlushError() }
func (f flushingWriter) FlushError() error {
	if f.w.status == 0 {
		f.w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(f.w.ResponseWriter).Flush()
}

type hijackingWriter struct{ w *statusWriter }

func (h hijackingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.w.ResponseWriter.(http.Hijacker).Hijack()
}

type readingWriter struct{ w *statusWriter }

func (r readingWriter) ReadFrom(src io.Reader) (int64, error) {
	if r.w.status == 0 {
		r.w.WriteHeader(http.StatusOK)
	}
	return r.w.ResponseWriter.(io.ReaderFrom).ReadFrom(src)
}

// Preserve exactly the underlying optional method set. Always declaring every
// method would make unsupported capabilities look available to downstream code.
// Anonymous combinations keep Unwrap for http.ResponseController as well.
func preserveInterfaces(w *statusWriter) http.ResponseWriter {
	mask := 0
	if _, ok := w.ResponseWriter.(http.Flusher); ok {
		mask |= 1
	}
	if _, ok := w.ResponseWriter.(http.Hijacker); ok {
		mask |= 2
	}
	if _, ok := w.ResponseWriter.(http.Pusher); ok {
		mask |= 4
	}
	if _, ok := w.ResponseWriter.(http.CloseNotifier); ok {
		mask |= 8
	}
	if _, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		mask |= 16
	}
	switch mask {
	case 1:
		return struct {
			*statusWriter
			flushCapability
		}{w, flushingWriter{w}}
	case 2:
		return struct {
			*statusWriter
			http.Hijacker
		}{w, hijackingWriter{w}}
	case 3:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
		}{w, flushingWriter{w}, hijackingWriter{w}}
	case 4:
		return struct {
			*statusWriter
			http.Pusher
		}{w, w.ResponseWriter.(http.Pusher)}
	case 5:
		return struct {
			*statusWriter
			flushCapability
			http.Pusher
		}{w, flushingWriter{w}, w.ResponseWriter.(http.Pusher)}
	case 6:
		return struct {
			*statusWriter
			http.Hijacker
			http.Pusher
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.Pusher)}
	case 7:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.Pusher
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.Pusher)}
	case 8:
		return struct {
			*statusWriter
			http.CloseNotifier
		}{w, w.ResponseWriter.(http.CloseNotifier)}
	case 9:
		return struct {
			*statusWriter
			flushCapability
			http.CloseNotifier
		}{w, flushingWriter{w}, w.ResponseWriter.(http.CloseNotifier)}
	case 10:
		return struct {
			*statusWriter
			http.Hijacker
			http.CloseNotifier
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.CloseNotifier)}
	case 11:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.CloseNotifier
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.CloseNotifier)}
	case 12:
		return struct {
			*statusWriter
			http.Pusher
			http.CloseNotifier
		}{w, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier)}
	case 13:
		return struct {
			*statusWriter
			flushCapability
			http.Pusher
			http.CloseNotifier
		}{w, flushingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier)}
	case 14:
		return struct {
			*statusWriter
			http.Hijacker
			http.Pusher
			http.CloseNotifier
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier)}
	case 15:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.Pusher
			http.CloseNotifier
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier)}
	case 16:
		return struct {
			*statusWriter
			io.ReaderFrom
		}{w, readingWriter{w}}
	case 17:
		return struct {
			*statusWriter
			flushCapability
			io.ReaderFrom
		}{w, flushingWriter{w}, readingWriter{w}}
	case 18:
		return struct {
			*statusWriter
			http.Hijacker
			io.ReaderFrom
		}{w, hijackingWriter{w}, readingWriter{w}}
	case 19:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			io.ReaderFrom
		}{w, flushingWriter{w}, hijackingWriter{w}, readingWriter{w}}
	case 20:
		return struct {
			*statusWriter
			http.Pusher
			io.ReaderFrom
		}{w, w.ResponseWriter.(http.Pusher), readingWriter{w}}
	case 21:
		return struct {
			*statusWriter
			flushCapability
			http.Pusher
			io.ReaderFrom
		}{w, flushingWriter{w}, w.ResponseWriter.(http.Pusher), readingWriter{w}}
	case 22:
		return struct {
			*statusWriter
			http.Hijacker
			http.Pusher
			io.ReaderFrom
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), readingWriter{w}}
	case 23:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.Pusher
			io.ReaderFrom
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), readingWriter{w}}
	case 24:
		return struct {
			*statusWriter
			http.CloseNotifier
			io.ReaderFrom
		}{w, w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 25:
		return struct {
			*statusWriter
			flushCapability
			http.CloseNotifier
			io.ReaderFrom
		}{w, flushingWriter{w}, w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 26:
		return struct {
			*statusWriter
			http.Hijacker
			http.CloseNotifier
			io.ReaderFrom
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 27:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.CloseNotifier
			io.ReaderFrom
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 28:
		return struct {
			*statusWriter
			http.Pusher
			http.CloseNotifier
			io.ReaderFrom
		}{w, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 29:
		return struct {
			*statusWriter
			flushCapability
			http.Pusher
			http.CloseNotifier
			io.ReaderFrom
		}{w, flushingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 30:
		return struct {
			*statusWriter
			http.Hijacker
			http.Pusher
			http.CloseNotifier
			io.ReaderFrom
		}{w, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	case 31:
		return struct {
			*statusWriter
			flushCapability
			http.Hijacker
			http.Pusher
			http.CloseNotifier
			io.ReaderFrom
		}{w, flushingWriter{w}, hijackingWriter{w}, w.ResponseWriter.(http.Pusher), w.ResponseWriter.(http.CloseNotifier), readingWriter{w}}
	default:
		return w
	}
}
