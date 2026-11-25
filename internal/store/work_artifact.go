package store

// artifactHasBasis follows actual prerequisite edges. A cancelled task or
// retracted finding cannot establish the basis for a currently reported result.
func artifactHasBasis(artifact NuwaNode, records map[string]NuwaNode) bool {
	pending := []NuwaNode{artifact}
	seen := map[string]bool{artifact.ID: true}
	var task, fact bool
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, id := range current.DependsOn {
			source, ok := records[id]
			if !ok || seen[id] || source.Inactive() ||
				(current.Scope == WorkScopeProject && source.Scope != WorkScopeProject) {
				continue
			}
			seen[id] = true
			task = task || source.Kind == "task"
			fact = fact || source.Kind == "fact"
			if task && fact {
				return true
			}
			pending = append(pending, source)
		}
	}
	return false
}
