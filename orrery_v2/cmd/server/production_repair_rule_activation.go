package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/reliability"
)

const (
	productionRepairRuleActivationAction       = "ontology.reliability.repair_rule.activate"
	productionRepairRuleActivationActionTypeID = "reliability.repair_rule.activate"
)

type productionRepairRuleActivationAuthorizer struct {
	repository reliability.WorkspaceRepository
	decisions  productionActionDecisionConsumer
	now        func() time.Time
}

func (a productionRepairRuleActivationAuthorizer) AuthorizeRepairRuleActivation(ctx context.Context, command reliability.RepairRuleCommand) error {
	if a.repository == nil || a.decisions == nil || command.Activation == nil {
		return fmt.Errorf("repair rule activation governance is unavailable")
	}
	authorization := *command.Activation
	if strings.TrimSpace(authorization.DecisionID) == "" || strings.TrimSpace(authorization.ReceiptID) == "" ||
		strings.TrimSpace(authorization.ArgsHash) == "" || strings.TrimSpace(authorization.PreflightEvidenceID) == "" {
		return fmt.Errorf("complete activation authorization material is required")
	}
	current, err := a.repository.GetRepairRule(ctx, command.Rule.TenantID, command.Rule.ID)
	if err != nil {
		return err
	}
	if !repairRuleActivationCandidateMatches(current, command.Rule) {
		return fmt.Errorf("activation candidate does not match the current proposed rule")
	}
	incident, err := a.repository.GetIncident(ctx, current.TenantID, current.SourceIncidentID)
	if err != nil {
		return err
	}
	diagnosis, err := a.repository.GetDiagnosisCase(ctx, current.TenantID, current.DiagnosisCaseID)
	if err != nil {
		return err
	}
	preflight, err := a.repository.GetRepairPreflight(ctx, current.TenantID, authorization.PreflightEvidenceID)
	if err != nil {
		return err
	}
	var target *reliability.RepairTarget
	if loaded, targetErr := a.repository.GetLatestRepairTarget(ctx, current.TenantID, incident.ID); targetErr == nil {
		target = &loaded
	} else if current.Strategy == reliability.RepairStrategyRetry {
		return targetErr
	}
	now := time.Now().UTC()
	if a.now != nil {
		now = a.now().UTC()
	}
	if blockers := repairRuleActivationBlockers(current, incident, &diagnosis, &preflight, target, now); len(blockers) > 0 {
		return fmt.Errorf("repair rule activation is blocked: %s", strings.Join(blockers, ","))
	}
	expectedHash := repairRuleActivationArgsHash(current, incident, diagnosis, preflight)
	if authorization.ArgsHash != expectedHash {
		return fmt.Errorf("activation authorization is stale")
	}
	actor, err := repairRuleActivationActor(command.Actor)
	if err != nil {
		return err
	}
	_, err = a.decisions.ConsumeActionDecision(ctx, hil.ActionReceiptConsumeInput{
		TenantID: current.TenantID, DecisionID: strings.TrimSpace(authorization.DecisionID), ReceiptID: strings.TrimSpace(authorization.ReceiptID),
		Actor: actor, Action: productionRepairRuleActivationAction, ActionTypeID: productionRepairRuleActivationActionTypeID,
		ObjectRefs: repairRuleActivationObjectRefs(current, incident, diagnosis, preflight), ArgsHash: expectedHash,
		Purpose: strings.TrimSpace(authorization.Purpose), RiskLevel: hil.RiskHigh,
	})
	if err != nil {
		return fmt.Errorf("consume repair rule activation receipt: %w", err)
	}
	return nil
}

func repairRuleActivationCandidateMatches(current, candidate reliability.RepairRule) bool {
	return current.Status == reliability.RepairRuleStatusProposed && candidate.Status == reliability.RepairRuleStatusActive &&
		!candidate.AutoApply && current.ID == candidate.ID && current.TenantID == candidate.TenantID && current.Version == candidate.Version &&
		current.SelectorFingerprint == candidate.SelectorFingerprint && current.Strategy == candidate.Strategy &&
		current.ActionTypeID == candidate.ActionTypeID && current.RiskLevel == candidate.RiskLevel && current.Confidence == candidate.Confidence &&
		current.SupportCount == candidate.SupportCount && current.SuccessCount == candidate.SuccessCount && current.FailureCount == candidate.FailureCount &&
		current.Reversible == candidate.Reversible && current.Idempotent == candidate.Idempotent &&
		current.SourceIncidentID == candidate.SourceIncidentID && current.DiagnosisCaseID == candidate.DiagnosisCaseID &&
		sameActivationTime(current.ExpiresAt, candidate.ExpiresAt)
}

func repairRuleActivationBlockers(rule reliability.RepairRule, incident reliability.FailureIncident, diagnosis *reliability.DiagnosisCase, preflight *reliability.RepairPreflight, target *reliability.RepairTarget, now time.Time) []string {
	blockers := make([]string, 0, 6)
	if rule.Status != reliability.RepairRuleStatusProposed || rule.ActivationDecisionID != "" {
		blockers = append(blockers, "rule_not_proposed")
	}
	if rule.ActionTypeID != string(productionRepairActionTypeID) || rule.SourceIncidentID != incident.ID || rule.SelectorFingerprint != incident.Fingerprint {
		blockers = append(blockers, "rule_binding_mismatch")
	}
	if diagnosis == nil || diagnosis.ID == "" || diagnosis.ID != rule.DiagnosisCaseID || diagnosis.IncidentID != incident.ID {
		blockers = append(blockers, "diagnosis_required")
	} else {
		if diagnosis.Status != reliability.DiagnosisCaseStatusConfirmed || diagnosis.IncidentVersion != incident.Version {
			blockers = append(blockers, "diagnosis_not_current")
		}
		if len(diagnosis.EvidenceIDs) == 0 {
			blockers = append(blockers, "diagnosis_evidence_required")
		}
	}
	expectedRepairHash := repairInvocationArgsHash(incident, rule, target)
	if preflight == nil || !currentReliabilityPreflight(*preflight, incident, rule, target, expectedRepairHash, now) {
		blockers = append(blockers, "current_preflight_required")
	}
	return blockers
}

func repairRuleActivationArgsHash(rule reliability.RepairRule, incident reliability.FailureIncident, diagnosis reliability.DiagnosisCase, preflight reliability.RepairPreflight) string {
	evidenceIDs := append([]string(nil), diagnosis.EvidenceIDs...)
	sort.Strings(evidenceIDs)
	parts := []string{
		productionRepairRuleActivationAction, rule.TenantID, rule.ID, strconv.FormatInt(rule.Version, 10), string(rule.Status),
		rule.SelectorFingerprint, string(rule.Strategy), rule.ActionTypeID, string(rule.RiskLevel), strconv.FormatBool(rule.Reversible),
		strconv.FormatBool(rule.Idempotent), rule.SourceIncidentID, incident.ID, strconv.FormatInt(incident.Version, 10),
		diagnosis.ID, strconv.FormatInt(diagnosis.Version, 10), string(diagnosis.Status), string(diagnosis.RootCauseCategory),
		strconv.FormatFloat(diagnosis.Confidence, 'g', -1, 64), diagnosis.RedactedSummary, strings.Join(evidenceIDs, "\x1f"),
		preflight.EvidenceID, preflight.ActionRunID, preflight.ArgsHash, preflight.CheckedAt.UTC().Format(time.RFC3339Nano),
		preflight.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func repairRuleActivationObjectRefs(rule reliability.RepairRule, incident reliability.FailureIncident, diagnosis reliability.DiagnosisCase, preflight reliability.RepairPreflight) []hil.ObjectRef {
	refs := []hil.ObjectRef{
		{Kind: "repair_rule", ID: rule.ID},
		{Kind: "failure_incident", ID: incident.ID},
		{Kind: "diagnosis_case", ID: diagnosis.ID},
		{Kind: "evidence_item", ID: preflight.EvidenceID},
	}
	seen := map[string]bool{"evidence_item\x00" + preflight.EvidenceID: true}
	for _, evidenceID := range diagnosis.EvidenceIDs {
		key := "evidence_item\x00" + strings.TrimSpace(evidenceID)
		if strings.TrimSpace(evidenceID) == "" || seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, hil.ObjectRef{Kind: "evidence_item", ID: strings.TrimSpace(evidenceID)})
	}
	return refs
}

func repairRuleActivationActor(actor reliability.ActorRef) (hil.ActorRef, error) {
	result := hil.ActorRef{ID: strings.TrimSpace(actor.ID)}
	switch strings.TrimSpace(actor.Kind) {
	case "human":
		result.Kind = hil.ActorHuman
	case "agent":
		result.Kind = hil.ActorAgent
	case "service":
		result.Kind = hil.ActorService
	default:
		return hil.ActorRef{}, fmt.Errorf("repair rule activation actor kind is invalid")
	}
	if result.ID == "" {
		return hil.ActorRef{}, fmt.Errorf("repair rule activation actor is required")
	}
	return result, nil
}

func sameActivationTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
