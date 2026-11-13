package writerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"repotory/internal/db"
	"repotory/internal/errcode"
	"repotory/internal/service"
	"repotory/internal/writer"
)

// MethodYield is the control message a desktop sends to an agent process that
// self-elected as writer and is asked to hand the role over. It is deliberately
// not in Methods(): it writes nothing, and listing it beside the write
// operations would blur the boundary the catalogue exists to draw.
const MethodYield = "writer.yield"

// MethodPing is the liveness probe. It exists so "is this writer alive" can be
// asked WITHOUT the side effect the yield carries — answering a yield makes the
// writer drop the role, which is the last thing a client deciding whether to
// re-elect should cause.
const MethodPing = "writer.ping"

// Roles this process can hold with respect to the write path.
const (
	roleClient   = iota // another process writes; we forward to it
	roleWriter          // we hold the election lock and serve the channel
	roleYielding        // we still hold the lock but refuse new writes: a desktop
	// asked for the role, so getting out of the way quickly is correct
)

// Options configures a Manager.
type Options struct {
	// DBPath is the knowledge base this process serves. The election files live
	// beside it, so "same database" and "same writer" are one statement.
	DBPath string
	// Process is the binary name, recorded in the endpoint file so a stale
	// publication can be blamed on something ("cinder-mcp" vs "Cinder Desktop").
	Process string
	// Surface is this process's audit surface (service.AISurface*), used in logs.
	Surface string
	// CoreVersion / SchemaVersion are published for drift logging only.
	CoreVersion   string
	SchemaVersion int
	// SelfElectAllowed says this process may become the writer when nobody else
	// is. The agent surfaces (cmd/mcp, cmd/repotory-capture) set it: that is what
	// keeps "install the MCP server, no desktop app" a working product (README
	// "zero desktop app required"). ADR-0058's default was to refuse writes when
	// the desktop is absent; the user decision of 2026-11-12 replaced that with
	// election, so the standalone promise survives the contract.
	SelfElectAllowed bool
	// AssertWriter is the desktop's flag: on startup it asks a self-elected
	// agent to yield and takes the role, because the desktop is the surface a
	// human is looking at, and it is where the backup / sync / compile tickers
	// that write the most live.
	AssertWriter bool
	// OnLost is invoked if this process is the writer and its channel dies,
	// before the process exits. The entry points wire it to a clean shutdown:
	// continuing to serve READS with no write path is worse than dying, because
	// an agent would see half a working Cinder.
	OnLost func(reason string)
}

// Manager owns this process's write path. One per process: tool handlers call
// Execute and never decide for themselves who runs the write.
type Manager struct {
	opts Options
	exec *Executor

	// mu guards the role state and the channel handle. It is NOT held across a
	// write — a forwarded call, and even a locally executed scan, runs for
	// minutes, and a role check must not queue behind one.
	mu      sync.Mutex
	role    int
	lock    *writer.Lock
	channel *writer.Server

	inflight atomic.Int64
	closing  atomic.Bool

	// yieldOnce guards the OnLost callback so a channel death and a yield
	// cannot both fire it.
	yieldOnce sync.Once
}

// Direct returns a manager that always executes locally and never elects or
// forwards. It exists for processes without a shared database file — the test
// harness over `:memory:` — where "who else writes this file" has no answer.
// Production entry points always use New; using Direct against a real file
// would silently opt out of the contract, which is why the role it reports is
// its own ("direct", never "writer"), so a status readout cannot mistake it for
// a process that actually holds the election.
func Direct(svc *service.Service) *Manager {
	return &Manager{opts: Options{Surface: service.AISurfaceUnknown}, exec: NewExecutor(svc), role: roleDirect}
}

// roleDirect: no election, no channel, local execution only.
const roleDirect = 99

// New elects, and if this process wins, starts serving the channel. It never
// blocks for long: a busy machine must not stall an agent's startup, which is
// why TryLock is non-blocking and the takeover budget is bounded.
func New(svc *service.Service, opts Options) (*Manager, error) {
	if svc == nil {
		return nil, errors.New("writerapi: New needs a Service")
	}
	if opts.DBPath == "" {
		return nil, errors.New("writerapi: New needs a database path")
	}
	if opts.Surface == "" {
		opts.Surface = service.AISurfaceUnknown
	}
	m := &Manager{opts: opts, exec: NewExecutor(svc)}
	if err := m.elect(); err != nil {
		return nil, err
	}
	return m, nil
}

// elect takes the lock, or records that we are a client. A desktop additionally
// tries to preempt an agent writer before settling for that.
func (m *Manager) elect() error {
	dir := writer.DirOf(m.opts.DBPath)
	lock, err := m.tryElect(dir)
	switch {
	case err == nil:
		m.mu.Lock()
		m.lock = lock
		m.mu.Unlock()
		return m.becomeWriter(dir)
	case errors.Is(err, writer.ErrLockedByAnotherProcess):
		if m.opts.AssertWriter && m.preempt(dir) {
			return nil
		}
		m.setRole(roleClient)
		log.Printf("write path: %s forwards its writes to the Cinder writer holding %s (ADR-0058)",
			m.opts.Process, dir)
		return nil
	default:
		// The lock file could not be created: the data directory is unusable,
		// which is the same diagnosis every later DB write would report.
		return fmt.Errorf("writer: take election lock in %s: %w", dir, err)
	}
}

// preempt asks a self-elected agent writer to yield and then retries the lock.
//
// Bounded and best-effort on purpose. If the agent is mid-scan the request is
// refused and the desktop keeps running as a client: the single-writer contract
// still holds, and "a human opened the app, so abandon the agent's long job"
// would be the worse product. A desktop that cannot take over is not a failure
// to report to the user beyond one log line.
func (m *Manager) preempt(dir string) bool {
	for attempt := 0; attempt < 3; attempt++ {
		ep, err := writer.ReadEndpoint(dir)
		if err != nil {
			return false
		}
		if err := m.askYield(ep); err != nil {
			log.Printf("write path: Cinder Desktop asked %s to yield and was refused: %v", ep.Process, err)
		}
		// Wait on the lock, not on the endpoint file: the lock is the truth, and
		// the writer unpublishes before it closes.
		for tick := 0; tick < 25; tick++ {
			lock, err := m.tryElect(dir)
			if err == nil {
				m.mu.Lock()
				m.lock = lock
				m.mu.Unlock()
				if err := m.becomeWriter(dir); err != nil {
					log.Printf("write path: took the writer role but could not serve the channel: %v", err)
					_ = m.lock.Close()
					m.mu.Lock()
					m.lock = nil
					m.mu.Unlock()
					m.setRole(roleClient)
					return false
				}
				log.Printf("write path: Cinder Desktop took the writer role from %s (pid %d)", ep.Process, ep.PID)
				return true
			}
			if !errors.Is(err, writer.ErrLockedByAnotherProcess) {
				return false
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	log.Printf("write path: the current writer is busy, so Cinder Desktop keeps forwarding (ADR-0058)")
	return false
}

func (m *Manager) askYield(ep writer.Endpoint) error {
	payload, err := writer.MarshalRequest(writer.Request{Method: MethodYield})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), yieldAskTimeout)
	defer cancel()
	// The control hop gets its own deadline (unlike a write): it is a
	// courtesy request, and a writer that cannot answer it in three seconds is
	// not going to answer the yield before the desktop gives up anyway.
	resp, err := writer.Post(ctx, ep, "/control", payload)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return errors.New(resp.Error.Message)
	}
	return nil
}

// becomeWriter starts the channel and flips this process into the writer role.
func (m *Manager) becomeWriter(dir string) error {
	srv, err := writer.Serve(writer.ListenSpec{
		Dir:       dir,
		CoreVer:   m.opts.CoreVersion,
		SchemaVer: m.opts.SchemaVersion,
		Process:   m.opts.Process,
		Handler:   http.HandlerFunc(m.handle),
		OnFatal:   m.onChannelFatal,
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.channel = srv
	m.role = roleWriter
	m.mu.Unlock()
	log.Printf("write path: %s is the single writer for %s (channel %s, %d catalogue methods)",
		m.opts.Process, dir, srv.Addr(), len(Methods()))
	return nil
}

// onChannelFatal is the writer's own failure path: the port died but this
// process still holds the election lock, which is the one state where NO ONE
// can write. Releasing the role lets a peer elect itself and restore service;
// staying "writer" with a dead channel would keep reads working while every
// forwarded write fails, which reads to an agent as a half-broken Cinder.
func (m *Manager) onChannelFatal(err error) {
	if m.closing.Load() {
		return // the death is our own Shutdown
	}
	m.stopChannel()
	m.mu.Lock()
	lock := m.lock
	m.lock = nil
	m.role = roleClient
	m.mu.Unlock()
	if lock != nil {
		_ = lock.Close()
	}
	log.Printf("write path: %s released the writer role after its channel failed: %v (a peer will take over)",
		m.opts.Process, err)
	if m.opts.OnLost != nil {
		m.opts.OnLost(err.Error())
	}
}

func (m *Manager) setRole(r int) {
	m.mu.Lock()
	m.role = r
	m.mu.Unlock()
}

// Role reports the current role, for status surfaces and tests.
func (m *Manager) Role() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.role
}

// RoleName renders a role for a status line / log.
func RoleName(role int) string {
	switch role {
	case roleWriter:
		return "writer"
	case roleYielding:
		return "yielding"
	case roleDirect:
		return "direct"
	default:
		return "client"
	}
}

// handle serves /write and /control in the writer process. The token check is
// already done by writer.Server's wrapper; this handler is about the role.
func (m *Manager) handle(w http.ResponseWriter, r *http.Request) {
	var req writer.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&req); err != nil {
		writeResponse(w, &writer.Response{Error: &writer.ResponseError{
			Code: errcode.Internal, Message: "malformed write request: " + err.Error()}})
		return
	}

	if r.URL.Path == "/control" {
		m.handleControl(w, req)
		return
	}

	m.inflight.Add(1)
	// Release exactly once, at return — including the early-refusal branches,
	// which never reach a write but still raced against a draining yield.
	defer m.inflight.Add(-1)
	m.mu.Lock()
	role := m.role
	m.mu.Unlock()
	if m.closing.Load() {
		m.respondGone(w, "the Cinder writer is shutting down")
		return
	}
	if role == roleYielding {
		m.respondGone(w, "Cinder Desktop is taking over the write path; retry this call")
		return
	}
	if !IsMethod(req.Method) {
		writeResponse(w, &writer.Response{Error: &writer.ResponseError{
			Code: errcode.NotFound, Message: fmt.Sprintf("%v: %q", ErrUnknownMethod, req.Method)}})
		return
	}

	// The claim is held through the response write (the outer defer releases
	// at return): a yield that sees inflight==0 must not pull the lock while
	// the client's result is still in flight.
	result, err := m.exec.Execute(r.Context(), req.Method, req.Args)
	if err != nil {
		code, retryable := errcode.Classify(err)
		if errors.Is(err, ErrUnknownMethod) {
			code = errcode.NotFound
		}
		log.Printf("write %s from surface %q failed (%s): %v", req.Method, req.Surface, code, err)
		writeResponse(w, &writer.Response{Error: &writer.ResponseError{
			Code: code, Message: err.Error(), Retryable: retryable}})
		return
	}
	writeResponse(w, &writer.Response{OK: true, Result: marshalResult(result)})
}

// handleControl answers the takeover request. Yielding is only ever honoured
// for an agent process: a desktop never gives the role up, because then two
// surfaces could each believe they own the write path in turn, which is the
// thing this whole package is preventing.
func (m *Manager) handleControl(w http.ResponseWriter, req writer.Request) {
	if req.Method == MethodPing {
		// Liveness only: no database, no role change.
		writeResponse(w, &writer.Response{OK: true})
		return
	}
	if req.Method != MethodYield {
		writeResponse(w, &writer.Response{Error: &writer.ResponseError{
			Code: errcode.NotFound, Message: "unknown control method " + req.Method}})
		return
	}
	if !m.opts.SelfElectAllowed {
		writeResponse(w, &writer.Response{Error: &writer.ResponseError{
			Code: errcode.Conflict, Message: "the desktop is the writer and does not yield the role"}})
		return
	}
	m.mu.Lock()
	if m.role == roleWriter {
		m.role = roleYielding
	}
	m.mu.Unlock()
	log.Printf("write path: %s is yielding the writer role to Cinder Desktop (ADR-0058)", m.opts.Process)
	writeResponse(w, &writer.Response{OK: true})
	// Release asynchronously: the response has to reach the requester first,
	// and Shutdown would unpublish the endpoint we still need to answer on.
	go m.releaseAfterYield()
}

// releaseAfterYield waits for in-flight writes to land, then drops the lock and
// the endpoint so the desktop can win the election. The process keeps serving
// reads, and its NEXT write re-elects (or forwards) exactly like a cold start.
func (m *Manager) releaseAfterYield() {
	deadline := time.Now().Add(yieldDrainBudget)
	for m.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if m.inflight.Load() > 0 {
		// Still holding a write after two minutes means it is a long job. Stop
		// the hand-off rather than yank the lock out from under it: the desktop
		// keeps forwarding, which is a supported state.
		m.mu.Lock()
		if m.role == roleYielding {
			m.role = roleWriter
		}
		m.mu.Unlock()
		log.Printf("write path: yield aborted, %d write(s) still in flight after %s",
			m.inflight.Load(), yieldDrainBudget)
		return
	}
	m.stopChannel()
	m.mu.Lock()
	lock := m.lock
	m.lock = nil
	m.mu.Unlock()
	if lock != nil {
		_ = lock.Close()
	}
	m.setRole(roleClient)
}

// stopChannel closes the HTTP server and unpublishes. Idempotent.
func (m *Manager) stopChannel() {
	m.mu.Lock()
	srv := m.channel
	m.channel = nil
	m.mu.Unlock()
	if srv == nil {
		return
	}
	if err := srv.Close(); err != nil {
		log.Printf("write path: close writer channel: %v", err)
	}
}

// respondGone answers with the code that tells a client "the writer changed",
// which is what makes it re-elect instead of reporting a failure to the agent.
func (m *Manager) respondGone(w http.ResponseWriter, message string) {
	writeResponse(w, &writer.Response{Error: &writer.ResponseError{
		Code: errcode.WriterUnavailable, Message: message, Retryable: true, WriterGone: true}})
}

// Execute runs a catalogue method through the contract: locally if this process
// is the writer, otherwise forwarded to whichever process is. Callers get back
// the result value or an error carrying the writer's own classification.
func (m *Manager) Execute(ctx context.Context, method string, args any) (any, error) {
	if !IsMethod(method) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMethod, method)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("writer: unmarshalable %s arguments: %w", method, err)
	}
	m.mu.Lock()
	role := m.role
	m.mu.Unlock()

	if role == roleWriter || role == roleDirect {
		m.inflight.Add(1)
		defer m.inflight.Add(-1)
		out, err := m.exec.Execute(ctx, method, raw)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	out, err := m.forward(ctx, method, raw)
	if errors.Is(err, errBusyWriter) {
		// The contract's own classification, not the generic one: "busy, retry"
		// is actionable, where a bare `internal` is not.
		return nil, &errcode.Remote{Code: errcode.Retryable, Message: err.Error(), Retryable: true}
	}
	return out, err
}

// ExecuteInto is Execute plus the decode into a typed result, so a tool handler
// writes one line instead of three. out must be a pointer.
func (m *Manager) ExecuteInto(ctx context.Context, method string, args any, out any) error {
	raw, err := m.Execute(ctx, method, args)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// The local-writer path returns the Go value, the forwarded path returns
	// json.RawMessage. Round-tripping through JSON is the honest way to give
	// both the same shape: the tool handlers then cannot tell which mode ran,
	// which is exactly what "one write path" is supposed to mean.
	body, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("writer: encode %s result: %w", method, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("writer: decode %s result: %w", method, err)
	}
	return nil
}

// maxForwardAttempts bounds re-election. Two is enough: the first call fails
// because the published writer died, the second runs after we either won the
// lock ourselves or found the replacement's endpoint. A larger count buys
// nothing — every remaining case is a machine where no writer can start.
const maxForwardAttempts = 2

// forward sends the write to the published writer, re-electing once if that
// writer turns out to be gone.
func (m *Manager) forward(ctx context.Context, method string, args json.RawMessage) (any, error) {
	var lastErr error
	for attempt := 1; attempt <= maxForwardAttempts; attempt++ {
		res, err := m.forwardOnce(ctx, method, args)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !m.isWriterGone(err) {
			// A real answer from a live writer (validation, conflict, a storage
			// failure): re-sending it would be a second attempt at a write that
			// was refused on purpose, or one that may already have landed.
			return nil, err
		}
		if attempt == maxForwardAttempts {
			break
		}
		if rerr := m.reelect(endpointAddrOf(err)); rerr != nil {
			return nil, rerr
		}
		// If re-election made us the writer, run it here instead of forwarding
		// to a channel we just published for ourselves.
		if m.Role() == roleWriter {
			return m.exec.Execute(ctx, method, args)
		}
	}
	// The writer stayed unreachable through re-election. Report the contract
	// failure, not the socket error: "connection refused" describes a port,
	// writer_unavailable describes the situation the caller can act on.
	return nil, fmt.Errorf("%w (last attempt: %v)", errcode.ErrWriterUnavailable, lastErr)
}

// endpointAddrOf extracts the failed writer's address so re-election can
// ignore its stale publication and wait for a genuinely different one.
func endpointAddrOf(err error) string {
	var stale *writer.StaleError
	if errors.As(err, &stale) {
		return stale.Addr
	}
	return ""
}

func (m *Manager) forwardOnce(ctx context.Context, method string, args json.RawMessage) (any, error) {
	dir := writer.DirOf(m.opts.DBPath)
	ep, err := writer.ReadEndpoint(dir)
	if errors.Is(err, writer.ErrNoEndpointPublished) {
		// Lock held by someone who never published, or a crash between the two.
		// Treat it as writer-gone: re-election distinguishes them.
		return nil, fmt.Errorf("%w: %v", errWriterGone, err)
	}
	if err != nil {
		return nil, err
	}
	payload, err := writer.MarshalRequest(writer.Request{Method: method, Args: args, Surface: m.opts.Surface})
	if err != nil {
		return nil, err
	}
	parsed, err := writer.Post(ctx, ep, "/write", payload)
	if err != nil {
		// Post classifies the failure: a StaleError with PreExecution means the
		// request never reached the writer (retry-safe), anything else does not
		// claim that.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("write %s cancelled: %w", method, ctx.Err())
		}
		return nil, err
	}
	if parsed.Error != nil {
		if parsed.Error.WriterGone {
			return nil, fmt.Errorf("%w: %s", errWriterGone, parsed.Error.Message)
		}
		return nil, &errcode.Remote{
			Code: parsed.Error.Code, Message: parsed.Error.Message, Retryable: parsed.Error.Retryable,
		}
	}
	if len(parsed.Result) == 0 {
		return nil, nil
	}
	return json.RawMessage(parsed.Result), nil
}

// errBusyWriter is the internal signal "the writer is alive and serving, but
// it could not take this write right now" (a long backup, a batch compile).
// Deliberately distinct from errWriterUnavailable: one says "wait", the other
// says "somebody else must do this".
var errBusyWriter = errors.New("the write path is busy")

// errWriterGone is the internal signal "the writer we targeted is not there any
// more", which is the only condition that justifies re-election.
var errWriterGone = errors.New("cinder writer is gone")

func (m *Manager) isWriterGone(err error) bool {
	if errors.Is(err, errWriterGone) {
		return true
	}
	var stale *writer.StaleError
	// Only a PRE-execution failure may trigger re-election + retry: if the
	// writer died after receiving the write, replaying it could apply a
	// non-idempotent operation twice. Reporting the ambiguity is the honest
	// answer (ADR-0058's "replay or explicitly refuse, never silently drop").
	return errors.As(err, &stale) && stale.PreExecution
}

// reelect runs the election again after the writer disappeared. An agent may
// take over (that is the standalone path); a process whose SelfElectAllowed is
// false only ever waits for a DIFFERENT writer to appear — the ghost endpoint
// file it just failed to reach must not count as one, or a writer frozen
// between locking and serving would hang every caller on it forever.
func (m *Manager) reelect(failedAddr string) error {
	dir := writer.DirOf(m.opts.DBPath)
	// tryElect skips when this process already holds the lock: flock is per
	// open file description, so a second fd in the SAME process would win
	// against itself and publish a duplicate channel beside the real holder
	// (the yielding-writer case below, or a channel restart).
	lock, err := m.tryElect(dir)
	if err == nil {
		m.mu.Lock()
		m.lock = lock
		m.mu.Unlock()
		if err := m.becomeWriter(dir); err != nil {
			_ = m.lock.Close()
			m.mu.Lock()
			m.lock = nil
			m.mu.Unlock()
			return fmt.Errorf("writer: took over but could not serve the channel: %w", err)
		}
		log.Printf("write path: %s took over the writer role after the previous writer disappeared (ADR-0058)",
			m.opts.Process)
		return nil
	}
	if !errors.Is(err, writer.ErrLockedByAnotherProcess) {
		return fmt.Errorf("writer: re-election failed: %w", err)
	}
	// The lock is STILL held by another process, and that holder may be the very
	// writer we just failed against — which is what a writer busy inside a long
	// write looks like from here. Waiting for a DIFFERENT endpoint is only
	// right when the old one is actually gone; waiting three seconds because the
	// address did not change turned a slow backup into "no Cinder writer is
	// reachable" (measured: 1108 such false reports in one 30-minute soak,
	// every one of them ~3s, i.e. this loop timing out).
	if ep, rerr := writer.ReadEndpoint(dir); rerr == nil && ep.Addr == failedAddr && writer.Probe(ep) {
		// Same writer, still published, AND still answering: it is alive but
		// could not take this write. The liveness probe matters — an endpoint
		// file outlives the process that wrote it, so "the address did not
		// change" alone would report a dead writer as busy. Report that, not
		// "writer_unavailable" — that code means "no process is holding the
		// lock", and claiming it while the holder is serving is a lie the agent
		// acts on by starting a second writer.
		//
		// It is retryable in the CONTENTION sense (a long backup, a batch
		// compile), which is exactly what errcode.Retryable says: wait and try
		// the same write again.
		return fmt.Errorf("%w: the write path is held by %s, which is busy on a long write — retry",
			errBusyWriter, ep.Addr)
	}
	// Someone else holds it and may not have published yet (it is starting up).
	// A short bounded wait is the difference between "retry works" and "the
	// agent sees a failure for a writer that was 200ms from being ready".
	for tick := 0; tick < 15; tick++ {
		time.Sleep(200 * time.Millisecond)
		if ep, err := writer.ReadEndpoint(dir); err == nil && ep.Addr != failedAddr {
			return nil
		}
		if lock, err := m.tryElect(dir); err == nil {
			m.mu.Lock()
			m.lock = lock
			m.mu.Unlock()
			return m.becomeWriter(dir)
		}
	}
	return fmt.Errorf("%w: no Cinder process is serving the write path for %s — start Cinder Desktop, or retry: another Cinder will take it over",
		errcode.ErrWriterUnavailable, dir)
}

// tryElect takes the lock only if this process is allowed to become the
// writer; otherwise the lock simply stays with whoever holds it.
func (m *Manager) tryElect(dir string) (*writer.Lock, error) {
	if !m.opts.SelfElectAllowed {
		return nil, writer.ErrLockedByAnotherProcess
	}
	if m.holdsLock() {
		return nil, writer.ErrLockedByAnotherProcess
	}
	return writer.TryLock(dir)
}

func (m *Manager) holdsLock() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lock != nil && m.lock.Held()
}

// Shutdown releases the role. Called from each entry point's cleanup, before the
// database handle closes, so a peer never forwards a write to a process that is
// about to close its DB.
func (m *Manager) Shutdown() {
	if !m.closing.CompareAndSwap(false, true) {
		return
	}
	// Unpublish first: while this process is still reachable it can finish what
	// it accepted. Then drain, then close, then release the lock.
	m.stopChannelNotice()
	deadline := time.Now().Add(shutdownDrainBudget)
	for m.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := m.inflight.Load(); n > 0 {
		log.Printf("write path: shutting down with %d write(s) still in flight after %s", n, shutdownDrainBudget)
	}
	m.stopChannel()
	m.mu.Lock()
	lock := m.lock
	m.lock = nil
	m.mu.Unlock()
	if lock != nil {
		if err := lock.Close(); err != nil {
			log.Printf("write path: release election lock: %v", err)
		}
	}
	m.setRole(roleClient)
}

// stopChannelNotice points the endpoint file at nothing while the port still
// answers, so a peer that starts a call now gets writer_gone instead of a
// half-executed write. Implemented by unpublishing directly: the running server
// still serves requests already in flight.
func (m *Manager) stopChannelNotice() {
	if err := writer.Unpublish(writer.DirOf(m.opts.DBPath), m.addr()); err != nil {
		log.Printf("write path: unpublish endpoint: %v", err)
	}
}

func (m *Manager) addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.channel == nil {
		return ""
	}
	return m.channel.Addr()
}

// IsWriter reports whether this process currently owns the write path. Used by
// the agent startup paths, which must only run their own incidental writes
// (seeding a config row) when they actually hold the lock.
func (m *Manager) IsWriter() bool { return m.Role() == roleWriter }

// Status is the small surface each entry point logs and /health echoes, so
// "who is writing right now" is answerable without reading files by hand.
type Status struct {
	Role    string `json:"role"`
	Writer  string `json:"writer_process,omitempty"`
	Addr    string `json:"writer_addr,omitempty"`
	PID     int    `json:"writer_pid,omitempty"`
	Methods int    `json:"catalogue_methods"`
}

// Status reports this process's role and, for a client, who it forwards to.
func (m *Manager) Status() Status {
	st := Status{Role: RoleName(m.Role()), Methods: len(Methods())}
	if m.Role() == roleWriter {
		st.Addr = m.addr()
		st.PID = os.Getpid()
		st.Writer = m.opts.Process
		return st
	}
	if ep, err := writer.ReadEndpoint(writer.DirOf(m.opts.DBPath)); err == nil {
		st.Addr, st.PID, st.Writer = ep.Addr, ep.PID, ep.Process
	}
	return st
}

// WriteDecision is what the RPC bridge does with a write classified by
// bindingWrites (internal/app). The single-writer contract has three answers,
// because three situations exist:
type WriteDecision int

const (
	// WriteLocal: this process is the writer (or in direct mode) — execute as
	// today, on the local handle.
	WriteLocal WriteDecision = iota
	// WriteForward: the op is catalogue-covered — send it to the elected
	// writer over the channel (the same Executor the writer runs, so both
	// modes share one code path).
	WriteForward
	// WriteRefuse: the op is a human-gate write with no catalogue method —
	// the desktop owns these, and a headless process that is not the writer
	// must not hit the shared file behind it. The error names the remedy.
	WriteRefuse
)

// DecideWrite maps an App binding name to its contract behaviour. bindingName
// is the exact method name from internal/app's bindingWrites registry.
func (m *Manager) DecideWrite(bindingName string) WriteDecision {
	m.mu.Lock()
	role := m.role
	m.mu.Unlock()
	if role == roleWriter || role == roleDirect {
		return WriteLocal
	}
	if CatalogueMethodForBinding(bindingName) != "" {
		return WriteForward
	}
	return WriteRefuse
}

// GuardedLocal runs a NON-catalogue write after confirming this process holds
// the write path. The RPC bridge calls it for the human-gate bindings
// (approve / merge / compile / config …): those have no catalogue method
// because they are desktop acts, and executing one from a client process
// would hit the shared SQLite file behind the elected writer's back.
func (m *Manager) GuardedLocal(bindingName string) error {
	m.mu.Lock()
	role := m.role
	m.mu.Unlock()
	if role == roleWriter || role == roleDirect {
		return nil
	}
	return fmt.Errorf("%w: %s writes only from the Cinder writer — start Cinder Desktop (it owns the human gate), or retry once no other Cinder holds the write path",
		errcode.ErrWriterUnavailable, bindingName)
}

// RecordAudit forwards an AI-call audit row. It is a write like any other: on a
// non-writer process the direct insert would hit the same lock contention the
// contract exists to remove, and silently dropping the row would leave the
// audit trail missing exactly the calls made by agents.
func (m *Manager) RecordAudit(row db.AICallLog) error {
	return m.ExecuteInto(context.Background(), MethodAuditRecord, AuditRecordArgs{Row: row}, nil)
}

func writeResponse(w http.ResponseWriter, resp *writer.Response) {
	body, err := json.Marshal(resp)
	if err != nil {
		// A marshal failure means the result type is unserialisable, which is a
		// bug in this file, not a client error. Say so in the status.
		http.Error(w, "writer: encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func marshalResult(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Returning the error instead of panicking: an unserialisable result is
		// a bug, but it must not take the writer process down mid-session.
		body, _ := json.Marshal(map[string]string{"error": "writer: encode result: " + err.Error()})
		return body
	}
	return b
}

const (
	// maxRequestBody caps the decoded request. A handoff note is bounded far
	// below this by db.MaxNoteContentLen, so anything larger is a local process
	// trying to OOM the writer through its own loopback port — the same reasoning
	// as the RPC bridge's cap.
	maxRequestBody = 8 << 20
	// yieldDrainBudget and shutdownDrainBudget are the two waits that make the
	// hand-off safe. Both are bounded so a stuck write cannot hold the machine
	// in a state where no one can write.
	yieldDrainBudget    = 2 * time.Minute
	shutdownDrainBudget = 10 * time.Second
	// yieldAskTimeout bounds only the request to yield, not the hand-off itself:
	// a writer that cannot answer a control ping in three seconds is busy, and
	// preempt already tolerates a refusal.
	yieldAskTimeout = 3 * time.Second
)

// TryLockForTest re-exports the election lock so another package's tests can
// simulate "some other process holds the write path" against the same file.
// Production code goes through New / Manager methods only.
func TryLockForTest(dir string) (*writer.Lock, error) { return writer.TryLock(dir) }
