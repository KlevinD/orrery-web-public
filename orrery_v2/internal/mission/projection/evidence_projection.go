package projection

func refreshMissingEvidence(model *ReadModel) {
	submitted := map[string]struct{}{}
	for _, item := range model.Evidence.EvidenceItems {
		if item.EvidenceRequirementID != "" {
			submitted[item.EvidenceRequirementID] = struct{}{}
		}
	}
	var missing []RequiredEvidenceView
	for _, task := range model.TaskBoard.Tasks {
		for _, requirement := range task.RequiredEvidence {
			if _, ok := submitted[requirement.ID]; !ok {
				missing = append(missing, requirement)
			}
		}
	}
	model.Evidence.MissingRequirements = missing
}
