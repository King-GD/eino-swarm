package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

func init() {
	stepFuncs[StepSummary] = stepDef{roles: []string{RoleStats}, fn: (*run).summary}
	stepFuncs[StepHeat] = stepDef{roles: []string{RoleHeat}, fn: (*run).heat}
}

// fofaSource selects the FOFA API on the stats tool. Without it the server's
// default backend answers with numeric rule IDs and different totals, which
// the panel would then mislabel as FOFA counts.
const fofaSource = "fofa"

// summary records the asset and distinct-IP totals of the run's query. Clues
// counts the non-root nodes the graph steps found: the old panel took this
// number from the agent, but here the producer owns the graph, so the count
// is derived from it and is present even when the totals lookup fails.
func (r *run) summary(ctx context.Context) error {
	nodes, _ := r.graph.Len()
	clues := int64(nodes - 1)
	r.state.Summary = &Summary{Clues: &clues}
	var reply struct {
		Query    json.RawMessage            `json:"query"`
		Size     json.RawMessage            `json:"size"`
		Distinct map[string]json.RawMessage `json:"distinct"`
	}
	arguments := map[string]any{"query": r.input.Query, "fields": "ip", "uniq_count": "ip", "tool": fofaSource}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return err
	}
	if err := r.sameQuery(reply.Query); err != nil {
		return err
	}
	assets, hasAssets, err := exactCount(reply.Size)
	if err != nil {
		return fmt.Errorf("asset total: %w", err)
	}
	ips, hasIPs, err := exactCount(reply.Distinct["ip"])
	if err != nil {
		return fmt.Errorf("distinct ip total: %w", err)
	}
	if !hasAssets && !hasIPs {
		return fmt.Errorf("reply carries no totals")
	}
	if hasAssets {
		r.state.Summary.FOFAAssets = &assets
	}
	if hasIPs {
		r.state.Summary.FOFAIPs = &ips
	}
	return nil
}

// heat records the query's attention trend. The renderer draws it as a time
// series and refuses unordered or invalid dates, so the reply must already be
// strictly ascending; reordering or merging points would guess at the source.
func (r *run) heat(ctx context.Context) error {
	var reply struct {
		Query    json.RawMessage `json:"query"`
		HotTrend *[]struct {
			Date  string          `json:"date"`
			Value json.RawMessage `json:"value"`
		} `json:"hot_trend"`
	}
	if err := r.call(ctx, RoleHeat, map[string]any{"query": r.input.Query}, &reply); err != nil {
		return err
	}
	if err := r.sameQuery(reply.Query); err != nil {
		return err
	}
	if reply.HotTrend == nil {
		return fmt.Errorf("reply carries no trend")
	}
	points := make([]HeatPoint, 0, len(*reply.HotTrend))
	for _, item := range *reply.HotTrend {
		if _, err := time.Parse(time.DateOnly, item.Date); err != nil {
			return fmt.Errorf("trend date: %w", err)
		}
		if len(points) > 0 && item.Date <= points[len(points)-1].Date {
			return fmt.Errorf("trend dates are not strictly ascending")
		}
		value, err := finiteValue(item.Value)
		if err != nil {
			return err
		}
		points = append(points, HeatPoint{Date: item.Date, Value: value})
	}
	r.state.Heat = points
	return nil
}

// sameQuery rejects a reply that echoes a different query than the one sent:
// its numbers describe another asset range.
func (r *run) sameQuery(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var echoed string
	if json.Unmarshal(raw, &echoed) != nil || echoed != r.input.Query {
		return fmt.Errorf("reply describes another query")
	}
	return nil
}

// exactCount reads a non-negative integer written as a JSON number or numeric
// string. Fractions, exponents and values beyond int64 are refused rather
// than rounded. An absent value reports ok=false without an error.
func exactCount(raw json.RawMessage) (int64, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false, fmt.Errorf("not a number")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < 0 {
		return 0, false, fmt.Errorf("not an exact non-negative integer")
	}
	return value, true, nil
}

// finiteValue reads a non-negative finite measurement, which may be fractional.
func finiteValue(raw json.RawMessage) (float64, error) {
	var number json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &number) != nil {
		return 0, fmt.Errorf("trend value is not a number")
	}
	value, err := number.Float64()
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || value < 0 {
		return 0, fmt.Errorf("trend value is not a finite non-negative number")
	}
	return value, nil
}
