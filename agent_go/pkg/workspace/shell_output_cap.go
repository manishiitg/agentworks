package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultAgentShellOutputBytes bounds what a single shell result shows an agent.
//
// Nothing on this path capped anything: workspace/handlers/shell.go returns
// stdoutBuf.String() whole, and mcpagent's 100KB maxOutputBytes guards a
// different executor the builder never calls. So a coding CLI received the full
// payload and rejected it against its own token cap — 12 times in 5h37m on
// 2026-08-04, at 67,930 to 130,046 characters.
//
// The number is chosen against the smallest observed rejection (67,930
// characters, "across 1 line"). Single-line output tokenizes at roughly 2.7
// characters per token, so 48,000 characters is about 18k tokens — under a
// 25k-token result cap even in that worst case, and about 12k tokens for
// ordinary prose.
const defaultAgentShellOutputBytes = 48000

// agentShellStderrReserve keeps room for stderr even when stdout is enormous.
// stderr is where the reason for a failure lives, and it is the part an agent
// most needs; a big stdout must not push it out of the result.
const agentShellStderrReserve = 8000

// agentShellOutputBytes reads the cap, allowing an operator to tune it for a
// consumer with a different limit without a rebuild. A non-positive value
// disables capping.
func agentShellOutputBytes() int {
	raw := strings.TrimSpace(os.Getenv("SHELL_MAX_AGENT_OUTPUT_BYTES"))
	if raw == "" {
		return defaultAgentShellOutputBytes
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return defaultAgentShellOutputBytes
	}
	return parsed
}

// capShellResultForAgent bounds a shell result that an LLM will read.
//
// It is applied in the tool executor, not in Client.ExecuteShellCommand, because
// scripted steps call the client directly and parse stdout as schema-validated
// JSON (controller_scripted.go). Truncating there would corrupt output that is
// correct.
//
// Truncation keeps both ends. An agent usually needs either the beginning (what
// ran) or the end (how it finished), and dropping the tail turns "the command
// worked" into "the command produced nothing conclusive". The marker between
// them states the real size and names the ways to produce less, because an agent
// told only that output was truncated re-runs the identical command — three of
// the 2026-08-04 rejections were byte-identical repeats.
func capShellResultForAgent(result ShellCommandResult) ShellCommandResult {
	return capShellStreamsToBudget(result, agentShellOutputBytes())
}

// capShellStreamsToBudget bounds the two streams to a combined byte budget.
// Split out from capShellResultForAgent so the encoder can re-cap against a
// smaller budget once it has measured what serialization actually costs.
func capShellStreamsToBudget(result ShellCommandResult, limit int) ShellCommandResult {
	if limit <= 0 {
		return result
	}
	if len(result.Stdout)+len(result.Stderr) <= limit {
		return result
	}

	stderrBudget := agentShellStderrReserve
	if stderrBudget > limit/2 {
		stderrBudget = limit / 2
	}
	if len(result.Stderr) < stderrBudget {
		stderrBudget = len(result.Stderr)
	}
	result.Stderr = truncateShellStream(result.Stderr, stderrBudget, "stderr")
	result.Stdout = truncateShellStream(result.Stdout, limit-len(result.Stderr), "stdout")
	return result
}

// encodeShellResultForAgent serializes a result without HTML escaping.
//
// encoding/json escapes <, > and & as <, > and & by default —
// six bytes for one. That protection is for JSON embedded in an HTML page; this
// payload goes to a tool-result channel, so it buys nothing and costs 6x on the
// most ordinary content these workflows produce. Measured on 48,000 capped
// characters: all-angle-bracket content encoded to 286,333 bytes with escaping,
// and HTML-ish content to 90,949.
func encodeShellResultForAgent(result ShellCommandResult) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	// Encoder.Encode appends a newline that Marshal does not.
	return strings.TrimRight(buf.String(), "\n"), nil
}

// marshalCappedShellResultForAgent bounds the SERIALIZED payload, which is the
// thing the consumer actually measures.
//
// Capping the streams and then encoding does not bound anything: quotes and
// backslashes double, control characters expand sixfold, and the JSON envelope
// adds its own bytes — all after the budget was checked. A 48,000-character cap
// delivered up to 286,333 bytes. So encode, measure, and if the real payload is
// over, shrink the stream budget in proportion to the overshoot and encode
// again. Ordinary output settles on the first pass; escape-heavy output takes a
// few, and each pass is a marshal of an already-bounded string.
// annotateKnownShellFailures appends a short, actionable note when the result
// carries a failure signature agents are known to retry unchanged.
//
// A quoting error in generated code is the case this exists for. An apostrophe
// in prose arrives already escaped through the tool-call boundary, gets escaped
// again into the source, and the resulting backslash does not escape the quote —
// it terminates the string. The interpreter reports the symptom ("unterminated
// string literal") at a line number, which reads like a typo, so the agent
// rewrites the same prose the same way. One observed session reached
// migrate3.py: three attempts, one bug.
//
// The note names the cause and forbids the identical retry, which is the part
// that breaks the loop.
func annotateKnownShellFailures(result ShellCommandResult) ShellCommandResult {
	if result.ExitCode == 0 {
		return result
	}
	combined := result.Stderr + "\n" + result.Stdout
	if !strings.Contains(combined, "unterminated string literal") &&
		!strings.Contains(combined, "EOL while scanning string literal") {
		return result
	}
	result.Stderr = strings.TrimRight(result.Stderr, "\n") + "\n\n" +
		"... [quoting hint: this is almost always prose escaped twice, not a typo. Text carried through a tool call is already escaped; escaping it again yields a backslash before the quote, and that backslash does NOT escape the quote — it ends the string. " +
		"Do NOT rewrite the same text into the same literal; it will fail identically. " +
		"Move the text out of the code (write it to its own file and read it back), or use a triple-quoted block with a delimiter the text does not contain, and write the file with a QUOTED heredoc delimiter (<< 'EOF', not << EOF) so the shell does not re-escape the body. " +
		"For a targeted change prefer a targeted edit (your own Edit tool, or diff_patch_workspace_file without native tools) over rewriting the whole file.] ...\n"
	return result
}

func marshalCappedShellResultForAgent(result ShellCommandResult) (string, error) {
	result = annotateKnownShellFailures(result)
	limit := agentShellOutputBytes()
	if limit <= 0 {
		return encodeShellResultForAgent(result)
	}

	budget := limit
	for attempt := 0; attempt < 6; attempt++ {
		capped := capShellStreamsToBudget(result, budget)
		out, err := encodeShellResultForAgent(capped)
		if err != nil {
			return "", err
		}
		if len(out) <= limit {
			return out, nil
		}

		// Shrink in proportion to how far over we landed, with a margin so a
		// pass that lands just above the line does not need another one.
		next := budget * limit / len(out) * 19 / 20
		if next >= budget {
			next = budget - 1
		}
		if next < 1 {
			next = 1
		}
		budget = next
	}

	// Convergence failed — content so escape-dense that even a tiny budget
	// encodes over the limit. Returning the oversize payload would recreate the
	// spill this cap exists to prevent, so return the explanation instead.
	return encodeShellResultForAgent(ShellCommandResult{
		Stdout: "",
		Stderr: fmt.Sprintf(
			"... [output omitted: %d characters of stdout and %d of stderr could not be encoded within the %d-character result budget "+
				"because the content is almost entirely escaped characters. Do NOT re-run this command unchanged. "+
				"Produce less: filter with grep, slice with head/tail or sed -n '<start>,<end>p', select fields with jq/awk, "+
				"or write the full output to a file under your working directory and read it back in pieces.] ...",
			len(result.Stdout), len(result.Stderr), limit,
		),
		ExitCode: result.ExitCode,
	})
}

// truncateShellStream keeps the head and tail of s within budget, replacing the
// middle with a marker that says how much was dropped and what to do about it.
func truncateShellStream(s string, budget int, stream string) string {
	if budget < 0 {
		budget = 0
	}
	if len(s) <= budget {
		return s
	}

	marker := fmt.Sprintf(
		"\n\n... [%s truncated: %d of %d characters omitted. Do NOT re-run this command unchanged — it will be truncated identically. "+
			"Produce less: filter with grep, slice with head/tail or sed -n '<start>,<end>p', select fields with jq/awk, "+
			"or write the full output to a file under your working directory and read it back in pieces.] ...\n\n",
		stream, len(s)-budget, len(s),
	)
	// A budget too small to hold the marker plus useful text on both sides is
	// better spent on the marker alone: the agent still learns why it is short
	// and what to do, which is the part it acts on. Returning the full marker
	// would itself exceed the budget, which is how a cap configured below the
	// marker length used to overshoot, so fall back to a short form and finally
	// to hard truncation — whatever is returned must fit.
	if budget <= len(marker) {
		short := fmt.Sprintf("\n... [%s truncated: %d of %d characters omitted. Do NOT re-run unchanged; produce less output.] ...\n", stream, len(s)-budget, len(s))
		if len(short) <= budget {
			return short
		}
		return s[:budget]
	}

	remaining := budget - len(marker)
	head := remaining * 2 / 3
	tail := remaining - head
	return s[:head] + marker + s[len(s)-tail:]
}
