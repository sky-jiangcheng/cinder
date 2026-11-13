package writer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Endpoint is what the writer publishes beside the database so other Cinder
// processes can find it. The token is the access control: the file is created
// 0600 in a 0750 data directory, so only this user's processes can read it —
// the same boundary that already protects the knowledge base itself.
//
// ADR-0058 left "loopback HTTP vs Unix socket" open on the grounds that a
// loopback port can be dialed by any local process. The token answers that
// without a socket: an attacker able to read this file is already able to read
// repotory.db, so the channel adds no reachable surface.
type Endpoint struct {
	Addr    string `json:"addr"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	// CoreVersion and SchemaVersion let a client notice it is talking to a
	// Cinder of a different build. Mismatch is logged, not refused: mixed
	// versions on one machine are a supported state today, and a hard failure
	// would break a working setup to report an oddity.
	CoreVersion   string `json:"core_version"`
	SchemaVersion int    `json:"schema_version"`
	StartedAt     string `json:"started_at"`
}

// ErrNoEndpointPublished means no writer has advertised an address. Either
// there is no writer (the caller may elect itself) or the writer is mid-startup.
var ErrNoEndpointPublished = errors.New("no writer endpoint published")

// StaleError reports an endpoint whose process is no longer reachable. The
// caller treats it as "the writer vanished": it tries to win the election and
// take over, which is how a killed desktop stops blocking agent writes.
//
// PreExecution distinguishes "the connection was never established, so the
// write certainly did not happen" (re-electing and retrying is safe) from
// "the writer died mid-request" (retrying could apply a non-idempotent write
// twice, so the caller must report the ambiguity instead).
type StaleError struct {
	Addr         string
	PreExecution bool
	Err          error
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("writer endpoint %s is not reachable: %v", e.Addr, e.Err)
}

func (e *StaleError) Unwrap() error { return e.Err }

// IsStale reports whether err means "the published writer is gone".
func IsStale(err error) bool {
	var s *StaleError
	return errors.As(err, &s)
}

// Publish writes the endpoint file atomically. A reader never sees a half
// written file, which matters because a truncated JSON would look exactly like
// a writer that vanished.
func Publish(dir string, ep Endpoint) (string, error) {
	if ep.Addr == "" || ep.Token == "" {
		return "", errors.New("writer: endpoint needs an addr and a token")
	}
	path := EndpointPath(dir)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone either way; the rename is the point
	body, err := json.Marshal(ep)
	if err != nil {
		tmp.Close() //nolint:errcheck
		return "", err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close() //nolint:errcheck
		return "", err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close() //nolint:errcheck
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// ReadEndpoint returns the published writer, or ErrNoEndpointPublished.
func ReadEndpoint(dir string) (Endpoint, error) {
	var ep Endpoint
	raw, err := os.ReadFile(EndpointPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return ep, ErrNoEndpointPublished
		}
		return ep, err
	}
	if err := json.Unmarshal(raw, &ep); err != nil {
		// Unparseable is not "no writer": a live writer whose file was
		// corrupted is a different diagnosis, and the caller should not
		// elect itself a second writer because of it.
		return ep, fmt.Errorf("writer endpoint file is unreadable (%s): %w", EndpointPath(dir), err)
	}
	if ep.Addr == "" || ep.Token == "" {
		return ep, fmt.Errorf("writer endpoint file is incomplete: %w", ErrNoEndpointPublished)
	}
	return ep, nil
}

// Unpublish removes the endpoint file, but only if it still describes this
// writer. A writer that is shutting down late must not delete the file a newer
// writer already published — that would take a working channel offline.
func Unpublish(dir, addr string) error {
	ep, err := ReadEndpoint(dir)
	if errors.Is(err, ErrNoEndpointPublished) {
		return nil
	}
	if err != nil {
		return err
	}
	if ep.Addr != addr {
		return nil
	}
	if err := os.Remove(EndpointPath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// NewToken returns a 256-bit hex token.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Request is one forwarded write: a catalogue method name plus its JSON
// arguments, and the surface the call came from, so the writer's logs can say
// which agent asked for the write rather than which process executed it.
type Request struct {
	Method  string          `json:"method"`
	Args    json.RawMessage `json:"args"`
	Surface string          `json:"surface"`
	// ClientRequestID ties a log line on both sides of the hop together
	// (ADR-0055 D4 correlation ids).
	ClientRequestID string `json:"client_request_id,omitempty"`
}

// Response is the writer's answer. On failure Error carries the code the
// WRITER classified the error with, because re-deriving it from the message on
// this side would downgrade a `conflict` to `internal` and lose what makes the
// failure actionable.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ResponseError  `json:"error,omitempty"`
}

// ResponseError is a failed write as it crosses the process boundary.
type ResponseError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	// WriterGone says the writer refused because it is handing off or about to
	// die, so the caller should re-elect rather than report the failure.
	WriterGone bool `json:"writer_gone,omitempty"`
}

// Server serves the write channel on a loopback port. It is started by
// whichever process won the election and stopped when that process releases the
// lock, so "the endpoint file points at a dead port" is always equivalent to
// "the writer is gone".
type Server struct {
	srv      *http.Server
	listener net.Listener
	token    string
	dir      string
}

// ListenSpec names the write-channel endpoint. Kept tiny so the package does
// not need to know about Services or the method catalogue.
type ListenSpec struct {
	Dir       string
	CoreVer   string
	SchemaVer int
	Process   string
	Handler   http.Handler
	// OnFatal fires when the server exits for any reason other than Close.
	// A writer whose channel died is still holding the election lock, so
	// without this it would keep the machine's write path while serving
	// nobody — the caller uses it to demote itself.
	OnFatal func(error)
}

// Serve starts the channel and publishes the endpoint. The caller owns the
// returned Server and must Close it while still holding the election lock.
func Serve(spec ListenSpec) (*Server, error) {
	if spec.Dir == "" || spec.Handler == nil {
		return nil, errors.New("writer: Serve needs a dir and a handler")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("writer: listen on loopback: %w", err)
	}
	token, err := NewToken()
	if err != nil {
		ln.Close() //nolint:errcheck
		return nil, err
	}
	s := &Server{
		listener: ln,
		token:    token,
		dir:      spec.Dir,
	}
	// The token check lives here, not in the handler: the secret belongs to the
	// channel, and a caller that forgets to wrap cannot leave the write path
	// open to every local process.
	s.srv = &http.Server{Handler: s.WithAuth(spec.Handler)}
	addr := ln.Addr().String()
	if _, err := Publish(spec.Dir, Endpoint{
		Addr:          addr,
		Token:         token,
		PID:           os.Getpid(),
		Process:       spec.Process,
		CoreVersion:   spec.CoreVer,
		SchemaVersion: spec.SchemaVer,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		ln.Close() //nolint:errcheck
		return nil, err
	}
	go func() {
		// ErrServerClosed is Close(), which is the normal exit path.
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logChannelError(err)
			if spec.OnFatal != nil {
				spec.OnFatal(err)
			}
		}
	}()
	return s, nil
}

func logChannelError(err error) { // indirection so tests can watch it without a logging harness
	// The channel dying takes every other process's write path with it, so it
	// is never silent: the caller also stops holding the lock (see writerapi).
	fmt.Fprintf(os.Stderr, "cinder writer channel: %v\n", err)
}

// Addr is the loopback address being served.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// ListenerForTest exposes the listener so a test can simulate the abnormal
// exit (closing it makes Serve return a non-ErrServerClosed error). Named for
// what it is: production code must not reach for it; there is no supported
// reason to kill your own channel from outside the Close path.
func (s *Server) ListenerForTest() net.Listener { return s.listener }

// Token is the bearer token clients must present.
func (s *Server) Token() string { return s.token }

// Close stops serving and unpublishes the endpoint.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if ue := Unpublish(s.dir, s.listener.Addr().String()); ue != nil && err == nil {
		err = ue
	}
	return err
}

// WithAuth wraps the method dispatcher with the token check. Exported because
// writerapi builds the handler and this package owns the credential.
func (s *Server) WithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Cinder-Writer-Token")), []byte(s.token)) != 1 {
			// 404, not 401: the port is on a loopback interface that any local
			// process can reach, and a 401 confirms "right port, wrong secret".
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HeaderToken is the request header carrying the endpoint secret.
const HeaderToken = "X-Cinder-Writer-Token"

// dialTimeout bounds the connect attempt, so a writer that died leaves a dead
// port and a fast error rather than a hung agent tool call.
const dialTimeout = 2 * time.Second

// channelClient is shared by every channel call on purpose. Building an
// http.Client (and therefore a Transport) per call is not free: a zero-value
// Transport never expires idle connections and nothing ever closes them, so
// each call left its keep-alive socket open for the life of the process — one
// fd and one ephemeral port per forwarded write, measured 1:1 in a soak. At a
// few writes per second that runs a normal fd limit (or the ephemeral port
// range) out inside a 30-minute run; the next write then fails at the DIAL
// while the writer is perfectly healthy, the re-election probe dials the same
// way and also fails, and the caller gets writer_unavailable. Keep-alive is
// off rather than pooled: this is loopback with a handful of writes per
// second, and "exactly one socket per call, closed with the response" is the
// invariant that cannot silently regress. No Timeout either: a forwarded scan
// or review legitimately runs for minutes, so cancellation rides on ctx.
var channelClient = &http.Client{Transport: &http.Transport{
	DialContext:       (&net.Dialer{Timeout: dialTimeout}).DialContext,
	DisableKeepAlives: true,
}}

// ProbeTimeout bounds Probe. It is short on purpose: the only question is
// "does something answer on this loopback port right now", and a writer busy
// inside a long SQLite write still accepts the TCP connection and answers the
// control path — the busy-ness shows up in the WRITE, not in the socket.
const ProbeTimeout = 1500 * time.Millisecond

// Probe reports whether the published writer is still there.
//
// It exists to keep two failures apart that look identical from an endpoint
// file: "the writer is alive but could not take this write" (a long backup, a
// batch compile — retry and it works) and "the writer is gone, its endpoint
// file outlived it" (re-elect). The endpoint file cannot answer that: it is
// written by the writer and survives a SIGKILL, which is exactly why the lock
// is released by process death but this file is not.
//
// The probe asks the control path, which answers without touching the
// database: a writer stuck on SQLite still answers it.
func Probe(ep Endpoint) bool {
	payload, err := MarshalRequest(Request{Method: "writer.ping"})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ep.Addr+"/control", strings.NewReader(string(payload)))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderToken, ep.Token)
	resp, err := channelClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	// Any HTTP answer proves the process is there. A 404 means the token no
	// longer matches (a different writer took the directory), which is "not
	// this writer" for our purposes, so only 2xx counts.
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// maxChannelBody caps the response. The largest legitimate payload is a review
// result; the 1 MiB ceiling the RPC bridge uses would truncate it, so the
// channel allows more while still refusing an unbounded read.
const maxChannelBody = 8 << 20

// Post sends one encoded request to a published writer and decodes the answer.
// It is the wire half of the channel, so both the write calls and the control
// calls (writerapi) speak it through one implementation: the dial-vs-midflight
// classification, which decides whether a retry is safe, must not be written
// twice.
//
// No overall timeout: a forwarded scan or review legitimately runs for minutes,
// and a client-side ceiling would report a successful write as a failure — the
// exact bug cmd/server documents for its HTTP surface. Cancellation rides on
// ctx, which is the caller's own request context.
func Post(ctx context.Context, ep Endpoint, path string, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ep.Addr+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderToken, ep.Token)
	resp, err := channelClient.Do(req)
	if err != nil {
		return nil, &StaleError{Addr: ep.Addr, PreExecution: isDialFailure(err), Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxChannelBody))
	if err != nil {
		// Headers arrived, so the handler ran (or is running): mid-request.
		return nil, &StaleError{Addr: ep.Addr, Err: err}
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		// The writer is yielding or closing: the caller should re-elect rather
		// than report the failure.
		return &Response{Error: &ResponseError{Code: WriterUnavailableCode, Message: strings.TrimSpace(string(raw)), WriterGone: true}}, nil
	case http.StatusMethodNotAllowed, http.StatusNotFound:
		// 404 is the token-mismatch shape. A version skew can produce it too,
		// which is why it is reported instead of retried into a loop.
		return nil, fmt.Errorf("writer channel rejected the request (%d): this process and the Cinder writer do not speak the same version", resp.StatusCode)
	default:
		return nil, fmt.Errorf("writer channel returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("writer response is unreadable: %w", err)
	}
	return &out, nil
}

// WriterUnavailableCode mirrors errcode.WriterUnavailable. The constant is
// repeated rather than imported so internal/writer keeps no dependency on the
// error-classification package (and errcode stays free of cycles).
const WriterUnavailableCode = "writer_unavailable"

// MarshalRequest encodes a channel request; exported so writerapi builds
// requests without reaching into the wire format.
func MarshalRequest(r Request) ([]byte, error) { return json.Marshal(r) }

// isDialFailure reports whether a transport error happened before the request
// could have executed: a refused/undialable connection means the writer never
// saw the write, which is the condition under which re-electing and retrying
// cannot double-apply anything.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}
