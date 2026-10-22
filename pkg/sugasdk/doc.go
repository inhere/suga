// Package sugasdk embeds suga in another program, in process: the host opens an
// engine, opens sessions in its own workspaces, runs turns and reads the answers
// and events, and reaches the same session history and memory the suga CLI uses.
//
// # Experimental
//
// The package is a first version: the API may change in a minor release until
// the host that embeds it has settled. Types and methods are documented with the
// behaviour they promise; the wire-level details of suga (events, trace, store)
// are not part of this contract.
//
// # One engine per process
//
// suga's configuration (config.yaml, .env, the model selection, tools.*) is
// process global. Therefore a process holds at most one open Engine: a second
// Open returns ErrEngineOpen, and after Close an Open is possible again. The
// data root is guarded the same way: Open sets $SUGA_HOME and loads the
// configuration when the process has none, reuses the configuration of the same
// root, and returns ErrSugaHomeMismatch when the process already loaded another
// root. The loaded configuration stays for the life of the process.
//
// # Sessions
//
// Every session assembles its own runtime for its workspace (tools, provider,
// project rules, memory scope), so sessions of different directories can run
// side by side. A session runs one turn at a time: a second concurrent Send
// returns ErrBusy instead of queueing, and Steer inserts text into a turn that
// is already running (ErrNoRunningTurn when there is none). A panic inside a
// turn is recovered and returned as an error; it never propagates to the host.
//
// # Idempotent sends
//
// Prompt.RequestID (recommended: a UUID the caller generates, scoped to one
// session) makes a Send deduplicated instead of always running: with an id this
// session already finished, Send replays the stored first result with
// Result.Replayed set and the model is not called again. The replay comes from
// storage: a small record in the session meta (turn id, state, a summary with
// the usage numbers and the end time; never the answer text, so the meta does
// not grow with the length of the answers), and the output rebuilt from the
// turn's own assistant entry. A turn whose entries are gone from the session
// log no longer replays: Send reports ErrRequestOutputGone. An id whose turn
// is running errors with ErrRequestRunning; a record left
// running by a process that died mid-turn (delivery unknown) errors with
// ErrDeliveryUncertain and reads "unknown" from Session.RequestStatus: the
// host resends the prompt under a NEW request id, nothing replays on its own.
// The mapping and outcomes survive restarts and a reopened session handle
// answers the same way. A Prompt without a RequestID keeps the plain
// behaviour. Missing or blank ids in place of RequestID (a Prompt with a
// whitespace-only id) are refused.
//
// Sessions opened without SessionOptions.Origin record origin "xhen" and are
// hidden by the default `suga sessions` list, which the host then filters with
// Engine.Sessions(SessionFilter{Origin: "xhen"}).
//
// # Thread safety
//
// Open, Close and OpenSession are safe to call from several goroutines, as are
// the Engine methods (Memory, Search, Sessions): they share the engine's locks.
// A Session's Send, Steer, Card, Compact and Close may be called from several
// goroutines too - Send and Steer are meant to be, since a turn is steered while
// it runs - and Close makes later calls return ErrSessionClosed. The Event
// callback of Send runs on the goroutine of the turn that emits it; it must not
// call Send on the same session (that is ErrBusy) but may call Steer.
//
// # Scope
//
// The SDK covers suga's programmatic side: one turn with its events and
// result, steering, the session card, compaction, session search and listing,
// and the memory store. It does not offer the chat terminal, hooks beyond
// agentcore.Hook, ACP or the MCP server gate; a host that needs those runs the
// CLI or the ACP server instead.
package sugasdk
