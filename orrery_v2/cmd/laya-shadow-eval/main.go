// laya-shadow-eval runs reviewed synthetic fixtures against the local model.
// It does not connect to Orrery databases, execute tools, or promote skills.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/platform/db"
)

type fixture struct {
	Name     string
	Job      laya.Job
	Expected []string
	Sample   *resourceadmin.RSILearningSample
}
type result struct {
	Name       string      `json:"name"`
	Expected   []string    `json:"expected"`
	Matches    int         `json:"matches"`
	Assessment laya.Record `json:"assessment"`
}

func fixtures() []fixture {
	knowledge := knowledgeadmin.BuildLayaSearchJob("synthetic/knowledge", "如何查询订单状态？", []knowledgeadmin.KnowledgeSearchResultDTO{
		{KnowledgeBaseID: "synthetic", DocumentID: "order-status", ChunkID: "1", Snippet: "使用订单号调用订单查询接口，返回支付状态、发货状态和物流单号。"},
		{KnowledgeBaseID: "synthetic", DocumentID: "password-reset", ChunkID: "2", Snippet: "忘记密码时，可以通过绑定邮箱收到重置密码链接。"},
		{KnowledgeBaseID: "synthetic", DocumentID: "order-help", ChunkID: "3", Snippet: "订单问题请联系人工客服；这里没有查询方法。"},
	})
	out := []fixture{{Name: "knowledge-zh", Job: knowledge, Expected: []string{"direct", "unrelated", "partial"}}}
	for _, item := range []struct{ name, tool, code, text, expected string }{
		{"rsi-network-en", "query_order", "http_503", "Upstream temporarily unavailable. Retrying the same request succeeded.", "environment"},
		{"rsi-arguments-en", "query_order", "invalid_arguments", "Required order_id was omitted from the tool arguments.", "tool_usage"},
		{"rsi-network-zh", "query_order", "http_503", "上游服务暂时不可用。使用相同参数重试后成功。", "environment"},
		{"rsi-arguments-zh", "query_order", "invalid_arguments", "调用订单查询工具时漏传了必填参数 order_id。", "tool_usage"},
		{"rsi-unknown-zh", "query_order", "tool_execution_failed", "操作失败，没有返回详细原因。", "insufficient"},
		{"rsi-skill-en", "query_order", "invalid_arguments", "Skill v1 always sends customer_id, but the API requires order_id. Reproduced on three orders; changing only the skill template fixes all three.", "skill_defect"},
		{"rsi-skill-zh", "query_order", "invalid_arguments", "技能v1模板固定传入customer_id，但接口要求order_id。三笔订单均复现；只修正技能模板后全部成功。", "skill_defect"},
		{"rsi-data-unknown-zh", "query_order", "order_not_found", "查询返回订单不存在；没有请求参数、订单记录或复现信息，无法确认原因。", "insufficient"},
		{"rsi-permission-unknown-zh", "query_order", "permission_denied", "接口拒绝访问；未提供权限配置、调用身份或请求内容，无法确认原因。", "insufficient"},
	} {
		sample := resourceadmin.RSILearningSample{Scope: `["synthetic","rsi-eval"]`, EvidenceRef: "synthetic:" + item.name, Tool: item.tool, ErrorCode: item.code, Summary: item.text}
		out = append(out, fixture{Name: item.name, Job: resourceadmin.BuildLayaRSISampleJob(sample), Expected: []string{item.expected}, Sample: &sample})
	}
	return out
}

func run() error {
	keyFile := flag.String("key-file", "", "plain key or deployed LAYA_API_KEY env file")
	endpoint := flag.String("url", "http://127.0.0.1:18490", "local Laya endpoint")
	rsiDB := flag.String("rsi-db", "", "optional isolated file database for synthetic RSI persistence evaluation")
	suite := flag.String("suite", "baseline", "synthetic fixture suite: baseline, memory or all")
	flag.Parse()
	items := fixtures()
	switch strings.TrimSpace(*suite) {
	case "baseline":
	case "memory":
		items = memoryFixtures()
	case "all":
		items = append(items, memoryFixtures()...)
	default:
		return fmt.Errorf("unknown fixture suite")
	}
	key, err := laya.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	client, err := laya.NewClient(*endpoint, key, 5*time.Second)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var samples *resourceadmin.PlatformRSILearningSampleStore
	if *rsiDB != "" {
		store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: *rsiDB})
		if err != nil {
			return err
		}
		defer store.Close()
		samples = resourceadmin.NewPlatformRSILearningSampleStore(store)
	}
	records := make(chan laya.Record, 1)
	observer := laya.NewObserver(ctx, client, func(record laya.Record) { records <- record })
	defer observer.Close()
	encoder := json.NewEncoder(os.Stdout)
	failed := false
	for _, item := range items {
		if samples != nil && item.Sample != nil {
			if _, err := samples.Save(ctx, *item.Sample); err != nil {
				return err
			}
		}
		observer.Submit(ctx, item.Job)
		select {
		case record := <-records:
			if samples != nil && item.Sample != nil {
				if err := samples.SaveAssessment(ctx, record); err != nil {
					return err
				}
			}
			matches := 0
			for i, decision := range record.Decisions {
				if i < len(item.Expected) && decision.Choice == item.Expected[i] {
					matches++
				}
			}
			if record.Fallback != "" {
				failed = true
			}
			if err := encoder.Encode(result{Name: item.Name, Expected: item.Expected, Matches: matches, Assessment: record}); err != nil {
				return err
			}
		case <-time.After(7 * time.Second):
			return fmt.Errorf("shadow evaluation timed out")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if failed {
		return fmt.Errorf("one or more model requests failed; inspect fallback fields")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
