package process

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxQueue = 8

type Record struct {
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	// Name is the session's short name ("<workspace directory>-k7"), the address
	// `suga send --session <name>` accepts; empty in records of older versions,
	// which stored the first message as the name.
	Name      string    `json:"name"`
	CWD       string    `json:"cwd"`
	Kind      string    `json:"kind"`
	Socket    string    `json:"socket"`
	StartedAt time.Time `json:"started_at"`
	// ProcStart is the OS start time of PID; it tells a reused PID apart from this process.
	ProcStart string `json:"proc_start,omitempty"`
	Host      string `json:"host"`
	State     string `json:"state"`
	// Features lists what this process accepts beyond plain text (FeatureImages
	// for `suga send --image`). A record written by an older version has no
	// field, and an older receiver would silently drop an images request, so a
	// sender checks the feature before it sends anything (design send --image D2).
	Features []string `json:"features,omitempty"`
}

// FeatureImages is the Record.Features entry of a process that accepts
// Request.Images (`suga send --image`).
const FeatureImages = "images"

// FeatureHops is the Record.Features entry of a process whose chat understands
// Request.Hops: it holds a message that arrived at the automatic-reply limit
// instead of starting a turn. A record without it belongs to an older version,
// which ignores the field and starts a turn every time (design session_message
// "兼容").
const FeatureHops = "hops"

// SessionFromPrefix starts Request.From of a message one suga session sends to
// another ("session:<full session ID>"): the receiver shows it as a message of a
// peer session, not as a person's instruction.
const SessionFromPrefix = "session:"

// HasFeature reports whether the record declares feature in its Features.
func (r Record) HasFeature(feature string) bool {
	for _, have := range r.Features {
		if have == feature {
			return true
		}
	}
	return false
}

// Delivery modes of a Request (Request.Mode).
const (
	// ModeFollowUp queues the message for the turn after the one running right
	// now; a client that sends no mode at all asks for this.
	ModeFollowUp = "follow_up"
	// ModeSteer asks the server to insert the message into the turn running right
	// now, and to queue it for the next turn when there is none (design
	// "send：--steer").
	ModeSteer = "steer"
)

// ImageRef names one image `suga send --image` delivers: the sender writes the
// file into the target session's images directory and the request carries the
// reference only (the request body stays far below the 1 MiB limit, design D1).
// The receiver checks the file and its sha256 before the picture enters the
// message.
type ImageRef struct {
	// Ref is the part reference below the session directory, images/<sha256>.<ext>.
	Ref       string `json:"ref"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// States of one message in its receiving process (Response.State and the status
// query; design session-message-delivery D2/D3).
const (
	// StateQueued is a message waiting for the next turn of the receiver.
	StateQueued = "queued"
	// StateSteered is a message inserted into the turn running right now.
	StateSteered = "steered"
	// StateProcessed is a message whose turn completed: it ran exactly once.
	StateProcessed = "processed"
	// StateFailed is a message whose turn failed or was cancelled: it did not run
	// and is not retried automatically (design D2).
	StateFailed = "failed"
	// StateHeld is a message a failed turn moved to the person's next turn.
	StateHeld = "held"
	// StateDiscarded is a message the receiver dropped at exit: it never ran.
	StateDiscarded = "discarded"
)

// TypeStatus is the Request.Type that asks for the state of one message ID
// (`session_message action=status`, design session-message-delivery D3).
const TypeStatus = "status"

// Request is one message or one status query a sender hands to a receiver.
type Request struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Text      string `json:"text,omitempty"`
	From      string `json:"from,omitempty"`
	// FromName is the short name of the sender when From names a session
	// ("session:<id>"): the receiver shows it instead of the raw id. An older
	// server ignores the field.
	FromName string `json:"from_name,omitempty"`
	// Hops is how many automatic replies this message is away from the person who
	// started the exchange: 0 for a message typed at a keyboard or sent by `suga
	// send` on the command line, h+1 for a message a session sent during a turn
	// that a hops-h message started (design session_message D3). An older server
	// ignores it; its Record.Features then lacks FeatureHops.
	Hops int `json:"hops,omitempty"`
	// Wait asks the server to keep the connection open after a "queued"/"steered" acknowledgement and
	// send a second Response once the message is delivered or starts a turn (OK) or is discarded (Error).
	Wait bool `json:"wait,omitempty"`
	// Mode is how the message is delivered: ModeSteer inserts it into the turn running
	// right now, anything else (an absent field included, the shape every older
	// client sends) queues it for the next turn.
	Mode string `json:"mode,omitempty"`
	// Images are the pictures delivered with the text, as references only. An
	// older server ignores the unknown field, which the sender's feature check
	// prevents (Record.Features).
	Images []ImageRef `json:"images,omitempty"`
	// ID is the sender's message ID: a message whose ID the receiver already took
	// is not queued a second time, and the ID names the message in a receipt and in
	// a status query (design session-message-delivery D2). An older server ignores
	// the unknown field, so the message is delivered as before.
	ID string `json:"id,omitempty"`
	// WaitProcessed asks for the receipt of the message's terminal state (processed,
	// failed, held or discarded) instead of the one Wait reports, which is sent when
	// the message starts a turn (design D4: the semantics of Wait do not change).
	WaitProcessed bool `json:"wait_processed,omitempty"`
}

type Response struct {
	OK bool `json:"ok"`
	// Queued and Steered are the acknowledgement of an accepted message: it waits
	// for the next turn, or it went into the turn running right now (an older
	// server never sets Steered).
	Queued  bool   `json:"queued,omitempty"`
	Steered bool   `json:"steered,omitempty"`
	Error   string `json:"error,omitempty"`
	// Warning reports something the receiver did to accept the message that the
	// sender should show (the model cannot see images, so they became a note).
	Warning string `json:"warning,omitempty"`
	// MessageID echoes Request.ID, and State says what happened to that message:
	// queued or steered in the first answer, processed, failed, held or discarded in
	// the second. Both stay empty for a request that names no ID, so the answers of
	// an older client are byte-identical to the ones this version always sent.
	MessageID string `json:"message_id,omitempty"`
	State     string `json:"state,omitempty"`
}

// Delivery is the acknowledgement of one accepted message: exactly one of Queued
// (it waits for its own turn, the default) and Steered (it went into the turn
// running right now) is set. A sender may wait on both.
type Delivery struct {
	Queued  bool
	Steered bool
	// Warning is what the receiver did to accept the message that the sender
	// should show (an empty one says nothing).
	Warning string
}

// DeliverFunc hands one message to the process the server belongs to. mode is the
// Request.Mode of the sender (ModeFollowUp when it sent none).
type DeliverFunc func(mode, text, from string) (Delivery, error)

// DeliverImagesFunc is a DeliverWaitFunc that also receives the request's
// FromName, Hops and images (Record.Features carrying FeatureImages /
// FeatureHops). A server started without one answers images requests with
// "images_unsupported", so an old receiver never takes the text and drops the
// pictures.
type DeliverImagesFunc func(mode, text, from, fromName string, hops int, images []ImageRef) (Delivery, <-chan error, error)

// DeliverRequest is one incoming request as a receiver that knows message IDs sees
// it: the fields of Request a chat reads, including the sender's ID and which
// receipt it asked for (design session-message-delivery D1).
type DeliverRequest struct {
	Mode, Text, From, FromName string
	Hops                       int
	Images                     []ImageRef
	// ID and WaitProcessed decide which receipt the receiver sends: the ID is
	// recorded in the state table, and WaitProcessed defers the receipt to the end
	// of the message's turn.
	ID            string
	WaitProcessed bool
}

// DeliverMessageFunc is what a receiver that knows message IDs registers: it takes
// the whole DeliverRequest and reports the final fate of an accepted message on the
// settled channel (nil processed, ErrFailed, ErrHeld, ErrDiscarded).
type DeliverMessageFunc func(req DeliverRequest) (Delivery, <-chan error, error)

// StatusFunc answers Request.Type == TypeStatus: the state of one message ID in
// this process, ok=false for an ID it never saw (a restart, or a message of
// another process; design session-message-delivery D3).
type StatusFunc func(id string) (state string, ok bool)

// DeliverWaitFunc is a DeliverFunc that can also report the final fate of an accepted message:
// settled (may be nil) yields nil once the message is delivered or starts a turn (OK), or an
// error if it is discarded before it ran.
type DeliverWaitFunc func(mode, text, from string) (Delivery, <-chan error, error)

// Error codes of the final Response sent to a waiting sender whose message was not run.
const (
	codeDiscarded     = "discarded"
	codeSessionClosed = "session_closed"
)

// ErrSessionClosed is returned by a deliver func that no longer accepts messages (chat is exiting).
var ErrSessionClosed = errors.New("session is closing")

// ErrDiscarded is the error a deliver func reports on a settled channel for a message dropped at exit.
var ErrDiscarded = errors.New("message discarded before it ran")

// ErrFailed is the error a deliver func reports on a settled channel for a message
// whose turn failed or was cancelled: it did not run and is not retried (design
// session-message-delivery D2).
var ErrFailed = errors.New("the turn carrying the message failed")

// ErrHeld is the error a deliver func reports on a settled channel for a message a
// failed turn moved to the person's next turn (StateHeld): it runs there once the
// person writes, never on its own.
var ErrHeld = errors.New("message held for the person's next turn")

// codeUnknownMessage is the status answer of a receiver that never saw the ID.
const codeUnknownMessage = "unknown_message"

// ErrUnknownMessage is what Status reports for an ID the receiving process does
// not know: the message belongs to another process, or its chat restarted.
var ErrUnknownMessage = errors.New("message ID unknown to the receiving process")

type Server struct {
	root     string
	mu       sync.RWMutex
	record   Record
	listener net.Listener
	done     chan struct{}
	closing  chan struct{} // closed by Close so connections waiting on a queued message end promptly
	closeOne sync.Once
	// messages and status belong to a receiver that knows message IDs
	// (StartMessages); both are set before serve starts and never change after.
	messages DeliverMessageFunc
	status   StatusFunc
}

func Start(root, sessionID, name, cwd string, deliver DeliverFunc) (*Server, error) {
	return StartWait(root, sessionID, name, cwd, func(mode, text, from string) (Delivery, <-chan error, error) {
		delivery, err := deliver(mode, text, from)
		return delivery, nil, err
	})
}

// StartFeatures is Start with the Record.Features the process declares: the
// features a sender checks before it relies on them (FeatureImages).
func StartFeatures(root, sessionID, name, cwd string, features []string, deliver DeliverFunc) (*Server, error) {
	return startServer(root, sessionID, name, cwd, features, nil, func(mode, text, from string) (Delivery, <-chan error, error) {
		delivery, err := deliver(mode, text, from)
		return delivery, nil, err
	}, nil, nil)
}

// StartMessages is the receiver of a process that knows message IDs: deliver takes
// the whole DeliverRequest (so the ID and the receipt choice reach the chat) and
// status answers Request.Type == TypeStatus for this process (design
// session-message-delivery D1/D3).
func StartMessages(root, sessionID, name, cwd string, features []string, deliver DeliverMessageFunc, status StatusFunc) (*Server, error) {
	return startServer(root, sessionID, name, cwd, features, nil, nil, deliver, status)
}

// StartWaitImages is StartWait with the Record.Features the process declares and a
// deliver func that also takes the request's images (`suga send --image`).
func StartWaitImages(root, sessionID, name, cwd string, features []string, deliver DeliverImagesFunc) (*Server, error) {
	return startServer(root, sessionID, name, cwd, features, deliver, nil, nil, nil)
}

// SecureDir restricts an existing directory to the current user: 0700 on
// Unix, a protected inheritable DACL (current user + SYSTEM) on Windows, so
// files created inside later get the same restriction.
func SecureDir(dir string) error { return secureDir(dir) }

// preferredSocketPath is root/<pid>.sock, the only location a short SUGA_HOME
// needs and the location every record this version and before wrote.
func preferredSocketPath(root string, pid int) string {
	return filepath.Join(root, fmt.Sprintf("%d.sock", pid))
}

// socketHash is the first 12 hex chars of the SHA-256 of the cleaned root. It
// names a SUGA_HOME inside the shared fallback directory, so two different
// homes never collide even when their PIDs happen to match.
func socketHash(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:])[:12]
}

// fallbackSocketPath computes the per-user fallback location of the socket,
// <fallback dir>/<hash>-<pid>.sock, without touching the filesystem; whether
// the fallback directory itself is safe to use is enforceFallbackDir's job.
func fallbackSocketPath(root string, pid int) string {
	return filepath.Join(fallbackDirPath(), fmt.Sprintf("%s-%d.sock", socketHash(root), pid))
}

// sessionSocketPath picks where the socket lives: the run root, or the per-user
// fallback directory when the preferred path is too long for AF_UNIX. It keeps
// the original length error and annotates it with the fallback's own failure so
// chat still prints one actionable message.
func sessionSocketPath(root string, pid int) (string, error) {
	preferred := preferredSocketPath(root, pid)
	err := validateSocketPath(preferred)
	if err == nil {
		return preferred, nil
	}
	fallback := fallbackSocketPath(root, pid)
	if dirErr := ensureFallbackDir(); dirErr != nil {
		return "", fmt.Errorf("%w (the per-user fallback directory is not usable: %v)", err, dirErr)
	}
	if lenErr := validateSocketPath(fallback); lenErr != nil {
		return "", fmt.Errorf("%w (the fallback path %s is too long as well: %v)", err, fallback, lenErr)
	}
	return fallback, nil
}

// trustedSocketPaths lists the socket paths a record may claim: the preferred
// run-root path, or the fallback path computed with the exact algorithm Start
// used. Any other path is rejected, so a forged record can never point the
// send/ps machinery (or dead-record cleanup) at an arbitrary file.
func trustedSocketPaths(root string, pid int) []string {
	return []string{preferredSocketPath(root, pid), fallbackSocketPath(root, pid)}
}

// trustedSocket reports whether a recorded socket path is one a record of this
// root and PID may name (see trustedSocketPaths).
func trustedSocket(socket, root string, pid int) bool {
	for _, trusted := range trustedSocketPaths(root, pid) {
		if filepath.Clean(socket) == filepath.Clean(trusted) {
			return true
		}
	}
	return false
}

func StartWait(root, sessionID, name, cwd string, deliver DeliverWaitFunc) (*Server, error) {
	return startServer(root, sessionID, name, cwd, nil, nil, deliver, nil, nil)
}

// startServer is what every Start variant runs: the record declares features,
// and either deliverImages (a receiver that takes pictures) or deliver (a text
// only one) handles a request.
func startServer(root, sessionID, name, cwd string, features []string, deliverImages DeliverImagesFunc, deliver DeliverWaitFunc, messages DeliverMessageFunc, status StatusFunc) (*Server, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("process registration requires root and session ID")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// Restrict the directory to the current user (0700 on Unix, a protected DACL on Windows).
	if err := secureDir(root); err != nil {
		return nil, fmt.Errorf("secure %s: %w", root, err)
	}
	pid := os.Getpid()
	socket, err := sessionSocketPath(root, pid)
	if err != nil {
		return nil, err
	}
	procStart := processStartID(pid)
	recordPath := filepath.Join(root, fmt.Sprintf("%d.json", pid))
	if data, err := os.ReadFile(recordPath); err == nil {
		var existing Record
		// Same PID and same start time is this very process; a different start time
		// is a crashed earlier owner of a now-reused PID.
		if json.Unmarshal(data, &existing) == nil && existing.PID == pid && existing.Host != "" && (procStart == "" || existing.ProcStart == procStart) {
			return nil, fmt.Errorf("this process already owns a chat registration")
		}
		_ = os.Remove(recordPath)
	}
	// Our own PID's socket can only be left behind by a dead owner (or one that died
	// between listening and registering), so it is always safe to remove here.
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen on local session socket: %w", err)
	}
	_ = os.Chmod(socket, 0o600)
	host, _ := os.Hostname()
	record := Record{PID: pid, SessionID: sessionID, Name: name, CWD: cwd, Kind: "chat", Socket: socket, StartedAt: time.Now().UTC(), ProcStart: procStart, Host: host, State: "idle", Features: features}
	server := &Server{root: root, record: record, listener: listener, done: make(chan struct{}), closing: make(chan struct{}), messages: messages, status: status}
	if err := server.writeRecord(); err != nil {
		_ = listener.Close()
		_ = os.Remove(socket)
		return nil, err
	}
	go server.serve(deliver, deliverImages)
	return server, nil
}

func (s *Server) Record() Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.record
}

func (s *Server) SetBusy(busy bool) error {
	s.mu.Lock()
	if busy {
		s.record.State = "busy"
	} else {
		s.record.State = "idle"
	}
	s.mu.Unlock()
	return s.persistRecord()
}

func (s *Server) persistRecord() error {
	copy := s.Record()
	data, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.root, fmt.Sprintf("%d.json.tmp", copy.PID))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.root, fmt.Sprintf("%d.json", copy.PID)))
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.closeOne.Do(func() { close(s.closing) })
	err := s.listener.Close()
	<-s.done
	_ = os.Remove(filepath.Join(s.root, fmt.Sprintf("%d.json", s.record.PID)))
	_ = os.Remove(s.record.Socket)
	return err
}

func (s *Server) writeRecord() error {
	data, err := json.Marshal(s.record)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.root, fmt.Sprintf("%d.json.tmp", s.record.PID))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.root, fmt.Sprintf("%d.json", s.record.PID)))
}

func (s *Server) serve(deliver DeliverWaitFunc, deliverImages DeliverImagesFunc) {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, deliver, deliverImages)
	}
}

func (s *Server) handle(conn net.Conn, deliver DeliverWaitFunc, deliverImages DeliverImagesFunc) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var request Request
	if err := json.NewDecoder(bufio.NewReader(io.LimitReader(conn, 1<<20))).Decode(&request); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "invalid_request"})
		return
	}
	if request.Type == "ping" {
		_ = json.NewEncoder(conn).Encode(Response{OK: true})
		return
	}
	if request.Type == TypeStatus {
		s.handleStatus(conn, request)
		return
	}
	if request.Type != "deliver" || request.SessionID != s.Record().SessionID || strings.TrimSpace(request.Text) == "" {
		_ = json.NewEncoder(conn).Encode(Response{Error: "invalid_request"})
		return
	}
	if deliver == nil && deliverImages == nil && s.messages == nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "delivery_unavailable"})
		return
	}
	// Images need a receiver that knows them: answering with an error instead of
	// taking the text is what keeps a picture from being dropped silently.
	if len(request.Images) > 0 && deliverImages == nil && s.messages == nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: codeImagesUnsupported})
		return
	}
	// An absent mode is the follow-up behaviour every client that predates the
	// field asks for.
	mode := request.Mode
	if mode == "" {
		mode = ModeFollowUp
	}
	// A receiver that knows message IDs takes the whole request; the older
	// endpoints are adapted, so a chat that registers them behaves as before.
	deliverMessage := s.messages
	switch {
	case deliverMessage != nil:
	case deliverImages != nil:
		deliverMessage = func(req DeliverRequest) (Delivery, <-chan error, error) {
			return deliverImages(req.Mode, req.Text, req.From, req.FromName, req.Hops, req.Images)
		}
	default:
		deliverMessage = func(req DeliverRequest) (Delivery, <-chan error, error) {
			return deliver(req.Mode, req.Text, req.From)
		}
	}
	delivery, settled, err := deliverMessage(DeliverRequest{
		Mode: mode, Text: request.Text, From: request.From, FromName: request.FromName,
		Hops: request.Hops, Images: request.Images, ID: request.ID, WaitProcessed: request.WaitProcessed,
	})
	if err != nil {
		code := "delivery_failed"
		if errors.Is(err, ErrImageRejected) {
			code = codeImageRejected
		} else if errors.Is(err, ErrQueueFull) {
			code = "queue_full"
		} else if errors.Is(err, ErrSessionClosed) {
			code = codeSessionClosed
		}
		_ = json.NewEncoder(conn).Encode(Response{Error: code, MessageID: request.ID})
		return
	}
	encoder := json.NewEncoder(conn)
	ack := Response{OK: true, Queued: delivery.Queued, Steered: delivery.Steered, Warning: delivery.Warning}
	if request.ID != "" {
		// Only a request that names an ID gets the new fields: an older client reads
		// exactly the answer it always got (design D4).
		ack.MessageID, ack.State = request.ID, StateQueued
		if delivery.Steered {
			ack.State = StateSteered
		}
	}
	_ = encoder.Encode(ack)
	if (!request.Wait && !request.WaitProcessed) || settled == nil || (!delivery.Queued && !delivery.Steered) {
		return
	}
	// Two-phase reply: the sender already has the "queued"/"steered" ack; keep the connection open (no
	// deadline, the wait is as long as the current turn) and report the message's fate.
	_ = conn.SetDeadline(time.Time{})
	select {
	case err := <-settled:
		_ = encoder.Encode(receipt(request, ack, err))
	case <-s.closing:
		_ = encoder.Encode(Response{Queued: delivery.Queued, Steered: delivery.Steered, Error: codeSessionClosed, MessageID: request.ID})
	}
}

// receipt is the second answer of a waiting sender. A --wait sender gets exactly the
// answer it always got (the ack, or "discarded"/"session_closed") so its exit codes
// do not change; a --wait-processed sender gets the message's terminal state
// (design session-message-delivery D4).
func receipt(request Request, ack Response, settledErr error) Response {
	if !request.WaitProcessed {
		if settledErr == nil {
			return ack
		}
		return Response{Queued: ack.Queued, Steered: ack.Steered, Error: codeDiscarded, MessageID: request.ID}
	}
	resp := Response{Queued: ack.Queued, Steered: ack.Steered, Warning: ack.Warning, MessageID: request.ID}
	switch {
	case settledErr == nil:
		resp.OK, resp.State = true, StateProcessed
	case errors.Is(settledErr, ErrHeld):
		// The failed turn moved the message to the person's next turn: accepted, not run.
		resp.OK, resp.State = true, StateHeld
	case errors.Is(settledErr, ErrDiscarded):
		resp.State, resp.Error = StateDiscarded, codeDiscarded
	default:
		resp.State, resp.Error = StateFailed, StateFailed
	}
	return resp
}

// handleStatus answers one status query: the state of a message ID in this process.
// An ID it never saw (a restart, or another process's message) is reported as an
// error, so the caller can say "unknown" instead of guessing.
func (s *Server) handleStatus(conn net.Conn, request Request) {
	if request.SessionID != s.Record().SessionID || strings.TrimSpace(request.ID) == "" {
		_ = json.NewEncoder(conn).Encode(Response{Error: "invalid_request"})
		return
	}
	state, ok := "", false
	if s.status != nil {
		state, ok = s.status(request.ID)
	}
	if !ok {
		_ = json.NewEncoder(conn).Encode(Response{Error: codeUnknownMessage, MessageID: request.ID})
		return
	}
	_ = json.NewEncoder(conn).Encode(Response{OK: true, MessageID: request.ID, State: state})
}

// codeImagesUnsupported is the response of a server that does not know
// Request.Images at all (it ignores the unknown field of a Request otherwise):
// the sender learns that nothing was taken.
const codeImagesUnsupported = "images_unsupported"

// ErrImagesUnsupported is what Send returns when the receiving process cannot
// take the request's images (an older version): nothing was delivered.
var ErrImagesUnsupported = errors.New("receiving process does not support images")

// ErrImageRejected is what a receiver reports when it refuses the images of a
// request (a missing file, a sha256 that does not match what the sender named):
// the whole message is refused, nothing is delivered.
var ErrImageRejected = errors.New("delivered image rejected")

// codeImageRejected is the response code of a receiver that refused the images of
// a request; a sender turns it back into ErrImageRejected.
const codeImageRejected = "image_rejected"

// ErrQueueFull reports a full delivery queue (of the running turn's steers, or of
// the messages waiting for the next turn): the sender is told to try again later
// instead of queueing without bound.
var ErrQueueFull = errors.New("delivery queue is full")

func List(root string) ([]Record, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record Record
		if json.Unmarshal(data, &record) != nil || record.PID <= 0 || filepath.Base(entry.Name()) != fmt.Sprintf("%d.json", record.PID) || !trustedSocket(record.Socket, root, record.PID) {
			continue
		}
		if !processAlive(record.PID, record.ProcStart) {
			// Both candidate locations are removed: whichever the record
			// named, only a dead owner can have left either behind.
			_ = os.Remove(path)
			for _, trusted := range trustedSocketPaths(root, record.PID) {
				_ = os.Remove(trusted)
			}
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID < records[j].SessionID })
	return records, nil
}

func Send(record Record, request Request) (Response, error) {
	if !processAlive(record.PID, record.ProcStart) {
		return Response{}, ErrNotOnline
	}
	conn, err := net.DialTimeout("unix", record.Socket, 2*time.Second)
	if err != nil {
		// The owner may have exited between the liveness check and the dial.
		if !processAlive(record.PID, record.ProcStart) {
			return Response{}, ErrNotOnline
		}
		return Response{}, fmt.Errorf("connect session socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if request.Type == "" {
		request.Type = "deliver"
	}
	request.SessionID = record.SessionID
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, err
	}
	decoder := json.NewDecoder(conn)
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return Response{}, err
	}
	if response.Error == codeImagesUnsupported {
		return response, ErrImagesUnsupported
	}
	if response.Error == codeImageRejected {
		return response, ErrImageRejected
	}
	if response.Error == "queue_full" {
		return response, ErrQueueFull
	}
	if response.Error == codeSessionClosed {
		return response, ErrNotOnline
	}
	if response.Error == codeUnknownMessage {
		return response, ErrUnknownMessage
	}
	if !response.OK {
		return response, fmt.Errorf("delivery failed: %s", response.Error)
	}
	// Only a message the server took into its queue or into the running turn is
	// waited for: one that goes straight into a turn (an idle chat) has nothing to
	// report later, which is the behaviour --wait always had. --wait-processed waits
	// for the terminal state of exactly those messages too (design D4).
	if (!request.Wait && !request.WaitProcessed) || (!response.Queued && !response.Steered) {
		return response, nil
	}
	// Wait for the message's fate. A dropped connection means the chat died.
	_ = conn.SetDeadline(time.Time{})
	var final Response
	if err := decoder.Decode(&final); err != nil {
		return response, fmt.Errorf("%w: connection closed before the message ran", ErrNotOnline)
	}
	if final.Error == codeDiscarded || final.Error == codeSessionClosed {
		return final, fmt.Errorf("%w: the message was discarded before it ran", ErrNotOnline)
	}
	if request.WaitProcessed {
		// A --wait-processed sender reads the terminal state itself (the CLI decides
		// what exit code failed and discarded get): no error hides the state.
		return final, nil
	}
	if !final.OK {
		return final, fmt.Errorf("delivery failed: %s", final.Error)
	}
	return final, nil
}

var ErrNotOnline = errors.New("session process is not online")

// Status asks one running process for the state of a message ID (`suga send --id`,
// session_message action=status). ErrUnknownMessage means the process never saw the
// ID: it belongs to another process, or the chat restarted (design
// session-message-delivery D3).
func Status(record Record, id string) (string, error) {
	response, err := Send(record, Request{Type: TypeStatus, ID: id})
	if errors.Is(err, ErrUnknownMessage) {
		return "", ErrUnknownMessage
	}
	if err != nil {
		return "", err
	}
	return response.State, nil
}

// processAlive reports whether pid still runs the process that recorded procStart.
// An empty procStart (older record or unsupported platform) falls back to the PID alone.
func processAlive(pid int, procStart string) bool {
	if !processExists(pid) {
		return false
	}
	if procStart == "" {
		return true
	}
	current := processStartID(pid)
	return current == "" || current == procStart
}

// Resolve picks the live process a selector names. Three stages, each only
// ambiguous within itself: an exact session ID, then an exact short name
// (ignoring case), then a session ID prefix. The order matters, a full ID that
// is also another session's prefix or short name never turns ambiguous, and a
// short name that is also a prefix of several IDs still addresses one process.
func Resolve(records []Record, selector string) (Record, error) {
	selector = strings.TrimSpace(selector)
	// An empty selector would match every record without a short name below.
	if selector == "" {
		return Record{}, ErrNotOnline
	}
	stages := []func(Record) bool{
		func(record Record) bool { return record.SessionID == selector },
		func(record Record) bool { return strings.EqualFold(record.Name, selector) },
		func(record Record) bool { return strings.HasPrefix(record.SessionID, selector) },
	}
	for _, matches := range stages {
		var picked []Record
		for _, record := range records {
			if matches(record) {
				picked = append(picked, record)
			}
		}
		if len(picked) > 1 {
			return Record{}, ErrAmbiguous
		}
		if len(picked) == 1 {
			return picked[0], nil
		}
	}
	return Record{}, ErrNotOnline
}

var ErrAmbiguous = errors.New("session selector matches multiple processes")
