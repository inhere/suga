package api

// RunInput captures the public input contract for a single agent run.
type RunInput struct {
	RunID       string
	AgentID     string
	Instruction string
	// Images are pictures that go with the instruction (the user message of this
	// run carries them); an input may be images alone, without an instruction.
	Images     []ImagePart
	Metadata   map[string]any
	Stream     StreamFunc
	MaxSteps   int
	ResumeMode ResumeMode
}

// RunStatus describes the lifecycle state of a run.
type RunStatus string

const (
	RunIdle      RunStatus = "idle"
	RunRunning   RunStatus = "running"
	RunYielded   RunStatus = "yielded"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

// ResumeMode describes how a runtime should source state when continuing a run.
type ResumeMode string

const (
	ResumeAuto      ResumeMode = "auto"
	ResumeFromState ResumeMode = "from_state"
	ResumeWithInput ResumeMode = "with_input"
)

// ToolBatchMetaKey is the Message.Metadata key (string) shared by the
// assistant tool-call messages of one model reply with several calls. The
// runtime stores each call with its result right after it; providers use the
// key to send such a reply back as one assistant message with all its calls
// followed by the results, so the history shows parallel calls as they were.
const ToolBatchMetaKey = "suga.tool_batch"

// Message is the public conversation unit exchanged with prompt, context, and model layers.
type Message struct {
	Role       string
	Content    string
	ToolCall   *ToolCall
	ToolResult *ToolResult
	Metadata   map[string]any
	// Reasoning is the model's reasoning text for an assistant message (e.g.
	// OpenAI-compatible reasoning_content). It is persisted with the message so
	// providers whose APIs require it can send it back on later requests.
	Reasoning string `json:",omitempty"`
	// ResultSameAsContent is a storage marker, never set in memory: the store
	// writes it when it keeps ToolResult.Content once (it equals Content), and
	// UnmarshalJSON restores the result and clears the flag. It is omitted from
	// every encoding that does not set it.
	ResultSameAsContent bool `json:",omitempty"`
	// Images are the pictures of this message: pictures the user attached to a
	// user message, or the images a tool result returned (view_image). It is
	// omitted when empty, so a plain text message encodes exactly as before.
	Images []ImagePart `json:",omitempty"`
}

// ImagePart is one image of a message. The bytes are never stored: Data is
// filled in memory by the host before a request (read back from Ref) and
// cleared after the image is written to disk, so a stored message carries only
// the reference and metadata.
//
// Ref is the path of the stored file relative to the session directory
// (images/<sha256>.<ext>). SHA256 is the hex digest of the sent bytes; a read
// back that does not match it is treated as a missing image.
//
// MediaType is the mime type of the sent bytes, Data (json:"-") holds those
// bytes for the provider call only; Width and Height are their pixel size (0
// when unknown), used for the text stand-in of an omitted image and for the
// image token estimate.
type ImagePart struct {
	MediaType string `json:",omitempty"`
	Ref       string `json:",omitempty"`
	SHA256    string `json:",omitempty"`
	Width     int    `json:",omitempty"`
	Height    int    `json:",omitempty"`
	Data      []byte `json:"-"`
}

// Artifact is a minimal portable output produced by tools or models.
type Artifact struct {
	Kind     string
	URI      string
	Content  string
	Metadata map[string]any
}

// Usage tracks model token consumption.
type Usage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// Add returns the field-wise sum of u and o. Providers report usage per model
// call; a run or session total is the sum of its calls.
func (u Usage) Add(o Usage) Usage {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.TotalTokens += o.TotalTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.CostUSD += o.CostUSD
	return u
}

// Yield captures a paused runtime state that requires external input.
type Yield struct {
	Reason   string
	ResumeOK bool
	Metadata map[string]any
}

// RunState is the primary public runtime state contract shared across storage and builders.
type RunState struct {
	RunID     string
	AgentID   string
	Status    RunStatus
	Step      int
	Messages  []Message
	Artifacts []Artifact
	Metadata  map[string]any
	Scratch   map[string]any
	Usage     Usage
	LastError string
	// WrappingUp tells the ContextBuilder that the step it is building for asks
	// the model for a final answer instead of more work ("" = an ordinary step);
	// the value is why ("max_steps" or a hook's Control.RequestWrapUp reason).
	// The runtime sets it before every BuildMessages call. It is transient: it is
	// never persisted and never marshalled (RunResult.WrapUp is the persisted,
	// caller-facing reason a run ended with a wrap-up answer).
	WrappingUp string `json:"-"`
}

// RunResult is the public outcome contract returned by a runtime execution.
type RunResult struct {
	RunID  string
	Status RunStatus
	Output string
	Steps  int
	Usage  Usage
	Yield  *Yield
	// WrapUp is why the run ended with a wrap-up answer ("max_steps", or the
	// reason a hook gave Control.RequestWrapUp); empty for a normal answer.
	WrapUp string
	// Truncated is true when the answer was cut off at the output token limit
	// (the model's stop reason), so it may be incomplete; OutputLimit names how
	// many tokens that limit allowed (0 = the provider's or server's own
	// default, i.e. no limit was configured).
	Truncated   bool
	OutputLimit int
	// EmptyFinal marks a normal completion whose final model answer was empty
	// after repeated empty replies following a successful tool call.
	EmptyFinal bool
}
