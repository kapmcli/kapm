package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// IDEExecutionResult holds aggregated execution data for one session.
type IDEExecutionResult struct {
	TotalCredits float64
	StartTime    time.Time
	EndTime      time.Time
	Executions   int
	ToolCalls    int
	ToolActions  []IDEAction // tool-type actions for timeline
}

var profileHashRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// LoadIDEExecutions scans ideBaseDir for profileHash directories and aggregates
// execution logs matching the given executionIDs set.
// Returns a map keyed by chatSessionId.
func LoadIDEExecutions(ctx context.Context, ideBaseDir string, executionIDs map[string]struct{}) (map[string]IDEExecutionResult, error) {
	results := make(map[string]IDEExecutionResult)

	if len(executionIDs) == 0 {
		return results, nil
	}

	profileEntries, err := os.ReadDir(ideBaseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return results, nil
		}
		return nil, err
	}

	for _, pe := range profileEntries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !pe.IsDir() || !profileHashRe.MatchString(pe.Name()) {
			continue
		}
		profileDir := filepath.Join(ideBaseDir, pe.Name())
		entries, err := os.ReadDir(profileDir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			slog.Warn("ide executions: read profile dir", "dir", profileDir, "err", err)
			continue
		}

		for _, e := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if e.IsDir() {
				sessionDir := filepath.Join(profileDir, e.Name())
				scanSessionDir(sessionDir, executionIDs, results)
			}
		}
	}

	return results, nil
}

// scanSessionDir reads all files in a session hash directory as execution logs.
func scanSessionDir(dir string, executionIDs map[string]struct{}, results map[string]IDEExecutionResult) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("ide executions: read session dir", "dir", dir, "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		processExecutionLog(path, executionIDs, results)
	}
}

// processExecutionLog reads one execution log file and updates results if the
// executionId is in the requested set.
func processExecutionLog(path string, executionIDs map[string]struct{}, results map[string]IDEExecutionResult) {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("ide executions: read file", "path", path, "err", err)
		return
	}
	var log IDEExecutionLog
	if err := json.Unmarshal(data, &log); err != nil {
		slog.Warn("ide executions: parse file", "path", path, "err", err)
		return
	}
	if log.ExecutionID == "" {
		return
	}
	if _, ok := executionIDs[log.ExecutionID]; !ok {
		return
	}

	var credits float64
	for _, u := range log.UsageSummary {
		credits += u.Usage
	}

	toolCalls := 0
	var toolActions []IDEAction
	for i, action := range log.Actions {
		switch action.ActionType {
		case ActionReadFiles, ActionRunCommand, ActionWrite, ActionCreate, ActionDelete, ActionSearch, ActionInvokeSubAgent, ActionSubAgentResponse:
			// Estimate duration from gap to next action's emittedAt, or execution endTime.
			nextTs := log.EndTime
			for j := i + 1; j < len(log.Actions); j++ {
				if log.Actions[j].EmittedAt > action.EmittedAt {
					nextTs = log.Actions[j].EmittedAt
					break
				}
			}
			action.EstimatedDuration = time.Duration(nextTs-action.EmittedAt) * time.Millisecond
			toolCalls++
			toolActions = append(toolActions, action)
		}
	}

	start := time.UnixMilli(log.StartTime)
	end := time.UnixMilli(log.EndTime)

	prev := results[log.ChatSessionID]
	if prev.Executions == 0 || start.Before(prev.StartTime) {
		prev.StartTime = start
	}
	if prev.Executions == 0 || end.After(prev.EndTime) {
		prev.EndTime = end
	}
	prev.TotalCredits += credits
	prev.ToolCalls += toolCalls
	prev.ToolActions = append(prev.ToolActions, toolActions...)
	prev.Executions++
	results[log.ChatSessionID] = prev
}

// BuildIDEMergedRecords combines IDEParsedSession list with execution results
// into MergedRecord slices (Kind="sessionMeta").
// A record is always generated even when TotalCredits is zero.
func BuildIDEMergedRecords(sessions []IDEParsedSession, execResults map[string]IDEExecutionResult) []MergedRecord {
	var out []MergedRecord
	for _, s := range sessions {
		res := execResults[s.SessionID]
		createdAt := s.CreatedAt
		if !res.StartTime.IsZero() && (createdAt.IsZero() || res.StartTime.Before(createdAt)) {
			createdAt = res.StartTime
		}
		updatedAt := res.EndTime
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		out = append(out, MergedRecord{
			SessionID:         s.SessionID,
			Kind:              RecordKindSessionMeta,
			Agent:             "kiro-ide",
			Title:             s.Title,
			Cwd:               s.WorkspaceDirectory,
			CreatedAt:         createdAt,
			UpdatedAt:         updatedAt,
			TotalCredits:      res.TotalCredits,
			TotalInputTokens:  0,
			TotalOutputTokens: 0,
			PromptTexts:       s.PromptTexts,
		})
		// Generate toolUse + toolResult pairs from IDE actions for Timeline.
		var lastInvokeTs int64
		for _, a := range res.ToolActions {
			toolName := a.ActionType
			if toolName == ActionRunCommand {
				toolName = ToolNameShell
			}

			preTs := time.UnixMilli(a.EmittedAt)
			postTs := preTs.Add(a.EstimatedDuration)

			// invokeSubAgent's emittedAt is completion time; derive start from duration.
			if a.ActionType == ActionInvokeSubAgent {
				postTs = preTs
				preTs = postTs.Add(-a.EstimatedDuration)
				lastInvokeTs = a.EmittedAt
			}
			// subagentResponse must sort after invokeSubAgent; duration is 0 (it's the response).
			if a.ActionType == ActionSubAgentResponse {
				if a.EmittedAt <= lastInvokeTs {
					preTs = time.UnixMilli(lastInvokeTs + 1)
				}
				postTs = preTs
			}

			rec := MergedRecord{
				SessionID:   s.SessionID,
				Kind:        RecordKindToolUse,
				Agent:       "kiro-ide",
				Cwd:         s.WorkspaceDirectory,
				ToolName:    toolName,
				ToolUseID:   a.ActionID,
				ToolInput:   a.Input,
				PreToolTs:   preTs,
				ActionState: a.ActionState,
			}

			// Parse sub-agent invocation data.
			if a.ActionType == ActionInvokeSubAgent {
				rec.SubAgent = parseSubAgentCall(a, preTs, a.EstimatedDuration)
			}

			out = append(out, rec)
			out = append(out, MergedRecord{
				SessionID:   s.SessionID,
				Kind:        RecordKindToolResult,
				Agent:       "kiro-ide",
				ToolName:    toolName,
				ToolUseID:   a.ActionID,
				ToolStatus:  ideActionStatus(a.ActionState),
				ErrorDetail: a.ErrorMessage,
				ToolResult:  string(a.Output),
				PostToolTs:  postTs,
				ActionState: a.ActionState,
			})
		}
	}
	return out
}

func ideActionStatus(state string) string {
	switch state {
	case "Error", "Rejected":
		return ToolStatusError
	default:
		return ToolStatusSuccess
	}
}

func parseSubAgentCall(a IDEAction, ts time.Time, dur time.Duration) *SubAgentCall {
	var inp struct {
		SubAgentName string `json:"subAgentName"`
		Explanation  string `json:"explanation"`
		Prompt       string `json:"prompt"`
	}
	if err := json.Unmarshal(a.Input, &inp); err != nil {
		slog.Warn("ide executions: parse action", "action_id", a.ActionID, "err", err)
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(a.Output, &out); err != nil {
		slog.Warn("ide executions: parse action", "action_id", a.ActionID, "err", err)
	}
	return &SubAgentCall{
		AgentName:   inp.SubAgentName,
		Explanation: inp.Explanation,
		Prompt:      inp.Prompt,
		Response:    out.Response,
		Duration:    JSONDuration(dur),
		Ts:          ts,
	}
}
