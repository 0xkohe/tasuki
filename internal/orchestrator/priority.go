package orchestrator

import (
	"sort"
	"strings"
	"time"

	"github.com/kooooohe/unblocked/internal/adapter"
	"github.com/kooooohe/unblocked/internal/config"
	"github.com/kooooohe/unblocked/internal/state"
)

// cycleFromRateLimitType extracts the canonical cycle label ("5h", "weekly",
// "monthly") from a RateLimitInfo.Type string. Returns "" for unknown inputs.
func cycleFromRateLimitType(rlType string) string {
	s := strings.ToLower(rlType)
	switch {
	case strings.HasPrefix(s, "five_hour"), strings.HasPrefix(s, "5_hour"), strings.Contains(s, "5h"):
		return "5h"
	case strings.HasPrefix(s, "seven_day"), strings.HasPrefix(s, "7_day"), strings.HasPrefix(s, "weekly"):
		return "weekly"
	case strings.HasPrefix(s, "monthly"):
		return "monthly"
	}
	return ""
}

// estimateResetsAt returns a conservative Unix timestamp for when a provider
// with the given cycle should be available again from `now`.
func estimateResetsAt(cycle string, now time.Time) int64 {
	switch cycle {
	case "5h":
		return now.Add(5 * time.Hour).Unix()
	case "weekly":
		return now.Add(7 * 24 * time.Hour).Unix()
	case "monthly":
		return now.Add(30 * 24 * time.Hour).Unix()
	}
	return now.Add(24 * time.Hour).Unix()
}

// selectionResult describes the outcome of selectStartingProvider.
type selectionResult struct {
	Index          int
	Reason         string // "fresh", "preferred", "recovered", "all_cooldown"
	CooldownUntil  time.Time
	ReplacedActive string // previous current name, when Reason == "recovered"
}

// selectStartingProvider picks which provider to run next.
//
// Resolution order:
//  1. `preferred` (from -p flag) is honored, even when the provider is in cooldown.
//  2. Expired cooldown entries are pruned.
//  3. Among providers not currently cooling down, the highest priority
//     (lowest config.ProviderPriority value) is chosen.
//  4. When every provider is in cooldown, the one that will recover soonest
//     is chosen so the user still gets a session.
//
// `currentName` is the provider currently active in the session. If the chosen
// provider differs and currentName was rate-limited, the reason is "recovered".
func selectStartingProvider(
	cfg *config.Config,
	providers []adapter.Provider,
	st *state.ProviderState,
	now time.Time,
	preferred string,
	currentName string,
) selectionResult {
	if len(providers) == 0 {
		return selectionResult{Index: -1, Reason: "fresh"}
	}

	if preferred != "" {
		for i, p := range providers {
			if p.Name() == preferred {
				res := selectionResult{Index: i, Reason: "preferred"}
				if st != nil {
					if cd, ok := st.Cooldowns[preferred]; ok && !cd.IsAvailable(now) {
						res.CooldownUntil = cd.ExpiresAt(now)
					}
				}
				return res
			}
		}
	}

	if st != nil {
		st.Prune(now)
	}

	type candidate struct {
		index    int
		priority int
		name     string
	}
	var available []candidate
	var cooling []candidate
	for i, p := range providers {
		name := p.Name()
		c := candidate{index: i, priority: cfg.ProviderPriority(name), name: name}
		if st != nil {
			if cd, ok := st.Cooldowns[name]; ok && !cd.IsAvailable(now) {
				cooling = append(cooling, c)
				continue
			}
		}
		available = append(available, c)
	}

	if len(available) == 0 {
		// All in cooldown — pick the one expiring soonest.
		if len(cooling) == 0 {
			return selectionResult{Index: 0, Reason: "fresh"}
		}
		sort.SliceStable(cooling, func(i, j int) bool {
			ei := st.Cooldowns[cooling[i].name].ExpiresAt(now)
			ej := st.Cooldowns[cooling[j].name].ExpiresAt(now)
			return ei.Before(ej)
		})
		return selectionResult{
			Index:         cooling[0].index,
			Reason:        "all_cooldown",
			CooldownUntil: st.Cooldowns[cooling[0].name].ExpiresAt(now),
		}
	}

	sort.SliceStable(available, func(i, j int) bool {
		if available[i].priority != available[j].priority {
			return available[i].priority < available[j].priority
		}
		return available[i].index < available[j].index
	})

	chosen := available[0]
	res := selectionResult{Index: chosen.index, Reason: "fresh"}
	if currentName != "" && chosen.name != currentName {
		res.Reason = "recovered"
		res.ReplacedActive = currentName
	}
	return res
}
