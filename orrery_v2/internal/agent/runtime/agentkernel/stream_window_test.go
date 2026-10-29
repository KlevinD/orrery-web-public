package agentkernel

import (
	"context"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

func TestStreamWindowRejectionRecoversOnlyBeforeAnyOutput(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			attempts, summaries := 0, 0
			base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				if strings.HasPrefix(messages[0].Content, compactionInstruction) {
					summaries++
					return llm.NewMessageStream([]*llm.Message{reply("Evidence retained.", "stop")}), nil
				}
				attempts++
				if attempts > 1 {
					return llm.NewMessageStream([]*llm.Message{reply("recovered answer", "stop")}), nil
				}
				reader, writer := llm.MessagePipe(0)
				go func() {
					defer writer.Close()
					if partial {
						writer.Send(&llm.Message{Role: llm.Assistant, Content: "provisional"}, nil)
					}
					writer.Send(nil, profilemodel.ErrContextWindowExceeded)
				}()
				return reader, nil
			}}
			r, input := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.AutoCompact = true
				c.CompactKeepMessages = 2
				c.CompactSummaryBytes = 256
				req.InputMessages = memoryHistory()
			})
			result, err := r.Run(context.Background(), input, discard)
			if partial {
				if !errors.Is(err, ErrModel) || attempts != 1 || summaries != 0 {
					t.Fatalf("partial output replayed: attempts=%d summaries=%d err=%v", attempts, summaries, err)
				}
			} else if err != nil || result.ResultText != "recovered answer" || attempts != 2 || summaries != 1 {
				t.Fatalf("empty-stream rejection failed recovery: attempts=%d summaries=%d result=%+v err=%v", attempts, summaries, result, err)
			}
		})
	}
}
