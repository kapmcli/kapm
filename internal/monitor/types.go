package monitor

import (
	"regexp"
	"time"
	"unicode/utf8"
)

// Kind values of MergedRecord. These are internal but flow into aggregation
// and WebUI payloads; wire-format stability is desirable.
// RecordKind* values use camelCase to match the JSONL record format (wire format; do not change).
const (
	RecordKindPrompt        = "prompt"
	RecordKindToolUse       = "toolUse"
	RecordKindToolResult    = "toolResult"
	RecordKindAssistantText = "assistantText"
	RecordKindSessionMeta   = "sessionMeta"
	RecordKindAgentSpawn    = "agentSpawn"
	RecordKindStop          = "stop"
	RecordKindHookEvent     = "hookEvent"
)

// SessionMessage.Kind values (wire format from Kiro; do not change).
const (
	MessageKindPrompt           = "Prompt"
	MessageKindAssistantMessage = "AssistantMessage"
	MessageKindToolResults      = "ToolResults"
)

// ContentItem.Kind values (wire format from Kiro; do not change).
const (
	ContentKindText       = "text"
	ContentKindJSON       = "json"
	ContentKindToolUse    = "toolUse"
	ContentKindToolResult = "toolResult"
)

// ToolResultData.Status values (wire format from Kiro).
const (
	ToolStatusSuccess = "success"
	ToolStatusError   = "error"
)

var skillPathRe = regexp.MustCompile(`([a-zA-Z0-9_-]+)/SKILL\.md`)

const (
	maxRecentCalls             = 100
	maxErrors                  = 50
	activeSessionTimeout       = 5 * time.Minute
	maxSummaryLength           = 120
	maxErrorDetailLength       = 256
	maxAssistantResponseLength = 2048
)

// truncateUTF8 truncates s to at most maxBytes bytes without splitting
// a multi-byte UTF-8 sequence. If truncation occurs the result is always
// valid UTF-8 and ≤ maxBytes.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return ""
	}
	// Walk backwards from maxBytes to find a valid rune boundary.
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// FileChange command constants.
const (
	CommandCreate     = "create"
	CommandStrReplace = "strReplace"
	CommandInsert     = "insert"
	CommandDelete     = "delete"
)

// FileChange captures a single file modification via the write tool.
// Diff is NOT stored — rendered on demand by template helper to avoid
// re-computing on every AggregateDetail call.
type FileChange struct {
	Path      string // normalized (filepath.Clean + cwd if relative) at extraction time
	Ts        time.Time
	Command   string // "create" | "strReplace" | "insert"
	Purpose   string // optional, from tool_input.__tool_use_purpose
	Content   string // for create/insert (empty if Oversized)
	OldStr    string // for strReplace (empty if Oversized)
	NewStr    string // for strReplace (empty if Oversized)
	Oversized bool   // true when any content field was truncated due to size cap
}

// SessionMetric is an overview-level session summary retained for backwards compatibility.
type SessionMetric struct {
	ID           string
	AgentKey     string // composite key sid + "|" + agent; unique per (session, agent)
	Agent        string
	Title        string // first userPromptSubmit prompt, cleaned; may be empty
	Cwd          string
	StartTime    time.Time
	EndTime      time.Time
	LastActivity time.Time
	Duration     JSONDuration
	Active       bool // last event within 5min of now
	ToolCalls    int
	Prompts      int
	FilesChanged int

	TotalInputTokens  int     // sessions meta derived
	TotalOutputTokens int     // sessions meta derived
	TotalCredits      float64 // sessions meta derived (metering_usage sum)
	TurnDurationSecs  float64 // sessions meta derived (turn_duration sum)
}

// ToolMetric is an overview-level tool usage summary retained for backwards compatibility.
type ToolMetric struct {
	Name       string
	CallCount  int
	ErrorCount int // preToolUse without matching postToolUse, or non-zero exit_status
	ErrorRate  float64
}

// AgentMetric is an overview-level agent activity summary retained for backwards compatibility.
type AgentMetric struct {
	Name              string
	SessionCount      int
	ToolCalls         int
	Prompts           int
	FilesChanged      int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCredits      float64
}

// HourlyMetric is an hourly event count for activity chart display.
type HourlyMetric struct {
	Hour       time.Time // truncated to hour
	EventCount int
}

// Metrics is the aggregated overview metrics retained for backwards compatibility.
type Metrics struct {
	Sessions       []SessionMetric
	Tools          []ToolMetric
	Agents         []AgentMetric
	HourlyActivity []HourlyMetric
}

// EventEntry is one log event for timeline display.
type EventEntry struct {
	Ts           time.Time
	Event        string // agentSpawn | userPromptSubmit | preToolUse | postToolUse | stop
	Tool         string
	IsError      bool         // preToolUse without matching postToolUse
	ErrorDetail  string       // exit code + stderr excerpt (max 256 chars), empty if no error
	InputSummary string       // short human-readable summary of tool_input (preToolUse only)
	ToolInput    string       // full tool input text for expand view
	ToolResult   string       // full tool result text for expand view, when available
	Duration     JSONDuration // postToolUse.Ts - preToolUse.Ts (preToolUse only, 0 for errors)

	toolUseID string // unexported: used for toolUse/toolResult pairing
	matched   bool   // unexported: true when a toolResult resolved this toolUse
}

// ToolCall is one completed tool invocation (preToolUse matched with postToolUse)
// or an unmatched preToolUse marked as error.
type ToolCall struct {
	Ts           time.Time // preToolUse timestamp
	Session      string
	Agent        string
	Tool         string
	Duration     JSONDuration // postToolUse.Ts - preToolUse.Ts (0 for errors)
	IsError      bool         // no matching postToolUse
	InputSummary string       // short human-readable summary of tool_input
	ToolInput    string       // full tool input text
}

// SessionToolSummary is a per-tool breakdown within a single session.
type SessionToolSummary struct {
	Tool        string
	CallCount   int
	ErrorCount  int
	SuccessRate float64 // (CallCount - ErrorCount) / CallCount
	AvgDuration JSONDuration
}

// SessionDetail is the per-session drill-down payload.
type SessionDetail struct {
	SessionMetric
	HasShell           bool                 // true if any timeline entry used the shell tool
	PromptHistory      []string             // raw prompts, oldest first
	Timeline           []EventEntry         // full ordered event list for this session
	ToolSummary        []SessionToolSummary // per-tool breakdown, sorted by CallCount desc
	AssistantResponse  string               // LLM final response from stop event (max 2KB); kept for backward compat
	AssistantResponses []string             // all assistant responses per turn, oldest first
	Changes            []FileChange         // chronological order (sorted by Ts ascending)
	SubAgentCalls      []SubAgentCall       // IDE sub-agent invocations
}

// SubAgentCall represents a sub-agent invocation from an IDE session.
type SubAgentCall struct {
	AgentName   string
	Explanation string
	Prompt      string
	Response    string
	Duration    JSONDuration
	Ts          time.Time
}

// AgentDetail is the per-agent drill-down payload.
type AgentDetail struct {
	AgentMetric
	Sessions     []SessionMetric      // sessions owned by this agent, newest first
	ToolSummary  []SessionToolSummary // per-tool breakdown, sorted by CallCount desc
	ToolErrorCnt int                  // total error tool calls across all its sessions
}

// ToolAliasMetric summarizes one observed raw alias within a tool detail.
type ToolAliasMetric struct {
	Name       string
	CallCount  int
	ErrorCount int
	Percentage float64
}

// ToolDetail is the per-tool drill-down payload.
type ToolDetail struct {
	ToolMetric
	AvgDuration JSONDuration // average pre→post across matched calls
	Aliases     []ToolAliasMetric
	RecentCalls []ToolCall // newest first, matched calls with duration
	Errors      []ToolCall // unmatched preToolUse samples
}

// SkillUsage counts how many times a skill's SKILL.md was read.
type SkillUsage struct {
	Name      string
	ReadCount int
}

// DetailedMetrics is the full aggregation result.
type DetailedMetrics struct {
	Overview Metrics
	Sessions []SessionDetail
	Agents   []AgentDetail
	Tools    []ToolDetail
	Skills   []SkillUsage
}

type sessionState struct {
	id                 string
	agent              string
	cwd                string
	start              time.Time
	end                time.Time
	stopped            bool
	toolCalls          int
	prompts            []string
	timeline           []EventEntry
	sumTitle           string         // latest summary-tool taskDescription (if any)
	assistantResponse  string         // last response (for backward compat)
	assistantResponses []string       // per-turn final assistant text, from TurnResponses
	changes            []FileChange   // write preToolUse events, chronological
	filesChangedCached int            // countUniqueFiles(changes), populated in finalizeSessionStats
	pendingToolUse     map[string]int // toolUseID → timeline index
	totalInputTokens   int
	totalOutputTokens  int
	totalCredits       float64
	subAgentCalls      []SubAgentCall
}

// aggState holds the mutable accumulators shared by the three
// AggregateDetail phases: processRecord, finalizeSessionStats, assembleDetails.
type aggState struct {
	now            time.Time
	sessions       map[string]*sessionState
	tools          map[string]*ToolDetail
	agents         map[string]*AgentDetail
	hours          map[time.Time]int
	skills         map[string]int
	sessionDetails []SessionDetail
}

// toolAgg accumulates call counts and durations for one tool within an agent.
type toolAgg struct {
	callCount  int
	errorCount int
	durSum     time.Duration
	durCount   int
}

// addCall records a single tool invocation.
func (a *toolAgg) addCall(isError bool, dur time.Duration) {
	a.callCount++
	if isError {
		a.errorCount++
	} else if dur > 0 {
		a.durSum += dur
		a.durCount++
	}
}

// addSummary merges an existing SessionToolSummary (for cross-agent merging).
func (a *toolAgg) addSummary(ts SessionToolSummary) {
	a.callCount += ts.CallCount
	a.errorCount += ts.ErrorCount
	sc := ts.CallCount - ts.ErrorCount
	if sc > 0 && ts.AvgDuration > 0 {
		a.durSum += time.Duration(ts.AvgDuration) * time.Duration(sc)
		a.durCount += sc
	}
}

// toolInput is a lenient typed view of tool_input payloads. Fields absent in
// the payload remain zero-valued; unknown fields are ignored (tool producers
// may send extra fields).
type toolInput struct {
	Operations []operation `json:"operations,omitempty"`
	Path       string      `json:"path,omitempty"`
	Paths      []string    `json:"paths,omitempty"`
	Command    string      `json:"command,omitempty"`
	Purpose    string      `json:"__tool_use_purpose,omitempty"`
	FilePath   string      `json:"file_path,omitempty"`
	Pattern    string      `json:"pattern,omitempty"`
	Prompt     string      `json:"prompt,omitempty"`
	Content    string      `json:"content,omitempty"`
	SymbolName string      `json:"symbol_name,omitempty"`
	NewName    string      `json:"new_name,omitempty"`
	Query      string      `json:"query,omitempty"`
}

// operation describes a single entry in tool_input.operations (used by read).
type operation struct {
	Mode       string   `json:"mode,omitempty"`
	Path       string   `json:"path,omitempty"`
	FilePath   string   `json:"file_path,omitempty"`
	ImagePaths []string `json:"image_paths,omitempty"`
	Offset     int      `json:"offset,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// TimeseriesPoint is one time-bucket of aggregated tool calls.
type TimeseriesPoint struct {
	Bucket      time.Time    `json:"bucket"`
	Count       int          `json:"count"`
	AvgDuration JSONDuration `json:"avgDuration"`
	ErrorCount  int          `json:"errorCount"`
}

// PatternCount is one InputSummary pattern with its call count.
type PatternCount struct {
	Summary string    `json:"summary"`
	Count   int       `json:"count"`
	LastTs  time.Time `json:"lastTs"`
}
