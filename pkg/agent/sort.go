package agent

import "sort"

func dedupeAgentInfos(agents []AgentInfo) []AgentInfo {
	if len(agents) < 2 {
		return agents
	}
	out := agents[:0]
	seen := make(map[string]bool, len(agents))
	for _, agent := range agents {
		var key string
		switch {
		case agent.DeviceID != "" && agent.ID != "":
			key = "device-id:" + agent.DeviceID + "\x00" + agent.ID
		case agent.ID != "":
			key = "id:" + agent.ID
		case agent.DeviceID != "" || agent.Name != "":
			key = "device-name:" + agent.DeviceID + "\x00" + agent.Name
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, agent)
	}
	return out
}

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
