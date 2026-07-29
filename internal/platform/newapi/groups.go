package newapi

import (
	"encoding/json"
	"sort"
	"strings"
)

// autoGroupName is New API's pseudo group that dispatches across several real
// groups at request time. Its cost cannot be predicted before the request, so
// it is surfaced but never given a numeric ratio.
const autoGroupName = "auto"

// userGroupsResponse is GET /api/user/self/groups. The ratio is a number for
// real groups but the localized string "自动" for the auto group, so it must be
// decoded leniently.
type userGroupsResponse struct {
	Success bool                       `json:"success"`
	Data    map[string]userGroupDetail `json:"data"`
}

type userGroupDetail struct {
	Ratio       json.RawMessage `json:"ratio"`
	Description string          `json:"desc"`
}

// pricingResponse is the optional GET /api/pricing enrichment. Core group
// models come from authenticated GET /api/user/models?group=... because the
// pricing navigation module may be disabled by an operator.
type pricingResponse struct {
	Success     bool               `json:"success"`
	Data        []pricingItem      `json:"data"`
	GroupRatio  map[string]float64 `json:"group_ratio"`
	UsableGroup map[string]string  `json:"usable_group"`
	AutoGroups  []string           `json:"auto_groups"`
}

type pricingItem struct {
	ModelName    string   `json:"model_name"`
	EnableGroups []string `json:"enable_groups"`
}

// ratioValue returns the numeric ratio and whether it is actually a number.
func (d userGroupDetail) ratioValue() (float64, bool) {
	if len(d.Ratio) == 0 {
		return 0, false
	}
	var ratio float64
	if err := json.Unmarshal(d.Ratio, &ratio); err != nil {
		return 0, false
	}
	return ratio, true
}

// modelsByGroup inverts optional pricing data for display/cross-checking. It is
// never used to replace a successfully verified per-group model response.
func (p pricingResponse) modelsByGroup() map[string][]string {
	models := make(map[string][]string, len(p.UsableGroup))
	for group := range p.UsableGroup {
		models[group] = make([]string, 0)
	}
	for _, item := range p.Data {
		name := strings.TrimSpace(item.ModelName)
		if name == "" {
			continue
		}
		if containsFold(item.EnableGroups, "all") {
			for group := range models {
				models[group] = append(models[group], name)
			}
			continue
		}
		for _, group := range item.EnableGroups {
			group = strings.TrimSpace(group)
			if _, usable := models[group]; usable {
				models[group] = append(models[group], name)
			}
		}
	}
	for group := range models {
		models[group] = dedupeSorted(models[group])
	}
	return models
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func dedupeSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
