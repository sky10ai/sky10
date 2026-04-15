package agent

import "sort"

// sortAgentInfos keeps agent lists deterministic for UI consumers.
func sortAgentInfos(agents []AgentInfo) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return agents[i].Name < agents[j].Name
		}
		if agents[i].DeviceID != agents[j].DeviceID {
			return agents[i].DeviceID < agents[j].DeviceID
		}
		return agents[i].ID < agents[j].ID
	})
}
