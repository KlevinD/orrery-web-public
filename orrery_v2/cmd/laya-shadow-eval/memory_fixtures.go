package main

import (
	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
)

// These reviewed synthetic labels are an evaluation aid, not a production
// accuracy estimate or training set. Stable preferences and constraints are
// deliberately included so they cannot be silently treated as disposable.
func memoryFixtures() []fixture {
	var out []fixture
	for _, item := range []struct {
		name, query string
		memories    []string
		expected    []string
	}{
		{"memory-orders-zh", "客户问订单A123是否已经发货。", []string{
			"订单A123已经发货，物流单号为SF456。",
			"客户的账号密码重置方式是邮箱验证。",
			"上次查询订单时因缺少物流数据，尚不能确认发货状态。",
		}, []string{"direct", "unrelated", "partial"}},
		{"memory-orders-en", "Has order A123 shipped?", []string{
			"Order A123 has shipped. Its tracking number is SF456.",
			"The customer's password reset method is email verification.",
			"The last order lookup lacked shipping data and did not establish shipment status.",
		}, []string{"direct", "unrelated", "partial"}},
		{"memory-preferences-zh", "按照用户喜欢的语言和形式解释月度报告。", []string{
			"用户偏好中文，先说结论，再列出证据。",
			"用户上个月查询过订单A123。",
			"用户表达过偏好，但没有记录偏好的内容。",
		}, []string{"direct", "unrelated", "insufficient"}},
		{"memory-preferences-en", "Explain the monthly report in the user's preferred language and format.", []string{
			"The user prefers Chinese, with conclusions first and supporting evidence second.",
			"The user looked up order A123 last month.",
			"The user expressed a preference, but the preference itself was not recorded.",
		}, []string{"direct", "unrelated", "insufficient"}},
		{"memory-constraint-zh", "准备把报告发送给外部供应商。", []string{
			"用户明确要求：向外部供应商发送报告前必须先征得本人同意。",
			"供应商上周更换了办公室的打印机。",
			"不知道该报告是否可以对外共享。",
		}, []string{"direct", "unrelated", "partial"}},
		{"memory-constraint-en", "Prepare to send the report to an external vendor.", []string{
			"The user requires explicit approval before any report is sent to an external vendor.",
			"The vendor replaced the office printer last week.",
			"It is not known whether this report may be shared externally.",
		}, []string{"direct", "unrelated", "partial"}},
	} {
		records := make([]memcore.MemoryRecord, len(item.memories))
		for i, text := range item.memories {
			records[i] = memcore.MemoryRecord{ID: item.name + "-" + string(rune('a'+i)), ProfileID: "synthetic-memory-profile", Content: text}
		}
		request := mempageindex.RecallRequest{Query: item.query, Subject: memcore.AccessSubject{AgentID: "synthetic-memory-agent"},
			ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: "synthetic-memory-profile"}}}
		out = append(out, fixture{Name: item.name, Job: mempageindex.BuildLayaRecallJob(request, records), Expected: item.expected})
	}
	return out
}
